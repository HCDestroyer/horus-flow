package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/google/uuid"

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

// Códigos de error del análisis de capacidad.
const (
	CodeUplinkNotFound = "UPLINK_NOT_FOUND"
	CodeTooManyUplinks = "UPLINK_LIMIT_REACHED"
)

// CapacityHandler sirve /capacity/* y /routers/{id}/netwatch-snippet
// (snmp.yaml, tag capacity).
type CapacityHandler struct {
	svc   *app.Capacity
	guard *authz.Guard
	audit func() (authapi.AuditRecorder, bool)
	deny  []netip.Prefix
	log   *slog.Logger
	now   func() time.Time
}

// NewCapacity crea el handler. deny son las redes internas de la
// instalación que no pueden ser destino de las sondas de Horus.
func NewCapacity(svc *app.Capacity, guard *authz.Guard, audit func() (authapi.AuditRecorder, bool), deny []netip.Prefix, log *slog.Logger) *CapacityHandler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	return &CapacityHandler{svc: svc, guard: guard, audit: audit, deny: deny, log: log, now: time.Now}
}

// Mount registra las rutas.
func (h *CapacityHandler) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/capacity/overview", h.guard.Tenant(app.PermSNMPRead, h.overview))
	r.Handle("GET /api/v1/capacity/egress", h.guard.Tenant(app.PermSNMPRead, h.egress))
	r.Handle("GET /api/v1/capacity/uplinks/{interface_id}", h.guard.Tenant(app.PermSNMPRead, h.uplink))
	r.Handle("PUT /api/v1/capacity/uplinks/{interface_id}", h.guard.Tenant(app.PermSNMPManage, h.putUplink))
	r.Handle("DELETE /api/v1/capacity/uplinks/{interface_id}", h.guard.Tenant(app.PermSNMPManage, h.deleteUplink))
	r.Handle("GET /api/v1/capacity/settings", h.guard.Tenant(app.PermSNMPRead, h.getSettings))
	r.Handle("PUT /api/v1/capacity/settings", h.guard.Tenant(app.PermSNMPManage, h.putSettings))
	r.Handle("DELETE /api/v1/capacity/settings", h.guard.Tenant(app.PermSNMPManage, h.resetSettings))
	r.Handle("GET /api/v1/capacity/traffic-split", h.guard.Tenant(app.PermSNMPRead, h.trafficSplit))
	r.Handle("GET /api/v1/routers/{router_id}/netwatch-snippet", h.guard.Tenant(app.PermSNMPManage, h.snippet))
}

// scopeOf devuelve tenant y nodos visibles con perm (nil = todos).
func scopeOf(r *http.Request, perm string) (uuid.UUID, []uuid.UUID) {
	p := authz.FromContext(r.Context())
	return p.TenantID, p.SiteScopes(perm)
}

func intParam(w http.ResponseWriter, r *http.Request, name string, def, lo, hi int) (int, bool) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, true
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < lo || v > hi {
		problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed,
			problem.WithErrors(problem.FieldError{Field: name, Code: "OUT_OF_RANGE", Message: "Entre " + strconv.Itoa(lo) + " y " + strconv.Itoa(hi) + "."}))
		return 0, false
	}
	return v, true
}

func siteParam(w http.ResponseWriter, r *http.Request) (*uuid.UUID, bool) {
	s := r.URL.Query().Get("site_id")
	if s == "" {
		return nil, true
	}
	id, err := uuid.Parse(s)
	if err != nil {
		problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("site_id"))
		return nil, false
	}
	return &id, true
}

