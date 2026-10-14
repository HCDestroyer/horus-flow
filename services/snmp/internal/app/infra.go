package app

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Servicios de infraestructura: inventario (CRUD por tenant, RLS) y estado
// que escribe el planificador (infra_scheduler.go).

// Permisos del contrato (snmp.yaml, infra-services).
const (
	PermSNMPRead   = "snmp.read"
	PermSNMPManage = "snmp.manage"
)

// TypeServiceStateChanged es el evento de cambio de estado (catálogo
// packages/events/catalog/snmp.yaml; contrato fijo para alerts).
const TypeServiceStateChanged = "horus.snmp.service.state_changed"

// Errores del almacén.
var (
	ErrInfraNotFound  = errors.New("snmp: infra service not found")
	ErrInfraVersion   = errors.New("snmp: infra service version mismatch")
	ErrInfraDuplicate = errors.New("snmp: infra service name already exists")
)

// InfraStore es el almacén PostgreSQL (esquema snmp).
type InfraStore interface {
	List(ctx context.Context, t pgdb.TenantID) ([]domain.Service, error)
	Get(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Service, error)
	Count(ctx context.Context, t pgdb.TenantID) (int, error)
	Insert(ctx context.Context, t pgdb.TenantID, s *domain.Service) error
	// Update guarda la configuración si version = expect y borra el estado
	// de los chequeos que ya no están.
	Update(ctx context.Context, t pgdb.TenantID, s *domain.Service, expect int) error
	Delete(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int) error
	// LoadAll lee todos los servicios de la plataforma con su estado
	// (planificador; rol de plataforma, multi-tenant declarado).
	LoadAll(ctx context.Context) ([]domain.Service, error)
	// SaveState guarda el estado de los chequeos y del servicio, y el evento
	// (si lo hay) en el outbox, en una transacción del tenant.
	SaveState(ctx context.Context, t pgdb.TenantID, serviceID uuid.UUID, status domain.ServiceStatus, checks []domain.CheckStatus, ev *outbox.Event) error
}

// Infra es el inventario de servicios de infraestructura (lo usa el handler).
type Infra struct {
	store   InfraStore
	devices func() (devapi.Onboarding, bool)
	sched   *InfraScheduler
	now     func() time.Time
}

// NewInfra crea el servicio. sched puede ser nil (sin planificador).
func NewInfra(store InfraStore, devices func() (devapi.Onboarding, bool), sched *InfraScheduler) *Infra {
	if devices == nil {
		devices = func() (devapi.Onboarding, bool) { return nil, false }
	}
	return &Infra{store: store, devices: devices, sched: sched, now: time.Now}
}

// Available indica si hay almacén (PostgreSQL).
func (i *Infra) Available() bool { return i != nil && i.store != nil }

// Scheduler devuelve el planificador (puede ser nil).
func (i *Infra) Scheduler() *InfraScheduler { return i.sched }

// scope devuelve el tenant y los nodos visibles con perm (nil = todos).
func scope(ctx context.Context, perm string) (pgdb.TenantID, []uuid.UUID, error) {
	p := authz.FromContext(ctx)
	tid, err := authz.TenantOf(ctx)
	if err != nil || p == nil || p.Type == authz.TypeKiosk {
		return pgdb.TenantID{}, nil, apperr.Forbidden(problem.CodeTokenScopeInvalid, "")
	}
	if !p.Has(perm) {
		return pgdb.TenantID{}, nil, apperr.Forbidden(problem.CodePermissionDenied, "")
	}
	return pgdb.TenantID(tid), p.SiteScopes(perm), nil
}

// visibleTo: con alcance por nodo solo se ven los servicios de esos nodos;
// los que no tienen nodo exigen alcance completo.
func visibleTo(s *domain.Service, sites []uuid.UUID) bool {
	if len(sites) == 0 {
		return true
	}
	return s.SiteID != nil && slices.Contains(sites, *s.SiteID)
}

func unavailable() error {
	return apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "Inventario de servicios no disponible (rol snmp sin PostgreSQL).")
}

// List es GET /infra-services (site = filtro opcional).
func (i *Infra) List(ctx context.Context, site *uuid.UUID) ([]domain.Service, error) {
	if !i.Available() {
		return nil, unavailable()
	}
	t, sites, err := scope(ctx, PermSNMPRead)
	if err != nil {
		return nil, err
	}
	all, err := i.store.List(ctx, t)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Service, 0, len(all))
	for k := range all {
		s := &all[k]
		if !visibleTo(s, sites) || (site != nil && (s.SiteID == nil || *s.SiteID != *site)) {
			continue
		}
		out = append(out, *s)
	}
	return out, nil
}

