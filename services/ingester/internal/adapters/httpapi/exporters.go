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
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
)

// PermFlowsRead es el permiso de las rutas (x-permission: flows.read).
const PermFlowsRead = "flows.read"

// PermPlatformTenantsRead es x-permission de GET /platform/exporters/unregistered.
const PermPlatformTenantsRead = "platform.tenants.read"

// Unregistered lista las IPs que envían flujos sin estar registradas (collector/api).
type Unregistered = collectorapi.UnregisteredLister

// States es el lector del estado (collector/api).
type States = collectorapi.States

// KVStates adapta el bucket flow_exporter_state.
type KVStates = collectorapi.KVStates

// FlowExporter es la respuesta.
type FlowExporter = collectorapi.View

// Handler sirve /flow-exporters y /platform/exporters/unregistered.
type Handler struct {
	guard  *authz.Guard
	inv    *flowinv.Store
	states States
	unreg  Unregistered
	cursor *pagination.Codec
	now    func() time.Time
}

// New crea el handler. unreg nil = listado de no registrados vacío; cursor
// nil = clave aleatoria por proceso.
func New(guard *authz.Guard, inv *flowinv.Store, states States, unreg Unregistered, cursor *pagination.Codec) *Handler {
	if cursor == nil {
		cursor = pagination.NewCodec(nil)
	}
	return &Handler{guard: guard, inv: inv, states: states, unreg: unreg, cursor: cursor, now: time.Now}
}

// Mount registra las rutas.
func (h *Handler) Mount(r *httpx.ServiceMux) {
	// Como /customers/stats y /security/summary: el listado admite el JWT de
	// kiosco, que no lleva permisos (lo limita la lista blanca de
	// permissions.yaml §kiosk). Por eso el Requirement no exige permiso y
	// visible() pide flows.read solo a los tokens de usuario.
	read := authz.Requirement{Scope: authz.ScopeTenant, AllowKiosk: true}
	r.Handle("GET /api/v1/flow-exporters", h.guard.Wrap(read, http.HandlerFunc(h.list)))
	r.Handle("GET /api/v1/flow-exporters/{router_id}", h.guard.Tenant(PermFlowsRead, h.get))
	r.Handle("GET /api/v1/platform/exporters/unregistered", h.guard.Platform(PermPlatformTenantsRead, h.listUnregistered))
}

func (h *Handler) visible(r *http.Request) (uuid.UUID, []uuid.UUID, bool) {
	p := authz.FromContext(r.Context())
	if p == nil || p.TenantID == uuid.Nil {
		return uuid.Nil, nil, false
	}
	if p.Type == authz.TypeKiosk {
		// Kiosco de solo lectura: ve los exportadores de todo su ISP.
		return p.TenantID, nil, true
	}
	if !p.Has(PermFlowsRead) {
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
