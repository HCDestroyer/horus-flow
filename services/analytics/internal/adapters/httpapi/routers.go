package httpapi

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/app"
)

// PermSNMPRead es el permiso de las series de routers (analytics.yaml, EP-08).
const PermSNMPRead = "snmp.read"

// mountRouters registra las series históricas de los routers (EP-08). Las
// rutas admiten el JWT de kiosco, que no lleva permisos (lista blanca de
// permissions.yaml §kiosk): el Requirement no exige permiso y routerScope
// pide snmp.read solo a los tokens de usuario.
func (h *Handler) mountRouters(r *httpx.ServiceMux) {
	req := authz.Requirement{Scope: authz.ScopeTenant, AllowKiosk: true}
	r.Handle("GET /api/v1/analytics/routers/{router_id}/metrics", h.guard.Wrap(req, http.HandlerFunc(h.routerMetrics)))
	r.Handle("GET /api/v1/analytics/routers/{router_id}/interfaces/metrics", h.guard.Wrap(req, http.HandlerFunc(h.routerInterfaceMetrics)))
}

// routerScope resuelve tenant y router: 403 sin snmp.read (usuarios), 404
// si el router no es del tenant o su nodo queda fuera del alcance del permiso.
func (h *Handler) routerScope(w http.ResponseWriter, r *http.Request) (tenant, router uuid.UUID, ok bool) {
	p := authz.FromContext(r.Context())
	if p == nil || p.TenantID == uuid.Nil {
		problem.Std(w, r, http.StatusForbidden, problem.CodePermissionDenied)
		return uuid.Nil, uuid.Nil, false
	}
	if p.Type != authz.TypeKiosk && !p.Has(PermSNMPRead) {
		problem.Std(w, r, http.StatusForbidden, problem.CodePermissionDenied)
		return uuid.Nil, uuid.Nil, false
	}
	id, err := uuid.Parse(r.PathValue("router_id"))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	site, err := h.svc.Router(r.Context(), p.TenantID, id)
	if err != nil {
		h.fail(w, r, err)
		return uuid.Nil, uuid.Nil, false
	}
	if p.Type != authz.TypeKiosk {
		if scopes := p.SiteScopes(PermSNMPRead); !p.TenantWide(PermSNMPRead) && !slices.Contains(scopes, site) {
			problem.Std(w, r, http.StatusNotFound, problem.CodeNotFound)
			return uuid.Nil, uuid.Nil, false
		}
	}
	return p.TenantID, id, true
}

// seriesParams lee range/from/to, step y metrics; false tras escribir el error.
func (h *Handler) seriesParams(w http.ResponseWriter, r *http.Request) (app.Range, time.Duration, []string, bool) {
	q := r.URL.Query()
	rg, err := app.ParseRange(q.Get("range"), q.Get("from"), q.Get("to"), "24h", time.Now())
	if err != nil {
		h.fail(w, r, err)
		return app.Range{}, 0, nil, false
	}
	step, ok := parseStep(q.Get("step"))
	if !ok {
		problem.Std(w, r, http.StatusUnprocessableEntity, problem.CodeValidationFailed, problem.WithDetail("step"))
		return app.Range{}, 0, nil, false
	}
	var metrics []string
	if m := q.Get("metrics"); m != "" {
		metrics = strings.Split(m, ",")
	}
	return rg, step, metrics, true
}

// routerMetrics sirve GET /analytics/routers/{router_id}/metrics.
func (h *Handler) routerMetrics(w http.ResponseWriter, r *http.Request) {
	tenant, id, ok := h.routerScope(w, r)
	if !ok {
		return
	}
	rg, step, metrics, ok := h.seriesParams(w, r)
	if !ok {
		return
	}
	res, err := h.svc.RouterMetrics(r.Context(), tenant, id, metrics, rg, step)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, seriesJSON(res, "snmp"))
}

// routerInterfaceMetrics sirve GET /analytics/routers/{router_id}/interfaces/metrics.
func (h *Handler) routerInterfaceMetrics(w http.ResponseWriter, r *http.Request) {
	tenant, id, ok := h.routerScope(w, r)
	if !ok {
		return
	}
	ifaces, err := app.ParseInterfaceIDs(r.URL.Query().Get("interface_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rg, step, metrics, ok := h.seriesParams(w, r)
	if !ok {
		return
	}
	res, err := h.svc.RouterInterfaceMetrics(r.Context(), tenant, id, ifaces, metrics, rg, step)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, seriesJSON(res, "snmp"))
}