func (i *Infra) get(ctx context.Context, perm string, id uuid.UUID) (pgdb.TenantID, []uuid.UUID, *domain.Service, error) {
	if !i.Available() {
		return pgdb.TenantID{}, nil, nil, unavailable()
	}
	t, sites, err := scope(ctx, perm)
	if err != nil {
		return t, nil, nil, err
	}
	s, err := i.store.Get(ctx, t, id)
	if errors.Is(err, ErrInfraNotFound) || (err == nil && !visibleTo(s, sites)) {
		return t, sites, nil, apperr.NotFound(problem.CodeNotFound)
	}
	return t, sites, s, err
}

// Get es GET /infra-services/{id}.
func (i *Infra) Get(ctx context.Context, id uuid.UUID) (*domain.Service, error) {
	_, _, s, err := i.get(ctx, PermSNMPRead, id)
	return s, err
}

func validation(errs []domain.FieldError) error {
	fe := make([]problem.FieldError, 0, len(errs))
	for _, e := range errs {
		fe = append(fe, apperr.Field(e.Field, e.Code, e.Msg))
	}
	return apperr.Validation(fe...)
}

// checkRouter comprueba que el router de acceso es del tenant y del alcance.
func (i *Infra) checkRouter(ctx context.Context, t pgdb.TenantID, sites []uuid.UUID, id *uuid.UUID) error {
	if id == nil {
		return nil
	}
	dev, ok := i.devices()
	if !ok {
		return nil // devices no es local: se resuelve al sondear
	}
	r, err := dev.GetRouter(ctx, t.UUID(), *id)
	if errors.Is(err, devapi.ErrRouterNotFound) || (err == nil && len(sites) > 0 && !slices.Contains(sites, r.SiteID)) {
		return validation([]domain.FieldError{{Field: "probe_via_router_id", Code: "not_found", Msg: "Router inexistente en este ISP."}})
	}
	return err
}

// Create es POST /infra-services.
func (i *Infra) Create(ctx context.Context, s *domain.Service) (*domain.Service, error) {
	if !i.Available() {
		return nil, unavailable()
	}
	t, sites, err := scope(ctx, PermSNMPManage)
	if err != nil {
		return nil, err
	}
	if errs := s.Validate(); len(errs) > 0 {
		return nil, validation(errs)
	}
	if !visibleTo(s, sites) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "Fuera de su alcance por nodo.")
	}
	if err := i.checkRouter(ctx, t, sites, s.ProbeViaRouterID); err != nil {
		return nil, err
	}
	n, err := i.store.Count(ctx, t)
	if err != nil {
		return nil, err
	}
	if n >= domain.MaxServicesPerTenant {
		return nil, apperr.Conflict(problem.CodeConflict, "Se alcanzó el máximo de 500 servicios de infraestructura por ISP.")
	}
	now := i.now().UTC()
	s.ID, s.TenantID, s.CreatedAt, s.UpdatedAt, s.Version = uuid.Must(uuid.NewV7()), t.UUID(), now, now, 1
	s.Status = domain.ServiceStatus{State: domain.StateUnknown}
	s.CheckStatus = nil
	for _, c := range s.Checks {
		s.CheckStatus = append(s.CheckStatus, domain.NewCheckStatus(c))
	}
	if err := i.store.Insert(ctx, t, s); err != nil {
		return nil, conflictOf(err)
	}
	i.sched.Refresh()
	return s, nil
}

func conflictOf(err error) error {
	if errors.Is(err, ErrInfraDuplicate) {
		return apperr.Conflict(problem.CodeConflict, "Ya existe un servicio con ese nombre.")
	}
	return err
}

// Patch son los campos editables (merge patch; nil = sin cambio).
type Patch struct {
	Name               *string
	SiteID             **uuid.UUID
	Target             *netip.Addr
	Kind               *string
	Checks             *[]domain.Check
	IntervalSeconds    *int
	FailuresBeforeDown *int
	RecoveriesBeforeUp *int
	Enabled            *bool
	ProbeViaRouterID   **uuid.UUID
}

