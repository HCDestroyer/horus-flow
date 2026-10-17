package app

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	secstatesapi "github.com/hcdestroyer/horus-flow/services/detection/api/securitystates"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

const (
	maxSecurityStateItems = 500
	codeSecurityUnavail   = "SECURITY_UNAVAILABLE"
	// ItemNotFound: la IP no es de un cliente del ISP (o fuera del alcance) o
	// el servicio no tiene identidad vigente.
	ItemNotFound = "not_found"
)

// SecurityStateItem es un elemento de SecurityStatesRequest.
type SecurityStateItem struct {
	Address           *string `json:"address"`
	ExternalServiceID *int64  `json:"external_service_id"`
}

// SecurityStatesInput es SecurityStatesRequest.
type SecurityStatesInput struct {
	Items []SecurityStateItem `json:"items"`
}

// SecurityStateRow es un elemento de la respuesta (Error ≠ nil: sin datos).
type SecurityStateRow struct {
	Address           *string
	ExternalServiceID *int64
	State             string
	OpenFindings      int
	MaxSeverity       *string
	Since             *time.Time
	Error             *string
}

// SecurityStatesResult es SecurityStatesResponse.
type SecurityStatesResult struct {
	Items []SecurityStateRow
	At    time.Time
}

// securityRank ordena los estados de peor a mejor (infected > suspected >
// mitigated > clean).
func securityRank(state string) int {
	switch state {
	case "infected":
		return 3
	case "suspected":
		return 2
	case "mitigated":
		return 1
	}
	return 0
}

// SecurityStates es POST /integrations/security-states (contrato ArgosISP
// §3.5): estado de seguridad actual de hasta 500 clientes por IP o por
// servicio de ArgosISP, para la columna "Seguridad" de las listas de Argos.
// Lee el estado de detection (securitystates.Reader). Un servicio con varias
// IPs (IPv4 y prefijo IPv6) devuelve la de peor estado.
func (s *Integrations) SecurityStates(ctx context.Context, in SecurityStatesInput) (*SecurityStatesResult, error) {
	p, t, err := scope(ctx, PermFindingsRead)
	if err != nil {
		return nil, err
	}
	var fe fieldErrs
	if len(in.Items) < 1 || len(in.Items) > maxSecurityStateItems {
		fe.add("items", "OUT_OF_RANGE", "Entre 1 y 500 elementos.")
	}
	byService := false
	addrs := map[netip.Addr]bool{}
	var svcIDs []int64
	for i, it := range in.Items {
		switch {
		case (it.Address == nil) == (it.ExternalServiceID == nil):
			fe.add("items["+itoa(i)+"]", "INVALID_VALUE", "Indique address o external_service_id (uno de los dos).")
		case it.Address != nil:
			a, ok := parseIP(*it.Address)
			if !ok {
				fe.add("items["+itoa(i)+"].address", "INVALID_FORMAT", "IP no válida.")
				continue
			}
			addrs[a] = true
		case *it.ExternalServiceID < 1:
			fe.add("items["+itoa(i)+"].external_service_id", "OUT_OF_RANGE", "Id positivo.")
		default:
			byService = true
			svcIDs = append(svcIDs, *it.ExternalServiceID)
		}
	}
	if err := fe.err(); err != nil {
		return nil, err
	}
	if byService {
		conn, err := s.store.GetArgosConnector(ctx, t)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && !conn.Enabled) {
			return nil, apperr.Validation(apperr.Field("items", CodeIntegrationNotConfig,
				"Sin conector ArgosISP activo solo se acepta address."))
		}
		if err != nil {
			return nil, err
		}
	}
	reader, ok := lookupReader(s.Readers.SecurityStates)
	if !ok {
		return nil, apperr.New(apperr.KindUnavailable, codeSecurityUnavail, "El estado de seguridad no está disponible en este proceso.")
	}
	sites := sitesFor(p, PermFindingsRead)
	list := make([]netip.Addr, 0, len(addrs))
	for a := range addrs {
		list = append(list, a)
	}
	custs, err := s.store.CustomersContaining(ctx, t, list, sites)
	if err != nil {
		return nil, err
	}
	bySvc := map[int64][]ServiceIdentity{}
	if len(svcIDs) > 0 {
		idents, err := s.store.ServiceIdentities(ctx, t, svcIDs, nil, sites)
		if err != nil {
			return nil, err
		}
		for _, x := range idents {
			if x.Identity.ExternalServiceID != nil {
				bySvc[*x.Identity.ExternalServiceID] = append(bySvc[*x.Identity.ExternalServiceID], x)
			}
		}
	}
	// Clientes a consultar en detection (sin repetir).
	seen := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	add := func(id uuid.UUID) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	addrCusts := make([]uuid.UUID, 0, len(custs))
	for _, c := range custs {
		add(c.ID)
		addrCusts = append(addrCusts, c.ID)
	}
	for _, xs := range bySvc {
		for _, x := range xs {
			add(x.Customer.ID)
		}
	}
	states, err := reader.SecurityStates(ctx, ids)
	if err != nil {
		return nil, err
	}
	// Servicio de ArgosISP de cada cliente pedido por IP (identidad vigente).
	current, err := s.store.CurrentIdentities(ctx, t, addrCusts)
	if err != nil {
		return nil, err
	}
	stateOf := func(c *domain.Customer) secstatesapi.State {
		if st, ok := states[c.ID]; ok {
			return st
		}
		// Sin estado guardado en detection: nunca tuvo hallazgos.
		return secstatesapi.State{State: "clean", Since: c.FirstSeen}
	}
	notFound := ItemNotFound
	rows := make([]SecurityStateRow, len(in.Items))
	for i, it := range in.Items {
		row := &rows[i]
		row.Address, row.ExternalServiceID = it.Address, it.ExternalServiceID
		var c *domain.Customer
		if it.Address != nil {
			a, _ := parseIP(*it.Address)
			if x, ok := custs[a]; ok {
				c = &x
				if cur, ok := current[x.ID]; ok {
					row.ExternalServiceID = cur.ExternalServiceID
				}
			}
		} else if xs := bySvc[*it.ExternalServiceID]; len(xs) > 0 {
			best := 0
			for j := 1; j < len(xs); j++ {
				a, b := stateOf(&xs[j].Customer), stateOf(&xs[best].Customer)
				if r1, r2 := securityRank(a.State), securityRank(b.State); r1 > r2 || (r1 == r2 && a.OpenFindings > b.OpenFindings) {
					best = j
				}
			}
			c = &xs[best].Customer
			addr := c.AddressString()
			row.Address = &addr
		}
		if c == nil {
			row.Error = &notFound
			continue
		}
		st := stateOf(c)
		since := st.Since.UTC()
		row.State, row.OpenFindings, row.MaxSeverity, row.Since = st.State, st.OpenFindings, st.MaxSeverity, &since
	}
	s.record(ctx, t, "integrations.security_states", "customer", "", map[string]any{"items": len(rows), "by_service": byService})
	return &SecurityStatesResult{Items: rows, At: s.ts()}, nil
}
