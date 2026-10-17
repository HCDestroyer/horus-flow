package app

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	consumptionapi "github.com/hcdestroyer/horus-flow/services/analytics/api/consumption"
	findingsapi "github.com/hcdestroyer/horus-flow/services/detection/api/customerfindings"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
	snmpapi "github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// Permisos de los bloques de la consulta 360 (contrato ArgosISP §3.1).
const (
	PermTrafficCustomerRead = "traffic.customer.read"
	PermSNMPRead            = "snmp.read"
	PermFindingsRead        = "security.findings.read"
	maxConsumptionItems     = 500
	maxConsumptionRange     = 400 * 24 * time.Hour
)

// Readers son los contratos en proceso de otros módulos que usa la consulta
// 360 (module.Services; se buscan en cada petición porque el proveedor puede
// registrarse después). Devuelven false si el módulo no está en el proceso.
type Readers struct {
	Consumption func() (consumptionapi.Reader, bool)
	Findings    func() (findingsapi.Reader, bool)
	Routers     func() (snmpapi.RouterStatusReader, bool)
	// Site y PrimaryRouter leen el inventario propio (nombre del nodo y su router principal).
	Site          func(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Site, error)
	PrimaryRouter func(ctx context.Context, t pgdb.TenantID, site uuid.UUID) (*domain.Router, error)
}

// Bloques de la consulta 360.
const (
	SectionExternal    = "external"
	SectionConsumption = "consumption"
	SectionHealth      = "health"
	SectionSecurity    = "security"
)

// Motivos de un bloque omitido.
const (
	OmitPermission    = "permission"
	OmitUnavailable   = "unavailable"
	OmitNotRequested  = "not_requested"
	OmitNotConfigured = "not_configured"
)

// IPLookupInput es IpLookupRequest.
type IPLookupInput struct {
	Address string     `json:"address"`
	At      *time.Time `json:"at"`
	Include []string   `json:"include"`
}

// Omitted es un bloque que no se devuelve y su motivo.
type Omitted struct {
	Section string `json:"section"`
	Reason  string `json:"reason"`
}

// RouterHealth es el router principal del nodo del cliente.
type RouterHealth struct {
	ID     uuid.UUID
	Name   string
	Status string
	Since  *time.Time
}

// Health es el bloque health.
type Health struct {
	Router          *RouterHealth
	ExporterStatus  *string
	LastFlowAt      *time.Time
	LastSeenMinutes int
}

// Security es el bloque security.
type Security struct {
	State        string
	OpenFindings int
	Findings     []findingsapi.Finding
}

// IPLookupResult es IpLookupResponse.
type IPLookupResult struct {
	Customer    domain.Customer
	SiteName    *string
	External    *domain.ExternalIdentity
	Consumption *consumptionapi.Summary
	Health      *Health
	Security    *Security
	At          time.Time
	Omitted     []Omitted
}