// Update es PATCH /infra-services/{id}.
func (i *Infra) Update(ctx context.Context, id uuid.UUID, expect int, p Patch) (*domain.Service, error) {
	t, sites, s, err := i.get(ctx, PermSNMPManage, id)
	if err != nil {
		return nil, err
	}
	if s.Version != expect {
		return nil, apperr.PreconditionFailed(s)
	}
	if p.Name != nil {
		s.Name = *p.Name
	}
	if p.SiteID != nil {
		s.SiteID = *p.SiteID
	}
	if p.Target != nil {
		s.Target = *p.Target
	}
	if p.Kind != nil {
		s.Kind = *p.Kind
	}
	if p.Checks != nil {
		s.Checks = *p.Checks
	}
	if p.IntervalSeconds != nil {
		s.IntervalSeconds = *p.IntervalSeconds
	}
	if p.FailuresBeforeDown != nil {
		s.FailuresBeforeDown = *p.FailuresBeforeDown
	}
	if p.RecoveriesBeforeUp != nil {
		s.RecoveriesBeforeUp = *p.RecoveriesBeforeUp
	}
	if p.Enabled != nil {
		s.Enabled = *p.Enabled
	}
	if p.ProbeViaRouterID != nil {
		s.ProbeViaRouterID = *p.ProbeViaRouterID
	}
	if errs := s.Validate(); len(errs) > 0 {
		return nil, validation(errs)
	}
	if !visibleTo(s, sites) {
		return nil, apperr.Forbidden(problem.CodePermissionDenied, "Fuera de su alcance por nodo.")
	}
	if err := i.checkRouter(ctx, t, sites, s.ProbeViaRouterID); err != nil {
		return nil, err
	}
	// Estado: se conservan los chequeos que mantienen id; los nuevos empiezan unknown.
	var st []domain.CheckStatus
	for _, c := range s.Checks {
		st = append(st, s.StatusOf(c))
	}
	s.CheckStatus = st
	s.UpdatedAt = i.now().UTC()
	if err := i.store.Update(ctx, t, s, expect); err != nil {
		if errors.Is(err, ErrInfraVersion) {
			cur, _ := i.store.Get(ctx, t, id)
			return nil, apperr.PreconditionFailed(cur)
		}
		if errors.Is(err, ErrInfraNotFound) {
			return nil, apperr.NotFound(problem.CodeNotFound)
		}
		return nil, conflictOf(err)
	}
	s.Version = expect + 1
	i.sched.Refresh()
	return s, nil
}

// Delete es DELETE /infra-services/{id}.
func (i *Infra) Delete(ctx context.Context, id uuid.UUID, expect int) (*domain.Service, error) {
	t, _, s, err := i.get(ctx, PermSNMPManage, id)
	if err != nil {
		return nil, err
	}
	if s.Version != expect {
		return nil, apperr.PreconditionFailed(s)
	}
	if err := i.store.Delete(ctx, t, id, expect); err != nil {
		if errors.Is(err, ErrInfraVersion) {
			cur, _ := i.store.Get(ctx, t, id)
			return nil, apperr.PreconditionFailed(cur)
		}
		if errors.Is(err, ErrInfraNotFound) {
			return nil, apperr.NotFound(problem.CodeNotFound)
		}
		return nil, err
	}
	i.sched.Refresh()
	return s, nil
}

// SnippetResult es la respuesta de GET /infra-services/{id}/router-snippet.
type SnippetResult struct {
	RouterID   uuid.UUID
	RouterName string
	domain.Snippet
}

// RouterSnippet genera el script RouterOS para un servidor privado.
func (i *Infra) RouterSnippet(ctx context.Context, id uuid.UUID, routerID *uuid.UUID, servicesCIDR netip.Prefix) (*SnippetResult, error) {
	t, sites, s, err := i.get(ctx, PermSNMPManage, id)
	if err != nil {
		return nil, err
	}
	rid := routerID
	if rid == nil {
		rid = s.ProbeViaRouterID
	}
	if rid == nil {
		return nil, validation([]domain.FieldError{{Field: "router_id", Code: "required",
			Msg: "Indique el router del nodo (o fije «sondear por el router» en el servicio)."}})
	}
	dev, ok := i.devices()
	if !ok {
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "El inventario de routers no está en este proceso.")
	}
	r, err := dev.GetRouter(ctx, t.UUID(), *rid)
	if errors.Is(err, devapi.ErrRouterNotFound) || (err == nil && len(sites) > 0 && !slices.Contains(sites, r.SiteID)) {
		return nil, apperr.NotFound(problem.CodeNotFound)
	}
	if err != nil {
		return nil, err
	}
	var tunnel netip.Addr
	if r.TunnelAddress != nil {
		tunnel = *r.TunnelAddress
	}
	return &SnippetResult{RouterID: r.ID, RouterName: r.Name, Snippet: domain.BuildSnippet(s, r.Name, tunnel, servicesCIDR)}, nil
}

// stateChangedEvent construye horus.snmp.service.state_changed (payload fijo
// del contrato; observed_at y el sobre en UTC con milisegundos).
func stateChangedEvent(s *domain.Service, checkType, previous, state, reason string, at time.Time) *outbox.Event {
	tid := s.TenantID
	var site any
	if s.SiteID != nil {
		site = s.SiteID.String()
	}
	return &outbox.Event{Type: TypeServiceStateChanged, Source: "horus/snmp", TenantID: &tid, AggregateType: "infra_service",
		AggregateID: s.ID, AggregateVersion: s.Version, Actor: outbox.Actor{Type: "service", ID: "svc:snmp"}, OccurredAt: at,
		Data: map[string]any{
			"tenant_id": s.TenantID.String(), "service_id": s.ID.String(), "site_id": site, "name": s.Name,
			"check_type": checkType, "target": s.Target.String(), "previous_state": previous, "state": state,
			"reason": reason, "observed_at": at.UTC().Format(outbox.TimeFormat),
		}}
}
