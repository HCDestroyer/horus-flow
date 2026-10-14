package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/clientip"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/app"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// InfraHandler sirve /infra-services (snmp.yaml, tag infra-services).
type InfraHandler struct {
	svc          *app.Infra
	guard        *authz.Guard
	audit        func() (authapi.AuditRecorder, bool)
	baseURL      string
	servicesCIDR netip.Prefix
	log          *slog.Logger
	now          func() time.Time
}

// NewInfra crea el handler. servicesCIDR es la red de servicios de Horus
// (HORUS_WG_SERVICES_CIDR) para el script de router.
func NewInfra(svc *app.Infra, guard *authz.Guard, audit func() (authapi.AuditRecorder, bool), baseURL string,
	servicesCIDR netip.Prefix, log *slog.Logger) *InfraHandler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	return &InfraHandler{svc: svc, guard: guard, audit: audit, baseURL: baseURL, servicesCIDR: servicesCIDR, log: log, now: time.Now}
}

// Mount registra las rutas.
func (h *InfraHandler) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/infra-services", h.guard.Tenant(app.PermSNMPRead, h.list))
	r.Handle("POST /api/v1/infra-services", h.guard.Tenant(app.PermSNMPManage, h.create))
	r.Handle("GET /api/v1/infra-services/{service_id}", h.guard.Tenant(app.PermSNMPRead, h.get))
	r.Handle("PATCH /api/v1/infra-services/{service_id}", h.guard.Tenant(app.PermSNMPManage, h.update))
	r.Handle("DELETE /api/v1/infra-services/{service_id}", h.guard.Tenant(app.PermSNMPManage, h.delete))
	r.Handle("GET /api/v1/infra-services/{service_id}/router-snippet", h.guard.Tenant(app.PermSNMPManage, h.snippet))
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

func checkStatusJSON(c domain.CheckStatus, withRecent bool) map[string]any {
	out := map[string]any{
		"check_id": c.CheckID, "type": c.Type, "state": c.State, "reason": c.Reason, "since": tsPtr(c.Since),
		"last_checked_at": tsPtr(c.LastCheckedAt), "latency_ms": c.LatencyMs, "consecutive_failures": c.ConsecutiveFailures,
		"consecutive_successes": c.ConsecutiveSuccesses, "detail": c.Detail,
	}
	if withRecent {
		recent := make([]map[string]any, 0, len(c.Recent))
		for _, r := range c.Recent {
			recent = append(recent, map[string]any{"at": ts(r.At), "state": r.State, "latency_ms": r.LatencyMs, "reason": r.Reason})
		}
		out["recent"] = recent
	}
	return out
}

func serviceJSON(s *domain.Service, withRecent bool) map[string]any {
	checks := s.Checks
	if checks == nil {
		checks = []domain.Check{}
	}
	cs := make([]map[string]any, 0, len(s.Checks))
	for _, c := range s.Checks {
		cs = append(cs, checkStatusJSON(s.StatusOf(c), withRecent))
	}
	state := s.Status.State
	if state == "" {
		state = domain.StateUnknown
	}
	return map[string]any{
		"id": s.ID, "tenant_id": s.TenantID, "version": s.Version, "created_at": ts(s.CreatedAt), "updated_at": ts(s.UpdatedAt),
		"name": s.Name, "site_id": s.SiteID, "target": s.Target.String(), "target_private": domain.IsPrivate(s.Target), "kind": s.Kind,
		"checks": checks, "interval_seconds": s.IntervalSeconds, "failures_before_down": s.FailuresBeforeDown,
		"recoveries_before_up": s.RecoveriesBeforeUp, "enabled": s.Enabled, "probe_via_router_id": s.ProbeViaRouterID,
		"status":       map[string]any{"state": state, "reason": s.Status.Reason, "check_type": s.Status.CheckType, "since": tsPtr(s.Status.Since)},
		"check_status": cs,
	}
}

