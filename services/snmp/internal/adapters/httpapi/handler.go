// Package httpapi sirve la API REST del rol snmp
// (packages/schemas/openapi/v0/snmp.yaml): estado de los routers, última
// muestra, interfaces y sondeo inmediato.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/clientip"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/app"
)

// Permisos del contrato.
const (
	PermDevicesRead   = "devices.read"
	PermDevicesUpdate = "devices.update"
	PermSNMPRead      = "snmp.read"
)

// Poller es lo que el handler necesita del sondeador.
type Poller interface {
	api.RouterStatusReader
	api.InterfaceLister
	SiteOf(tenant, routerID uuid.UUID) (uuid.UUID, bool)
	PollNow(ctx context.Context, tenant, routerID uuid.UUID) (found, queued bool, reason *string, err error)
}

// Handler sirve las rutas.
type Handler struct {
	svc   Poller
	guard *authz.Guard
	audit func() (authapi.AuditRecorder, bool)
	log   *slog.Logger
	now   func() time.Time
}

// New crea el handler. audit nil = sin auditoría.
func New(svc Poller, guard *authz.Guard, audit func() (authapi.AuditRecorder, bool), log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if audit == nil {
		audit = func() (authapi.AuditRecorder, bool) { return nil, false }
	}
	return &Handler{svc: svc, guard: guard, audit: audit, log: log, now: time.Now}
}

// Mount registra las rutas (gateway-routes.yaml sección snmp).
func (h *Handler) Mount(r *httpx.ServiceMux) {
	// El JWT de kiosco no lleva permisos: el Requirement solo exige tenant y
	// visible() pide el permiso a los tokens de usuario.
	read := authz.Requirement{Scope: authz.ScopeTenant, AllowKiosk: true}
	r.Handle("GET /api/v1/routers/status", h.guard.Wrap(read, http.HandlerFunc(h.list)))
	r.Handle("GET /api/v1/routers/{router_id}/metrics/live", h.guard.Wrap(read, http.HandlerFunc(h.live)))
	r.Handle("GET /api/v1/routers/{router_id}/interfaces", h.guard.Wrap(read, http.HandlerFunc(h.interfaces)))
	r.Handle("POST /api/v1/routers/{router_id}/poll", h.guard.Tenant(PermDevicesUpdate, h.poll))
}

// visible devuelve el tenant y los nodos visibles con perm (nil = todos).
func visible(r *http.Request, perm string) (uuid.UUID, []uuid.UUID, bool) {
	p := authz.FromContext(r.Context())
	if p == nil || p.TenantID == uuid.Nil {
		return uuid.Nil, nil, false
	}
	if p.Type == authz.TypeKiosk {
		return p.TenantID, nil, true
	}
	if !p.Has(perm) {
		return uuid.Nil, nil, false
	}
	return p.TenantID, p.SiteScopes(perm), true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenant, sites, ok := visible(r, PermDevicesRead)
	if !ok {
		problem.Std(w, r, http.StatusForbidden, problem.CodePermissionDenied)
		return
	}
	if s := r.URL.Query().Get("site_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("site_id"))
			return
		}
		if len(sites) > 0 && !slices.Contains(sites, id) {
			h.writeList(w, nil, api.PollerHealth{IntervalSeconds: api.DefaultIntervalSeconds, Enabled: true})
			return
		}
		sites = []uuid.UUID{id}
	}
	rows, poller, err := h.svc.List(r.Context(), tenant, sites)
	if err != nil {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	rows = app.FilterStatus(rows, r.URL.Query().Get("status"))
	h.writeList(w, rows, poller)
}

func (h *Handler) writeList(w http.ResponseWriter, rows []api.RouterStatus, poller api.PollerHealth) {
	if rows == nil {
		rows = []api.RouterStatus{}
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": rows,
		"meta": map[string]any{"poller": poller, "generated_at": h.now().UTC().Format(time.RFC3339)}})
}

// router resuelve el router de la ruta dentro del tenant y del alcance por
// nodo; 404 sin distinguir "de otro ISP" de "no existe".
func (h *Handler) router(w http.ResponseWriter, r *http.Request, perm string) (uuid.UUID, uuid.UUID, bool) {
	tenant, sites, ok := visible(r, perm)
	id, err := uuid.Parse(r.PathValue("router_id"))
	if !ok || err != nil {
		if !ok && err == nil {
			problem.Std(w, r, http.StatusForbidden, problem.CodePermissionDenied)
			return uuid.Nil, uuid.Nil, false
		}
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	site, found := h.svc.SiteOf(tenant, id)
	if !found || (len(sites) > 0 && !slices.Contains(sites, site)) {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	return tenant, id, true
}

func (h *Handler) live(w http.ResponseWriter, r *http.Request) {
	tenant, id, ok := h.router(w, r, PermSNMPRead)
	if !ok {
		return
	}
	out, found, err := h.svc.Live(r.Context(), tenant, id)
	if err != nil {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	if !found {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	jsonapi.Write(w, http.StatusOK, out)
}

func (h *Handler) interfaces(w http.ResponseWriter, r *http.Request) {
	tenant, id, ok := h.router(w, r, PermDevicesRead)
	if !ok {
		return
	}
	out, found, err := h.svc.Interfaces(r.Context(), tenant, id)
	if err != nil {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	if !found {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	jsonapi.Write(w, http.StatusOK, out)
}

func (h *Handler) poll(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" { // también lo exige el gateway
		problem.Std(w, r, http.StatusPreconditionRequired, problem.CodePreconditionRequired,
			problem.WithDetail("Idempotency-Key es obligatoria."))
		return
	}
	tenant, id, ok := h.router(w, r, PermDevicesUpdate)
	if !ok {
		return
	}
	found, queued, reason, err := h.svc.PollNow(r.Context(), tenant, id)
	outcome := "success"
	switch {
	case errors.Is(err, app.ErrRateLimited):
		w.Header().Set("Retry-After", "10")
		problem.Std(w, r, http.StatusTooManyRequests, problem.CodeRateLimited,
			problem.WithDetail("Ya se pidió un sondeo de este router hace menos de 10 s."))
		outcome = "denied"
	case err != nil:
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		outcome = "failure"
	case !found:
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	default:
		jsonapi.Write(w, http.StatusAccepted, map[string]any{"queued": queued, "reason": reason})
	}
	h.record(r, tenant, id, outcome, queued, reason)
}

func (h *Handler) record(r *http.Request, tenant, id uuid.UUID, outcome string, queued bool, reason *string) {
	rec, ok := h.audit()
	if !ok {
		return
	}
	p := authz.FromContext(r.Context())
	changes := map[string]any{"queued": queued}
	if reason != nil {
		changes["reason"] = *reason
	}
	e := authapi.AuditEntry{TenantID: tenant, OccurredAt: h.now(), ActorType: p.Type, ActorID: p.ActorID(), ViaPlatform: p.ViaPlatform,
		Action: "snmp.router.poll_requested", ResourceType: "router", ResourceID: id.String(), Outcome: outcome,
		IP: clientip.String(r), UserAgent: r.UserAgent(), Changes: changes, RequestID: observability.RequestIDFrom(r.Context())}
	if err := rec.Record(r.Context(), e); err != nil {
		h.log.WarnContext(r.Context(), "snmp: audit not recorded", "error", err)
	}
}
