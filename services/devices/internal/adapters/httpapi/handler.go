// Package httpapi es el adaptador REST del módulo devices: rutas I0 de
// packages/schemas/openapi/v0/devices.yaml (nodos, routers y prefijos de
// clientes).
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Handler sirve las rutas del módulo.
type Handler struct {
	svc     *app.Service
	guard   *authz.Guard
	baseURL string
	logger  *slog.Logger
}

// New crea el handler.
func New(svc *app.Service, guard *authz.Guard, baseURL string, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, guard: guard, baseURL: baseURL, logger: logger}
}

// Mount registra las rutas (permiso fino revalidado por el guard).
func (h *Handler) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/sites", h.guard.Tenant("sites.read", h.listSites))
	r.Handle("POST /api/v1/sites", h.guard.Tenant("sites.create", h.createSite))
	r.Handle("GET /api/v1/sites/{site_id}", h.guard.Tenant("sites.read", h.getSite))
	r.Handle("PATCH /api/v1/sites/{site_id}", h.guard.Tenant("sites.update", h.updateSite))
	r.Handle("DELETE /api/v1/sites/{site_id}", h.guard.Tenant("sites.delete", h.deleteSite))
	r.Handle("GET /api/v1/routers", h.guard.Tenant("devices.read", h.listRouters))
	r.Handle("POST /api/v1/routers", h.guard.Tenant("devices.create", h.createRouter))
	r.Handle("GET /api/v1/routers/{router_id}", h.guard.Tenant("devices.read", h.getRouter))
	r.Handle("PATCH /api/v1/routers/{router_id}", h.guard.Tenant("devices.update", h.updateRouter))
	r.Handle("DELETE /api/v1/routers/{router_id}", h.guard.Tenant("devices.delete", h.deleteRouter))
	r.Handle("GET /api/v1/sites/{site_id}/client-prefixes", h.guard.Tenant("sites.read", h.listPrefixes))
	r.Handle("POST /api/v1/sites/{site_id}/client-prefixes", h.guard.Tenant("sites.update", h.createPrefix))
	r.Handle("PATCH /api/v1/client-prefixes/{client_prefix_id}", h.guard.Tenant("sites.update", h.updatePrefix))
	r.Handle("DELETE /api/v1/client-prefixes/{client_prefix_id}", h.guard.Tenant("sites.update", h.deletePrefix))
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok && e.Current != nil {
		switch c := e.Current.(type) {
		case *domain.Site:
			e.Current = siteJSON(c)
		case *domain.Router:
			e.Current = routerJSON(c)
		case *domain.ClientPrefix:
			e.Current = prefixJSON(c)
		}
	}
	apperr.WriteHTTP(w, r, h.logger, err)
}

func pathID(w http.ResponseWriter, r *http.Request, name, code string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, code)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) ifMatch(w http.ResponseWriter, r *http.Request) (int, bool) {
	v, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return 0, false
	}
	return v, true
}

func writeItem(w http.ResponseWriter, status, version int, v any) {
	w.Header().Set("ETag", jsonapi.ETag(version))
	jsonapi.Write(w, status, v)
}

func listJSON[T any](rows []T, conv func(*T) map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, conv(&rows[i]))
	}
	return out
}

// ------------------------------------------------------------------ sites

func siteJSON(s *domain.Site) map[string]any {
	tags := s.Tags
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{
		"id": s.ID, "tenant_id": s.TenantID, "version": s.Version, "created_at": ts(s.CreatedAt), "updated_at": ts(s.UpdatedAt),
		"name": s.Name, "code": s.Code, "kind": s.Kind, "parent_id": s.ParentID, "address": s.Address, "latitude": s.Latitude,
		"longitude": s.Longitude, "timezone": s.Timezone, "tags": tags, "discovery_mode": s.DiscoveryMode(),
		"primary_router_id": s.PrimaryRouterID, "private_realm_id": s.PrivateRealmID,
	}
}