func (h *InfraHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok && e.Current != nil {
		if s, ok := e.Current.(*domain.Service); ok {
			e.Current = serviceJSON(s, false)
		}
	}
	apperr.WriteHTTP(w, r, h.log, err)
}

func writeService(w http.ResponseWriter, status int, s *domain.Service, withRecent bool) {
	w.Header().Set("ETag", jsonapi.ETag(s.Version))
	jsonapi.Write(w, status, serviceJSON(s, withRecent))
}

func serviceID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("service_id"))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func (h *InfraHandler) list(w http.ResponseWriter, r *http.Request) {
	var site *uuid.UUID
	if v := r.URL.Query().Get("site_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("site_id"))
			return
		}
		site = &id
	}
	rows, err := h.svc.List(r.Context(), site)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for i := range rows {
		data = append(data, serviceJSON(&rows[i], false))
	}
	hl := h.svc.Scheduler().Health()
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "meta": map[string]any{
		"scheduler": map[string]any{"enabled": hl.Enabled, "healthy": hl.Healthy, "last_cycle_at": tsPtr(hl.LastCycleAt),
			"passive_checks": hl.PassiveChecks},
		"generated_at": ts(h.now()),
	}})
}

// input es InfraServiceInput / InfraServicePatch (presencia por campo).
type input struct {
	Name               *string         `json:"name"`
	SiteID             json.RawMessage `json:"site_id"`
	Target             *string         `json:"target"`
	Kind               *string         `json:"kind"`
	Checks             *[]domain.Check `json:"checks"`
	IntervalSeconds    *int            `json:"interval_seconds"`
	FailuresBeforeDown *int            `json:"failures_before_down"`
	RecoveriesBeforeUp *int            `json:"recoveries_before_up"`
	Enabled            *bool           `json:"enabled"`
	ProbeViaRouterID   json.RawMessage `json:"probe_via_router_id"`
}

// nullableUUID decodifica un campo uuid|null presente (nil = ausente).
func nullableUUID(raw json.RawMessage, field string) (**uuid.UUID, *domain.FieldError) {
	if raw == nil {
		return nil, nil
	}
	var out *uuid.UUID
	if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var id uuid.UUID
		if err := json.Unmarshal(raw, &id); err != nil {
			return nil, &domain.FieldError{Field: field, Code: "invalid_format", Msg: "UUID no válido."}
		}
		out = &id
	}
	return &out, nil
}

func (in *input) patch() (app.Patch, []domain.FieldError) {
	var p app.Patch
	var errs []domain.FieldError
	p.Name, p.Kind, p.Checks, p.IntervalSeconds = in.Name, in.Kind, in.Checks, in.IntervalSeconds
	p.FailuresBeforeDown, p.RecoveriesBeforeUp, p.Enabled = in.FailuresBeforeDown, in.RecoveriesBeforeUp, in.Enabled
	if in.Target != nil {
		a, err := netip.ParseAddr(*in.Target)
		if err != nil || a.Zone() != "" {
			errs = append(errs, domain.FieldError{Field: "target", Code: "invalid_format", Msg: "IP del servidor no válida."})
		} else {
			p.Target = &a
		}
	}
	var fe *domain.FieldError
	if p.SiteID, fe = nullableUUID(in.SiteID, "site_id"); fe != nil {
		errs = append(errs, *fe)
	}
	if p.ProbeViaRouterID, fe = nullableUUID(in.ProbeViaRouterID, "probe_via_router_id"); fe != nil {
		errs = append(errs, *fe)
	}
	return p, errs
}

func validationErr(errs []domain.FieldError) error {
	fe := make([]problem.FieldError, 0, len(errs))
	for _, e := range errs {
		fe = append(fe, apperr.Field(e.Field, e.Code, e.Msg))
	}
	return apperr.Validation(fe...)
}

// decode lee el cuerpo estricto (campos desconocidos → 422).
func decode(w http.ResponseWriter, r *http.Request, in *input) bool {
	return jsonapi.Decode(w, r, in, true)
}