func (h *CapacityHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, app.ErrUplinkNotFound):
		problem.Write(w, r, http.StatusNotFound, CodeUplinkNotFound, "Enlace no encontrado")
	case errors.Is(err, app.ErrCapacityNoStore):
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable,
			problem.WithDetail("Configuración de capacidad no disponible: el rol snmp no tiene PostgreSQL (HORUS_POSTGRES_DSN)."))
	case errors.Is(err, app.ErrCapacityVersion):
		problem.Std(w, r, http.StatusPreconditionFailed, problem.CodePreconditionFailed)
	case errors.Is(err, app.ErrTooManyUplinks):
		problem.Write(w, r, http.StatusConflict, CodeTooManyUplinks, "Límite de enlaces configurados",
			problem.WithDetail("Máximo de "+strconv.Itoa(domain.MaxUplinksPerTenant)+" enlaces configurados por ISP."))
	default:
		h.log.ErrorContext(r.Context(), "snmp: capacity", "error", err)
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
	}
}

func (h *CapacityHandler) overview(w http.ResponseWriter, r *http.Request) {
	tenant, sites := scopeOf(r, app.PermSNMPRead)
	site, ok := siteParam(w, r)
	if !ok {
		return
	}
	days, ok := intParam(w, r, "days", 7, 1, 30)
	if !ok {
		return
	}
	ov, err := h.svc.Overview(r.Context(), tenant, days)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, app.FilterSites(ov, sites, site))
}

func (h *CapacityHandler) egress(w http.ResponseWriter, r *http.Request) {
	tenant, sites := scopeOf(r, app.PermSNMPRead)
	days, ok := intParam(w, r, "days", app.DefaultDetailDays, app.MinDetailDays, app.MaxDetailDays)
	if !ok {
		return
	}
	d, err := h.svc.EgressDetail(r.Context(), tenant, sites, days)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, d)
}

func ifaceParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("interface_id"))
	if err != nil {
		problem.Write(w, r, http.StatusNotFound, CodeUplinkNotFound, "Enlace no encontrado")
		return uuid.Nil, false
	}
	return id, true
}

func (h *CapacityHandler) uplink(w http.ResponseWriter, r *http.Request) {
	iface, ok := ifaceParam(w, r)
	if !ok {
		return
	}
	days, ok := intParam(w, r, "days", app.DefaultDetailDays, app.MinDetailDays, app.MaxDetailDays)
	if !ok {
		return
	}
	tenant, sites := scopeOf(r, app.PermSNMPRead)
	d, err := h.svc.UplinkDetail(r.Context(), tenant, sites, iface, days)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, d)
}

func ifMatch(w http.ResponseWriter, r *http.Request) (int, bool) {
	if r.Header.Get("If-Match") == "" {
		return 0, true
	}
	v, err := jsonapi.IfMatch(r)
	if err != nil {
		problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("If-Match"))
		return 0, false
	}
	return v, true
}

func invalid(w http.ResponseWriter, r *http.Request, errs []domain.FieldError) bool {
	if len(errs) == 0 {
		return false
	}
	fields := make([]problem.FieldError, 0, len(errs))
	for _, e := range errs {
		fields = append(fields, problem.FieldError{Field: e.Field, Code: e.Code, Message: e.Msg})
	}
	problem.Std(w, r, http.StatusUnprocessableEntity, problem.CodeValidationFailed, problem.WithErrors(fields...))
	return true
}

func (h *CapacityHandler) putUplink(w http.ResponseWriter, r *http.Request) {
	iface, ok := ifaceParam(w, r)
	if !ok {
		return
	}
	expect, ok := ifMatch(w, r)
	if !ok {
		return
	}
	var in domain.UplinkConfigInput
	if !jsonapi.Decode(w, r, &in, true) || invalid(w, r, in.Validate()) {
		return
	}
	p := authz.FromContext(r.Context())
	tenant, sites := scopeOf(r, app.PermSNMPManage)
	after, before, err := h.svc.PutUplink(r.Context(), tenant, sites, iface, in, p.ActorID(), expect)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	changes := map[string]any{"after": after}
	if before != nil {
		changes["before"] = before
	}
	h.record(r, "snmp.uplink_config.updated", "uplink_config", iface.String(), changes)
	w.Header().Set("ETag", jsonapi.ETag(after.Version))
	jsonapi.Write(w, http.StatusOK, after)
}

