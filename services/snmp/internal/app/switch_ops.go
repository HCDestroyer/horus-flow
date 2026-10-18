package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Errores de las operaciones de switches.
var (
	// ErrSwitchNotFound: no es un switch sondeado del tenant (o no se ve con su alcance).
	ErrSwitchNotFound = errors.New("snmp: switch not found")
	// ErrSwitchNoStore: sin PostgreSQL no se guarda la elección de puertos.
	ErrSwitchNoStore = errors.New("snmp: switch port store not configured")
	// ErrSwitchNoFlows: sin ClickHouse no hay top de muestras sFlow.
	ErrSwitchNoFlows = errors.New("snmp: switch samples not available")
	// ErrSwitchVersion: If-Match no coincide.
	ErrSwitchVersion = pgstore.ErrVersion
)

// SwitchRepo guarda la elección de puertos (pgstore).
type SwitchRepo interface {
	PutPortSelection(ctx context.Context, tenant, routerID uuid.UUID, ports map[string]domain.PortChoice, actor string,
		expect int) (*domain.PortSelection, error)
	DeletePortSelection(ctx context.Context, tenant, routerID uuid.UUID) error
}

// SwitchFlows lee el top de muestras sFlow de un switch (ClickHouse).
type SwitchFlows interface {
	SwitchTop(ctx context.Context, tenant, switchID uuid.UUID, ifIndex uint32, by string, from, to time.Time, limit int) (
		[]domain.SwitchTopRow, uint64, uint64, error)
}

// Switches son las operaciones de la API de switches sobre el sondeador.
type Switches struct {
	Engine *Engine
	Repo   SwitchRepo // nil = sin PostgreSQL
	Flows  func() (SwitchFlows, bool)
	Now    func() time.Time
}

// PortInput es un puerto elegido en PUT /switches/{id}/ports.
type PortInput struct {
	Name   string `json:"name"`
	Uplink bool   `json:"uplink"`
}

// PortSelectionInput es el cuerpo de PUT /switches/{id}/ports: los puertos
// listados se sondean (máximo 64); uplink marca los de subida.
type PortSelectionInput struct {
	Ports []PortInput `json:"ports"`
}

func (s *Switches) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (s *Switches) inScope(tenant uuid.UUID, sites []uuid.UUID, id uuid.UUID) (map[string]bool, error) {
	if s.Engine == nil {
		return nil, ErrSwitchNotFound
	}
	names, site, ok := s.Engine.SwitchPortNames(tenant, id)
	if !ok || !siteAllowed(sites, site) {
		return nil, ErrSwitchNotFound
	}
	return names, nil
}

// ValidateSelection comprueba la elección contra los puertos descubiertos.
func ValidateSelection(in PortSelectionInput, names map[string]bool) (map[string]domain.PortChoice, []domain.FieldError) {
	var errs []domain.FieldError
	if in.Ports == nil {
		return nil, []domain.FieldError{{Field: "ports", Code: "REQUIRED", Msg: "Lista de puertos a monitorear (puede ser vacía)."}}
	}
	if len(in.Ports) > domain.MaxSwitchPorts {
		errs = append(errs, domain.FieldError{Field: "ports", Code: "TOO_MANY",
			Msg: "Máximo " + strconv.Itoa(domain.MaxSwitchPorts) + " puertos por switch (cardinalidad de las series)."})
	}
	out := map[string]domain.PortChoice{}
	for i, p := range in.Ports {
		name := strings.TrimSpace(p.Name)
		field := "ports[" + strconv.Itoa(i) + "].name"
		switch {
		case name == "":
			errs = append(errs, domain.FieldError{Field: field, Code: "REQUIRED", Msg: "Nombre del puerto (IF-MIB ifName)."})
		case !names[name]:
			errs = append(errs, domain.FieldError{Field: field, Code: "NOT_FOUND", Msg: "El switch no tiene ese puerto (último descubrimiento)."})
		case out[name].Monitored:
			errs = append(errs, domain.FieldError{Field: field, Code: "DUPLICATE", Msg: "Puerto repetido."})
		default:
			out[name] = domain.PortChoice{Monitored: true, Uplink: p.Uplink}
		}
	}
	return out, errs
}