func (h *InfraHandler) create(w http.ResponseWriter, r *http.Request) {
	var in input
	if !decode(w, r, &in) {
		return
	}
	p, errs := in.patch()
	var missing []domain.FieldError
	if in.Name == nil {
		missing = append(missing, domain.FieldError{Field: "name", Code: "required", Msg: "Obligatorio."})
	}
	if in.Target == nil {
		missing = append(missing, domain.FieldError{Field: "target", Code: "required", Msg: "Obligatorio."})
	}
	if in.Kind == nil {
		missing = append(missing, domain.FieldError{Field: "kind", Code: "required", Msg: "Obligatorio."})
	}
	if in.Checks == nil {
		missing = append(missing, domain.FieldError{Field: "checks", Code: "required", Msg: "Obligatorio."})
	}
	if errs = append(errs, missing...); len(errs) > 0 {
		h.fail(w, r, validationErr(errs))
		return
	}
	s := &domain.Service{Name: *p.Name, Target: *p.Target, Kind: *p.Kind, Checks: *p.Checks, Enabled: true}
	if p.SiteID != nil {
		s.SiteID = *p.SiteID
	}
	if p.ProbeViaRouterID != nil {
		s.ProbeViaRouterID = *p.ProbeViaRouterID
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
	out, err := h.svc.Create(r.Context(), s)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.record(r, out, "snmp.infra_service.created", map[string]any{"kind": out.Kind, "checks": len(out.Checks)})
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/infra-services/"+out.ID.String())
	writeService(w, http.StatusCreated, out, false)
}

func (h *InfraHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := serviceID(w, r)
	if !ok {
		return
	}
	s, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeService(w, http.StatusOK, s, true)
}

func (h *InfraHandler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := serviceID(w, r)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in input
	if !decode(w, r, &in) {
		return
	}
	p, errs := in.patch()
	if len(errs) > 0 {
		h.fail(w, r, validationErr(errs))
		return
	}
	s, err := h.svc.Update(r.Context(), id, ver, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.record(r, s, "snmp.infra_service.updated", map[string]any{"version": s.Version})
	writeService(w, http.StatusOK, s, false)
}

func (h *InfraHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := serviceID(w, r)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, err := h.svc.Delete(r.Context(), id, ver)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.record(r, s, "snmp.infra_service.deleted", map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}

func (h *InfraHandler) snippet(w http.ResponseWriter, r *http.Request) {
	id, ok := serviceID(w, r)
	if !ok {
		return
	}
	var rid *uuid.UUID
	if v := r.URL.Query().Get("router_id"); v != "" {
		u, err := uuid.Parse(v)
		if err != nil {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("router_id"))
			return
		}
		rid = &u
	}
	out, err := h.svc.RouterSnippet(r.Context(), id, rid, h.servicesCIDR)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	warnings := out.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"router_id": out.RouterID, "router_name": out.RouterName, "applicable": out.Applicable,
		"script": out.Script, "horus_side": out.HorusSide, "warnings": warnings})
}

// record audita las altas, ediciones y bajas (sin IP del servidor en los cambios).
func (h *InfraHandler) record(r *http.Request, s *domain.Service, action string, changes map[string]any) {
	rec, ok := h.audit()
	if !ok {
		return
	}
	p := authz.FromContext(r.Context())
	if p == nil {
		return
	}
	e := authapi.AuditEntry{TenantID: s.TenantID, OccurredAt: h.now(), ActorType: p.Type, ActorID: p.ActorID(), ViaPlatform: p.ViaPlatform,
		Action: action, ResourceType: "infra_service", ResourceID: s.ID.String(), Outcome: "success",
		IP: clientip.String(r), UserAgent: r.UserAgent(), Changes: changes, RequestID: observability.RequestIDFrom(r.Context())}
	if err := rec.Record(r.Context(), e); err != nil {
		h.log.WarnContext(r.Context(), "snmp: audit not recorded", "error", err)
	}
}