func parseIP(raw string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// IPLookup es POST /integrations/ip-lookup (consulta 360; la IP va en el cuerpo).
func (s *Integrations) IPLookup(ctx context.Context, in IPLookupInput) (*IPLookupResult, error) {
	p, t, err := scope(ctx, PermCustomersRead)
	if err != nil {
		return nil, err
	}
	var fe fieldErrs
	addr, ok := parseIP(in.Address)
	if !ok {
		fe.add("address", "INVALID_FORMAT", "IP no válida.")
	}
	now := s.ts()
	at := now
	if in.At != nil {
		at = in.At.UTC()
		if at.After(now.Add(5 * time.Minute)) {
			fe.add("at", "OUT_OF_RANGE", "No puede ser futuro.")
		}
		if at.After(now) {
			at = now
		}
	}
	include := map[string]bool{SectionConsumption: true, SectionHealth: true, SectionSecurity: true}
	if in.Include != nil {
		include = map[string]bool{}
		for _, x := range in.Include {
			if !slices.Contains([]string{SectionConsumption, SectionHealth, SectionSecurity}, x) {
				fe.add("include", "INVALID_VALUE", "consumption, health o security.")
				continue
			}
			include[x] = true
		}
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	found, err := s.store.CustomersContaining(ctx, t, []netip.Addr{addr}, sitesFor(p, PermCustomersRead))
	if err != nil {
		return nil, err
	}
	c, ok := found[addr]
	if !ok {
		return nil, apperr.NotFound(domain.CodeCustomerNotFound)
	}
	res := &IPLookupResult{Customer: c, At: at, Omitted: []Omitted{}}
	omit := func(section, reason string) { res.Omitted = append(res.Omitted, Omitted{Section: section, Reason: reason}) }
	site := "site:" + c.SiteID.String()
	if s.Readers.Site != nil {
		if st, err := s.Readers.Site(ctx, t, c.SiteID); err == nil {
			res.SiteName = &st.Name
		}
	}
	// Identidad externa vigente en at.
	res.External, err = s.store.IdentityAt(ctx, t, c.ID, at)
	if err != nil {
		return nil, err
	}
	if res.External == nil {
		if _, err := s.store.GetArgosConnector(ctx, t); errors.Is(err, domain.ErrNotFound) {
			omit(SectionExternal, OmitNotConfigured)
		}
	}
	// Consumo.
	switch {
	case !include[SectionConsumption]:
		omit(SectionConsumption, OmitNotRequested)
	case !p.HasScope(PermTrafficCustomerRead, site):
		omit(SectionConsumption, OmitPermission)
	default:
		r, ok := lookupReader(s.Readers.Consumption)
		if !ok {
			omit(SectionConsumption, OmitUnavailable)
			break
		}
		sum, err := r.Summary(ctx, t.UUID(), c.RealmID, c.Address.Addr(), at, c.ResetAt)
		if err != nil {
			s.logger.WarnContext(ctx, "ip lookup: consumption unavailable", "error", err)
			omit(SectionConsumption, OmitUnavailable)
			break
		}
		res.Consumption = sum
	}
	// Salud.
	switch {
	case !include[SectionHealth]:
		omit(SectionHealth, OmitNotRequested)
	case !p.HasScope(PermSNMPRead, site):
		omit(SectionHealth, OmitPermission)
	default:
		h := &Health{LastSeenMinutes: max(0, int(now.Sub(c.LastSeen).Minutes()))}
		res.Health = h
		var primary *domain.Router
		if s.Readers.PrimaryRouter != nil {
			primary, _ = s.Readers.PrimaryRouter(ctx, t, c.SiteID)
		}
		if primary == nil {
			break
		}
		name := primary.Hostname
		if primary.DisplayName != nil && *primary.DisplayName != "" {
			name = *primary.DisplayName
		}
		h.Router = &RouterHealth{ID: primary.ID, Name: name, Status: "unknown"}
		if r, ok := lookupReader(s.Readers.Routers); ok {
			if list, _, err := r.List(ctx, t.UUID(), []uuid.UUID{c.SiteID}); err == nil {
				for _, st := range list {
					if st.RouterID == primary.ID {
						h.Router.Status, h.Router.Since, h.ExporterStatus = st.Status, st.Since, st.Signals.Exporter
						if st.Name != "" {
							h.Router.Name = st.Name
						}
					}
				}
			}
		}
	}
	// Seguridad.
	switch {
	case !include[SectionSecurity]:
		omit(SectionSecurity, OmitNotRequested)
	case !p.HasScope(PermFindingsRead, site):
		omit(SectionSecurity, OmitPermission)
	default:
		sec := &Security{State: c.SecurityState, OpenFindings: c.OpenFindings, Findings: []findingsapi.Finding{}}
		res.Security = sec
		if r, ok := lookupReader(s.Readers.Findings); ok {
			if list, err := r.ActiveFindings(ctx, c.ID, 20); err == nil {
				sec.Findings = list
			} else {
				s.logger.WarnContext(ctx, "ip lookup: findings unavailable", "error", err)
			}
		}
	}
	sections := []string{}
	for _, x := range []string{SectionConsumption, SectionHealth, SectionSecurity} {
		if include[x] {
			sections = append(sections, x)
		}
	}
	s.record(ctx, t, "integrations.ip_lookup", "customer", c.ID.String(), map[string]any{"sections": sections, "at": at.Format(time.RFC3339)})
	return res, nil
}

func lookupReader[T any](f func() (T, bool)) (T, bool) {
	var zero T
	if f == nil {
		return zero, false
	}
	return f()
}

// ConsumptionItem es un elemento de ConsumptionRequest.
type ConsumptionItem struct {
	Address           *string `json:"address"`
	ExternalServiceID *int64  `json:"external_service_id"`
}

// ConsumptionInput es ConsumptionRequest.
type ConsumptionInput struct {
	From        *time.Time        `json:"from"`
	To          *time.Time        `json:"to"`
	Granularity string            `json:"granularity"`
	Items       []ConsumptionItem `json:"items"`
}

// ConsumptionDay es un día de la serie.
type ConsumptionDay struct {
	Day       time.Time
	UpBytes   uint64
	DownBytes uint64
}

// ConsumptionRow es un elemento de la respuesta.
type ConsumptionRow struct {
	Address           *string
	ExternalServiceID *int64
	UpBytes           uint64
	DownBytes         uint64
	Addresses         []string
	Series            []ConsumptionDay
	Error             *string
}

// ConsumptionResult es ConsumptionResponse.
type ConsumptionResult struct {
	From, To    time.Time
	Granularity string
	Items       []ConsumptionRow
}

// Consumption es POST /integrations/consumption: consumo por IP o por
// servicio de ArgosISP (suma las IPs que tuvo el servicio, cada una en su vigencia).
func (s *Integrations) Consumption(ctx context.Context, in ConsumptionInput) (*ConsumptionResult, error) {
	p, t, err := scope(ctx, PermTrafficCustomerRead)
	if err != nil {
		return nil, err
	}
	var fe fieldErrs
	if in.From == nil {
		fe.add("from", "REQUIRED", "Obligatorio.")
	}
	if in.To == nil {
		fe.add("to", "REQUIRED", "Obligatorio.")
	}
	if in.From != nil && in.To != nil {
		if !in.From.Before(*in.To) {
			fe.add("to", "OUT_OF_RANGE", "Debe ser posterior a from.")
		} else if in.To.Sub(*in.From) > maxConsumptionRange {
			fe.add("to", "OUT_OF_RANGE", "Máximo 400 días.")
		}
	}
	gran := in.Granularity
	if gran == "" {
		gran = "total"
	}
	if gran != "day" && gran != "total" {
		fe.add("granularity", "INVALID_VALUE", "day o total.")
	}
	if len(in.Items) < 1 || len(in.Items) > maxConsumptionItems {
		fe.add("items", "OUT_OF_RANGE", "Entre 1 y 500 elementos.")
	}
	byService := false
	addrs := map[netip.Addr]bool{}
	for i, it := range in.Items {
		switch {
		case (it.Address == nil) == (it.ExternalServiceID == nil):
			fe.add("items["+itoa(i)+"]", "INVALID_VALUE", "Indique address o external_service_id (uno de los dos).")
		case it.Address != nil:
			if a, ok := parseIP(*it.Address); ok {
				addrs[a] = true
			}
		case *it.ExternalServiceID < 1:
			fe.add("items["+itoa(i)+"].external_service_id", "OUT_OF_RANGE", "Id positivo.")
		default:
			byService = true
		}
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	if byService {
		conn, err := s.store.GetArgosConnector(ctx, t)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && !conn.Enabled) {
			return nil, apperr.Validation(apperr.Field("items", "INTEGRATION_NOT_CONFIGURED",
				"Sin conector ArgosISP activo solo se acepta address."))
		}
		if err != nil {
			return nil, err
		}
	}
	reader, ok := lookupReader(s.Readers.Consumption)
	if !ok {
		return nil, apperr.New(apperr.KindUnavailable, codeAnalyticsUnavailable, "El consumo no está disponible en este proceso.")
	}
	from, to := in.From.UTC(), in.To.UTC()
	sites := sitesFor(p, PermTrafficCustomerRead)
	allowed := func(site uuid.UUID) bool { return sites == nil || slices.Contains(sites, site) }
	// Ventanas por elemento.
	list := make([]netip.Addr, 0, len(addrs))
	for a := range addrs {
		list = append(list, a)
	}
	custs, err := s.store.CustomersContaining(ctx, t, list, sites)
	if err != nil {
		return nil, err
	}
	var svcIDs []int64
	for _, it := range in.Items {
		if it.ExternalServiceID != nil {
			svcIDs = append(svcIDs, *it.ExternalServiceID)
		}
	}
	windowsBySvc := map[int64][]ServiceWindow{}
	if len(svcIDs) > 0 {
		ws, err := s.store.ServiceWindows(ctx, t, svcIDs, from, to)
		if err != nil {
			return nil, err
		}
		for _, w := range ws {
			if allowed(w.SiteID) {
				windowsBySvc[w.ServiceID] = append(windowsBySvc[w.ServiceID], w)
			}
		}
	}
	var windows []consumptionapi.Window
	owner := []int{} // ventana → elemento
	rows := make([]ConsumptionRow, len(in.Items))
	notFound := "not_found"
	for i, it := range in.Items {
		row := &rows[i]
		row.Address, row.ExternalServiceID = it.Address, it.ExternalServiceID
		row.Series = []ConsumptionDay{}
		if it.Address != nil {
			a, _ := parseIP(*it.Address)
			c, ok := custs[a]
			if !ok {
				row.Error = &notFound
				continue
			}
			windows = append(windows, consumptionapi.Window{RealmID: c.RealmID, Address: c.Address.Addr(), From: from, To: to})
			owner = append(owner, i)
			continue
		}
		ws := windowsBySvc[*it.ExternalServiceID]
		if len(ws) == 0 {
			row.Error = &notFound
			continue
		}
		row.Addresses = []string{}
		for _, w := range ws {
			wf, wt := w.From, to
			if wf.Before(from) {
				wf = from
			}
			if w.To != nil && w.To.Before(wt) {
				wt = *w.To
			}
			if !wf.Before(wt) {
				continue
			}
			windows = append(windows, consumptionapi.Window{RealmID: w.RealmID, Address: w.Address.Addr(), From: wf, To: wt})
			owner = append(owner, i)
			if a := w.Address.Addr().String(); !slices.Contains(row.Addresses, a) {
				row.Addresses = append(row.Addresses, a)
			}
		}
	}
	days, err := reader.Daily(ctx, t.UUID(), windows)
	if err != nil {
		if errors.Is(err, consumptionapi.ErrUnavailable) {
			return nil, apperr.New(apperr.KindUnavailable, codeAnalyticsUnavailable, "ClickHouse no responde.")
		}
		return nil, err
	}
	series := make([]map[int64]*ConsumptionDay, len(rows))
	for wi, list := range days {
		i := owner[wi]
		row := &rows[i]
		for _, d := range list {
			row.UpBytes += d.UpBytes
			row.DownBytes += d.DownBytes
			if gran != "day" {
				continue
			}
			if series[i] == nil {
				series[i] = map[int64]*ConsumptionDay{}
			}
			x := series[i][d.Day.Unix()]
			if x == nil {
				x = &ConsumptionDay{Day: d.Day}
				series[i][d.Day.Unix()] = x
			}
			x.UpBytes += d.UpBytes
			x.DownBytes += d.DownBytes
		}
	}
	for i, m := range series {
		for _, d := range m {
			rows[i].Series = append(rows[i].Series, *d)
		}
		slices.SortFunc(rows[i].Series, func(a, b ConsumptionDay) int { return a.Day.Compare(b.Day) })
	}
	s.record(ctx, t, "integrations.consumption", "customer", "", map[string]any{"items": len(rows), "by_service": byService,
		"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)})
	return &ConsumptionResult{From: from, To: to, Granularity: gran, Items: rows}, nil
}

func itoa(i int) string { return strconv.Itoa(i) }

const codeAnalyticsUnavailable = "ANALYTICS_UNAVAILABLE"
