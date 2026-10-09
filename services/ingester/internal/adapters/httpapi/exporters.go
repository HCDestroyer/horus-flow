// Package httpapi sirve la API REST del rol ingester: estado de los
// exportadores de flujos (I1-09, packages/schemas/openapi/v0/flows.yaml).
package httpapi

import (
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
)

// PermFlowsRead es el permiso de las rutas (x-permission: flows.read).
const PermFlowsRead = "flows.read"

// States es el lector del estado (collector/api).
type States = collectorapi.States

// KVStates adapta el bucket flow_exporter_state.
type KVStates = collectorapi.KVStates

// FlowExporter es la respuesta.
type FlowExporter = collectorapi.View

// Handler sirve /flow-exporters.
type Handler struct {
	guard  *authz.Guard
	inv    *flowinv.Store
	states States
	now    func() time.Time
}

// New crea el handler.
func New(guard *authz.Guard, inv *flowinv.Store, states States) *Handler {
	return &Handler{guard: guard, inv: inv, states: states, now: time.Now}
}

// Mount registra las rutas.
func (h *Handler) Mount(r *httpx.ServiceMux) {
	read := authz.Requirement{Scope: authz.ScopeTenant, Permission: PermFlowsRead, AllowKiosk: true}
	r.Handle("GET /api/v1/flow-exporters", h.guard.Wrap(read, http.HandlerFunc(h.list)))
	r.Handle("GET /api/v1/flow-exporters/{router_id}", h.guard.Tenant(PermFlowsRead, h.get))
}

func (h *Handler) visible(r *http.Request) (uuid.UUID, []uuid.UUID, bool) {
	p := authz.FromContext(r.Context())
	if p == nil || p.TenantID == uuid.Nil {
		return uuid.Nil, nil, false
	}
	return p.TenantID, p.SiteScopes(PermFlowsRead), true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenant, sites, ok := h.visible(r)
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
			jsonapi.Write(w, http.StatusOK, map[string]any{"data": []FlowExporter{}})
			return
		}
		sites = []uuid.UUID{id}
	}
	out, err := collectorapi.Exporters(r.Context(), h.inv.Load(), h.states, tenant, sites, h.now())
	if err != nil {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	if st := r.URL.Query().Get("state"); st != "" {
		out = slices.DeleteFunc(out, func(e FlowExporter) bool { return e.State != st })
	}
	if out == nil {
		out = []FlowExporter{}
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": out})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	tenant, sites, ok := h.visible(r)
	id, err := uuid.Parse(r.PathValue("router_id"))
	if !ok || err != nil {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return
	}
	out, err := collectorapi.Exporters(r.Context(), h.inv.Load(), h.states, tenant, sites, h.now())
	if err != nil {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return
	}
	for _, e := range out {
		if e.RouterID == id {
			w.Header().Set("ETag", jsonapi.ETag(e.Version))
			jsonapi.Write(w, http.StatusOK, e)
			return
		}
	}
	// Router de otro ISP o inexistente: 404 sin distinguir.
	problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
}