func (h *CapacityHandler) deleteUplink(w http.ResponseWriter, r *http.Request) {
	iface, ok := ifaceParam(w, r)
	if !ok {
		return
	}
	tenant, sites := scopeOf(r, app.PermSNMPManage)
	before, err := h.svc.DeleteUplink(r.Context(), tenant, sites, iface)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.record(r, "snmp.uplink_config.deleted", "uplink_config", iface.String(), map[string]any{"before": before})
	w.WriteHeader(http.StatusNoContent)
}

func writeSettings(w http.ResponseWriter, v app.SettingsView) {
	w.Header().Set("ETag", jsonapi.ETag(v.Version))
	jsonapi.Write(w, http.StatusOK, v)
}

func (h *CapacityHandler) getSettings(w http.ResponseWriter, r *http.Request) {
	tenant, _ := scopeOf(r, app.PermSNMPRead)
	v, err := h.svc.GetSettings(r.Context(), tenant)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeSettings(w, v)
}

func (h *CapacityHandler) putSettings(w http.ResponseWriter, r *http.Request) {
	expect, ok := ifMatch(w, r)
	if !ok {
		return
	}
	var in domain.CapacitySettings
	if !jsonapi.Decode(w, r, &in, true) || invalid(w, r, in.Validate(h.deny)) {
		return
	}
	p := authz.FromContext(r.Context())
	before, _ := h.svc.GetSettings(r.Context(), p.TenantID)
	v, err := h.svc.PutSettings(r.Context(), p.TenantID, in, p.ActorID(), expect)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.record(r, "snmp.capacity_settings.updated", "capacity_settings", p.TenantID.String(),
		map[string]any{"before": before.CapacitySettings, "after": v.CapacitySettings})
	writeSettings(w, v)
}

func (h *CapacityHandler) resetSettings(w http.ResponseWriter, r *http.Request) {
	p := authz.FromContext(r.Context())
	v, err := h.svc.ResetSettings(r.Context(), p.TenantID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.record(r, "snmp.capacity_settings.reset", "capacity_settings", p.TenantID.String(), map[string]any{"after": v.CapacitySettings})
	writeSettings(w, v)
}

func (h *CapacityHandler) trafficSplit(w http.ResponseWriter, r *http.Request) {
	tenant, sites := scopeOf(r, app.PermSNMPRead)
	site, ok := siteParam(w, r)
	if !ok {
		return
	}
	days, ok := intParam(w, r, "days", app.DefaultTraffic, 1, app.MaxTrafficDays)
	if !ok {
		return
	}
	filter := sites
	if site != nil {
		if len(sites) > 0 && !contains(sites, *site) {
			filter = []uuid.UUID{uuid.Nil} // ningún nodo visible: resultado vacío
		} else {
			filter = []uuid.UUID{*site}
		}
	}
	v, err := h.svc.TrafficSplit(r.Context(), tenant, filter, days)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, v)
}

func contains(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func (h *CapacityHandler) snippet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("router_id"))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	tenant, sites := scopeOf(r, app.PermSNMPManage)
	v, err := h.svc.NetwatchSnippet(r.Context(), tenant, sites, id)
	if errors.Is(err, app.ErrUplinkNotFound) {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, v)
}

func (h *CapacityHandler) record(r *http.Request, action, rtype, rid string, changes map[string]any) {
	rec, ok := h.audit()
	if !ok {
		return
	}
	p := authz.FromContext(r.Context())
	e := authapi.AuditEntry{TenantID: p.TenantID, OccurredAt: h.now(), ActorType: p.Type, ActorID: p.ActorID(), ViaPlatform: p.ViaPlatform,
		Action: action, ResourceType: rtype, ResourceID: rid, Scope: "tenant", Outcome: "success", IP: clientip.String(r),
		UserAgent: r.UserAgent(), Changes: changes, RequestID: observability.RequestIDFrom(r.Context())}
	if err := rec.Record(r.Context(), e); err != nil {
		h.log.WarnContext(r.Context(), "snmp: audit not recorded", "error", err, "action", action)
	}
}
