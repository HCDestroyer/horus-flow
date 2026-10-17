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
	"github.com/hcdestroyer/horus-flow/services/analytics/api/servingrouter"
	"github.com/hcdestroyer/horus-flow/services/collector/api/lastflow"
	findingsapi "github.com/hcdestroyer/horus-flow/services/detection/api/customerfindings"
	secstatesapi "github.com/hcdestroyer/horus-flow/services/detection/api/securitystates"
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
	// SecurityStates: estado de seguridad por lotes (POST /integrations/security-states).
	SecurityStates func() (secstatesapi.Reader, bool)
	Routers        func() (snmpapi.RouterStatusReader, bool)
	// Site y PrimaryRouter leen el inventario propio (nombre del nodo y su router principal).
	Site          func(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Site, error)
	PrimaryRouter func(ctx context.Context, t pgdb.TenantID, site uuid.UUID) (*domain.Router, error)
	// Router lee un router del inventario (el que sirve al cliente).
	Router func(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
	// ServingRouter: router de los flujos más recientes del cliente (analytics).
	ServingRouter func() (servingrouter.Reader, bool)
	// LastFlow: último flujo de cada exportador (estado del collector, rol ingester).
	LastFlow func() (lastflow.Reader, bool)
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

// IPLookupInput es IpLookupRequest: address o, desde el contrato 1.2.0,
// external_service_id (exactamente uno de los dos).
type IPLookupInput struct {
	Address           *string    `json:"address"`
	ExternalServiceID *int64     `json:"external_service_id"`
	At                *time.Time `json:"at"`
	Include           []string   `json:"include"`
}

// Omitted es un bloque que no se devuelve y su motivo.
type Omitted struct {
	Section string `json:"section"`
	Reason  string `json:"reason"`
}

// Origen del router de la consulta 360.
const (
	// RouterSourceFlows: el router de los flujos más recientes del cliente.
	RouterSourceFlows = "flows"
	// RouterSourcePrimary: sin flujos recientes del cliente (o analítica no
	// disponible): el router principal de su nodo.
	RouterSourcePrimary = "primary_router"
)

// RouterHealth es el router que lleva el tráfico del cliente (o, si no se
// sabe, el principal de su nodo; Source lo indica).
type RouterHealth struct {
	ID     uuid.UUID
	SiteID uuid.UUID
	Name   string
	Status string
	Since  *time.Time
	Source string
	// CustomerLastFlowAt es el flujo más reciente del cliente por ese router
	// (solo con Source = flows).
	CustomerLastFlowAt *time.Time
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
	var addr netip.Addr
	switch {
	case (in.Address == nil) == (in.ExternalServiceID == nil):
		fe.add("address", "INVALID_VALUE", "Indique address o external_service_id (uno de los dos).")
	case in.Address != nil:
		a, ok := parseIP(*in.Address)
		if !ok {
			fe.add("address", "INVALID_FORMAT", "IP no válida.")
		}
		addr = a
	case *in.ExternalServiceID < 1:
		fe.add("external_service_id", "OUT_OF_RANGE", "Id positivo.")
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
	var c domain.Customer
	if in.ExternalServiceID != nil {
		// Por servicio de ArgosISP: la identidad del servicio vigente en at (o ahora).
		var atp *time.Time
		if in.At != nil {
			atp = &at
		}
		list, err := s.store.ServiceIdentities(ctx, t, []int64{*in.ExternalServiceID}, atp, sitesFor(p, PermCustomersRead))
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, apperr.NotFound(domain.CodeCustomerNotFound)
		}
		c = primaryOf(list).Customer
	} else {
		found, err := s.store.CustomersContaining(ctx, t, []netip.Addr{addr}, sitesFor(p, PermCustomersRead))
		if err != nil {
			return nil, err
		}
		var ok bool
		if c, ok = found[addr]; !ok {
			return nil, apperr.NotFound(domain.CodeCustomerNotFound)
		}
	}
	res := &IPLookupResult{Customer: c, At: at, Omitted: []Omitted{}}
	omit := func(section, reason string) {
		res.Omitted = append(res.Omitted, Omitted{Section: section, Reason: reason})
	}
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
		h.Router = s.healthRouter(ctx, t, c, at)
		if h.Router == nil {
			break
		}
		if r, ok := lookupReader(s.Readers.Routers); ok {
			if list, _, err := r.List(ctx, t.UUID(), []uuid.UUID{h.Router.SiteID}); err == nil {
				for _, st := range list {
					if st.RouterID == h.Router.ID {
						h.Router.Status, h.Router.Since, h.ExporterStatus, h.LastFlowAt = st.Status, st.Since, st.Signals.Exporter, st.LastFlowAt
						if st.Name != "" {
							h.Router.Name = st.Name
						}
					}
				}
			}
		}
		// Último flujo del exportador: el estado del collector (más fresco que el último sondeo SNMP).
		if r, ok := lookupReader(s.Readers.LastFlow); ok {
			if m, err := r.LastFlowAt(ctx, t.UUID(), []uuid.UUID{h.Router.ID}); err == nil {
				if v, ok := m[h.Router.ID]; ok {
					h.LastFlowAt = &v
				}
			} else {
				s.logger.WarnContext(ctx, "ip lookup: last flow unavailable", "error", err)
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
	audit := map[string]any{"sections": sections, "at": at.Format(time.RFC3339)}
	if in.ExternalServiceID != nil {
		audit["by"] = "external_service_id"
	}
	s.record(ctx, t, "integrations.ip_lookup", "customer", c.ID.String(), audit)
	return res, nil
}

func routerName(r *domain.Router) string {
	if r.DisplayName != nil && *r.DisplayName != "" {
		return *r.DisplayName
	}
	return r.Hostname
}

// healthRouter elige el router que lleva el tráfico del cliente: el de sus
// flujos más recientes (hasta 24 h antes de at); si no hay, la analítica no
// está o el router ya no existe, el principal de su nodo con
// Source = primary_router.
func (s *Integrations) healthRouter(ctx context.Context, t pgdb.TenantID, c domain.Customer, at time.Time) *RouterHealth {
	if r, ok := lookupReader(s.Readers.ServingRouter); ok && s.Readers.Router != nil {
		sv, err := r.ServingRouter(ctx, t.UUID(), servingrouter.Query{SiteID: c.SiteID, RealmID: c.RealmID, Prefix: c.Address, At: at})
		switch {
		case err != nil:
			s.logger.WarnContext(ctx, "ip lookup: serving router unavailable (primary router used)", "error", err)
		case sv != nil:
			if rt, err := s.Readers.Router(ctx, t, sv.RouterID); err == nil && rt != nil {
				last := sv.LastFlowAt
				return &RouterHealth{ID: rt.ID, SiteID: rt.SiteID, Name: routerName(rt), Status: "unknown", Source: RouterSourceFlows,
					CustomerLastFlowAt: &last}
			}
		}
	}
	if s.Readers.PrimaryRouter == nil {
		return nil
	}
	primary, _ := s.Readers.PrimaryRouter(ctx, t, c.SiteID)
	if primary == nil {
		return nil
	}
	return &RouterHealth{ID: primary.ID, SiteID: primary.SiteID, Name: routerName(primary), Status: "unknown", Source: RouterSourcePrimary}
}

// primaryOf elige, entre las identidades de un mismo servicio (IPv4 y
// prefijo IPv6 son clientes distintos en Horus), la del cliente visto más
// recientemente; con el mismo instante, la IPv4. list viene ordenada por
// last_seen descendente.
func primaryOf(list []ServiceIdentity) ServiceIdentity {
	best := list[0]
	for _, x := range list[1:] {
		if x.Customer.LastSeen.Equal(best.Customer.LastSeen) && x.Customer.Address.Addr().Is4() && !best.Customer.Address.Addr().Is4() {
			best = x
		}
	}
	return best
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