func (h *Handler) listSites(w http.ResponseWriter, r *http.Request) {
	page, err := h.svc.ListSites(r.Context(), r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": listJSON(page.Data, siteJSON), "page": page.Page})
}

func (h *Handler) createSite(w http.ResponseWriter, r *http.Request) {
	var f app.Fields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	s, err := h.svc.CreateSite(r.Context(), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/sites/"+s.ID.String())
	writeItem(w, http.StatusCreated, s.Version, siteJSON(s))
}

func (h *Handler) getSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "site_id", domain.CodeSiteNotFound)
	if !ok {
		return
	}
	s, err := h.svc.GetSite(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, s.Version, siteJSON(s))
}

func (h *Handler) updateSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "site_id", domain.CodeSiteNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var f app.Fields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	s, err := h.svc.UpdateSite(r.Context(), id, ver, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, s.Version, siteJSON(s))
}

func (h *Handler) deleteSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "site_id", domain.CodeSiteNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteSite(r.Context(), id, ver); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------ routers

func routerJSON(x *domain.Router) map[string]any {
	tags := x.Tags
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{
		"id": x.ID, "tenant_id": x.TenantID, "version": x.Version, "created_at": ts(x.CreatedAt), "updated_at": ts(x.UpdatedAt),
		"site_id": x.SiteID, "name": x.Hostname, "display_name": x.DisplayName, "is_primary": x.IsPrimary, "vendor": x.Vendor,
		"model": x.Model, "routeros_version": x.RouterOSVersion, "tags": tags, "admin_state": x.AdminState,
		"onboarding_state": x.OnboardingState, "routeros_version_detected": x.RouterOSVersionDetected,
		"routeros_version_supported": x.RouterOSVersionOK, "tunnel_address": x.TunnelAddress, "wireguard_peer_id": x.WireguardPeerID,
		"warnings": x.Warnings(), "credentials": []any{},
	}
}

func (h *Handler) listRouters(w http.ResponseWriter, r *http.Request) {
	page, err := h.svc.ListRouters(r.Context(), r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": listJSON(page.Data, routerJSON), "page": page.Page})
}

func (h *Handler) createRouter(w http.ResponseWriter, r *http.Request) {
	var f app.Fields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	x, err := h.svc.CreateRouter(r.Context(), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/routers/"+x.ID.String())
	writeItem(w, http.StatusCreated, x.Version, routerJSON(x))
}

func (h *Handler) getRouter(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	x, err := h.svc.GetRouter(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, x.Version, routerJSON(x))
}

func (h *Handler) updateRouter(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var f app.Fields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	x, err := h.svc.UpdateRouter(r.Context(), id, ver, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, x.Version, routerJSON(x))
}

func (h *Handler) deleteRouter(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteRouter(r.Context(), id, ver); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------ client prefixes

func prefixJSON(p *domain.ClientPrefix) map[string]any {
	return map[string]any{
		"id": p.ID, "tenant_id": p.TenantID, "version": p.Version, "created_at": ts(p.CreatedAt), "updated_at": ts(p.UpdatedAt),
		"prefix": p.Prefix.String(), "role": p.Role, "assignment_mode": p.AssignmentMode, "default_kind": p.DefaultKind,
		"ipv6_client_len": p.IPv6ClientLen, "source": p.Source, "note": p.Note, "site_id": p.SiteID, "realm_id": p.RealmID,
		"realm_kind": p.RealmKind, "confirmed": p.Confirmed, "customers_count": nil,
	}
}

func (h *Handler) listPrefixes(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "site_id", domain.CodeSiteNotFound)
	if !ok {
		return
	}
	page, err := h.svc.ListPrefixes(r.Context(), id, r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": listJSON(page.Data, prefixJSON), "page": page.Page})
}

func (h *Handler) createPrefix(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "site_id", domain.CodeSiteNotFound)
	if !ok {
		return
	}
	var f app.Fields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	p, err := h.svc.CreatePrefix(r.Context(), id, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/client-prefixes/"+p.ID.String())
	writeItem(w, http.StatusCreated, p.Version, prefixJSON(p))
}

func (h *Handler) updatePrefix(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "client_prefix_id", domain.CodeClientPrefixNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var f app.Fields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	p, err := h.svc.UpdatePrefix(r.Context(), id, ver, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeItem(w, http.StatusOK, p.Version, prefixJSON(p))
}

func (h *Handler) deletePrefix(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "client_prefix_id", domain.CodeClientPrefixNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeletePrefix(r.Context(), id, ver); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