// PutPorts guarda la elección de puertos de un switch, la aplica en caliente
// y devuelve los puertos. Devuelve también la elección guardada (auditoría).
func (s *Switches) PutPorts(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, id uuid.UUID, in PortSelectionInput, actor string,
	expect int) (*api.SwitchPorts, *domain.PortSelection, []domain.FieldError, error) {
	names, err := s.inScope(tenant, sites, id)
	if err != nil {
		return nil, nil, nil, err
	}
	if s.Repo == nil {
		return nil, nil, nil, ErrSwitchNoStore
	}
	ports, errs := ValidateSelection(in, names)
	if len(errs) > 0 {
		return nil, nil, errs, nil
	}
	sel, err := s.Repo.PutPortSelection(ctx, tenant, id, ports, actor, expect)
	if err != nil {
		return nil, nil, nil, err
	}
	s.Engine.SetPortSelection(id, sel)
	out, _, err := s.Engine.SwitchPorts(ctx, tenant, id)
	return out, sel, nil, err
}

// ResetPorts borra la elección (vuelve a la de por defecto).
func (s *Switches) ResetPorts(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, id uuid.UUID) (*api.SwitchPorts, error) {
	if _, err := s.inScope(tenant, sites, id); err != nil {
		return nil, err
	}
	if s.Repo == nil {
		return nil, ErrSwitchNoStore
	}
	if err := s.Repo.DeletePortSelection(ctx, tenant, id); err != nil {
		return nil, err
	}
	s.Engine.SetPortSelection(id, nil)
	out, _, err := s.Engine.SwitchPorts(ctx, tenant, id)
	return out, err
}

// SwitchTop es GET /switches/{id}/flows/top.
type SwitchTop struct {
	RouterID     uuid.UUID             `json:"router_id"`
	By           string                `json:"by"`
	IfIndex      *int                  `json:"if_index"`
	From         time.Time             `json:"from"`
	To           time.Time             `json:"to"`
	TotalBytes   uint64                `json:"total_bytes,string"`
	Samples      uint64                `json:"samples,string"`
	SFlowEnabled bool                  `json:"sflow_enabled"`
	Source       string                `json:"source"`
	Data         []domain.SwitchTopRow `json:"data"`
}

// Top devuelve el top de muestras sFlow del switch (fuente secundaria).
func (s *Switches) Top(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, id uuid.UUID, ifIndex int, by string, window time.Duration,
	limit int) (*SwitchTop, error) {
	if _, err := s.inScope(tenant, sites, id); err != nil {
		return nil, err
	}
	var flows SwitchFlows
	if s.Flows != nil {
		flows, _ = s.Flows()
	}
	if flows == nil {
		return nil, ErrSwitchNoFlows
	}
	to := s.now().UTC().Truncate(time.Minute).Add(time.Minute)
	from := to.Add(-window)
	rows, total, samples, err := flows.SwitchTop(ctx, tenant, id, uint32(max(ifIndex, 0)), by, from, to, limit) //nolint:gosec // ifIndex ≥ 0
	if err != nil {
		return nil, err
	}
	out := &SwitchTop{RouterID: id, By: by, From: from, To: to, TotalBytes: total, Samples: samples, Source: "secondary",
		Data: rows}
	if out.Data == nil {
		out.Data = []domain.SwitchTopRow{}
	}
	if ifIndex > 0 {
		out.IfIndex = &ifIndex
	}
	if r := s.Engine.find(tenant, id); r != nil {
		r.mu.Lock()
		out.SFlowEnabled = r.SFlow
		r.mu.Unlock()
	}
	return out, nil
}
