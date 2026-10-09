package dashboards

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

type handler struct {
	svc     *Service
	guard   *authz.Guard
	baseURL string
	logger  *slog.Logger
}

func (h *handler) mount(r *httpx.ServiceMux) {
	tenant := func(f http.HandlerFunc) http.Handler {
		return h.guard.Wrap(authz.Requirement{Scope: authz.ScopeTenant}, f)
	}
	tenantKiosk := func(f http.HandlerFunc) http.Handler {
		return h.guard.Wrap(authz.Requirement{Scope: authz.ScopeTenant, AllowKiosk: true}, f)
	}
	r.Handle("GET /api/v1/widget-types", h.guard.Wrap(authz.Requirement{AllowKiosk: true}, http.HandlerFunc(h.widgetTypes)))
	r.Handle("GET /api/v1/dashboards", tenant(h.list))
	r.Handle("POST /api/v1/dashboards", tenant(h.create))
	r.Handle("GET /api/v1/dashboards/{dashboard_id}", tenantKiosk(h.get))
	r.Handle("PATCH /api/v1/dashboards/{dashboard_id}", tenant(h.update))
	r.Handle("DELETE /api/v1/dashboards/{dashboard_id}", tenant(h.delete))
	r.Handle("POST /api/v1/dashboards/{dashboard_id}/duplicate", tenant(h.duplicate))
	r.Handle("PUT /api/v1/dashboards/{dashboard_id}/layout", tenant(h.layout))
	r.Handle("POST /api/v1/dashboards/{dashboard_id}/widgets", tenant(h.addWidget))
	r.Handle("PATCH /api/v1/dashboards/{dashboard_id}/widgets/{widget_id}", tenant(h.updateWidget))
	r.Handle("DELETE /api/v1/dashboards/{dashboard_id}/widgets/{widget_id}", tenant(h.deleteWidget))
	r.Handle("GET /api/v1/dashboards/{dashboard_id}/widgets/{widget_id}/data", tenantKiosk(h.widgetData))
	r.Handle("POST /api/v1/widget-data/preview", tenant(h.preview))
	r.Handle("GET /api/v1/playlists", tenant(h.listPlaylists))
	r.Handle("POST /api/v1/playlists", tenant(h.createPlaylist))
	r.Handle("PATCH /api/v1/playlists/{playlist_id}", tenant(h.updatePlaylist))
	r.Handle("DELETE /api/v1/playlists/{playlist_id}", tenant(h.deletePlaylist))
	r.Handle("GET /api/v1/kiosk/config", tenantKiosk(h.kioskConfig))
}

func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok && e.Current != nil {
		switch c := e.Current.(type) {
		case *Dashboard:
			e.Current = dashboardJSON(c)
		case *Playlist:
			e.Current = playlistJSON(c)
		}
	}
	apperr.WriteHTTP(w, r, h.logger, err)
}

func dashboardJSON(d *Dashboard) map[string]any {
	return map[string]any{
		"id": d.ID, "tenant_id": d.TenantID, "version": d.Version, "name": d.Name, "visibility": d.Visibility, "owner_id": d.OwnerID,
		"template_key": d.TemplateKey, "template_version": d.TemplateVersion, "layout": d.Layout, "default_range": d.DefaultRange,
		"refresh_seconds": d.RefreshSeconds, "variables": d.Variables, "widgets": d.Widgets, "created_at": fmtTS(d.CreatedAt),
		"updated_at": fmtTS(d.UpdatedAt),
	}
}

func summaryJSON(d *Dashboard) map[string]any {
	return map[string]any{"id": d.ID, "tenant_id": d.TenantID, "version": d.Version, "name": d.Name, "visibility": d.Visibility,
		"owner_id": d.OwnerID, "template_key": d.TemplateKey, "widget_count": len(d.Widgets), "updated_at": fmtTS(d.UpdatedAt)}
}

func playlistJSON(p *Playlist) map[string]any {
	return map[string]any{"id": p.ID, "tenant_id": p.TenantID, "version": p.Version, "name": p.Name, "items": p.Items,
		"transition": p.Transition, "created_at": fmtTS(p.CreatedAt), "updated_at": fmtTS(p.UpdatedAt)}
}

func writeDash(w http.ResponseWriter, status int, d *Dashboard) {
	w.Header().Set("ETag", jsonapi.ETag(d.Version))
	jsonapi.Write(w, status, dashboardJSON(d))
}

func pathID(w http.ResponseWriter, r *http.Request, name, code string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, code)
		return uuid.Nil, false
	}
	return id, true
}

func (h *handler) ifMatch(w http.ResponseWriter, r *http.Request) (int, bool) {
	v, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return 0, false
	}
	return v, true
}

func limitOf(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("limit debe estar entre 1 y 200"))
			return 0, false
		}
		limit = n
	}
	return limit, true
}

func (h *handler) widgetTypes(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "private, max-age=300")
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": h.svc.catalog.Types, "version": h.svc.catalog.Version})
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	limit, ok := limitOf(w, r)
	if !ok {
		return
	}
	rows, pg, err := h.svc.ListDashboards(r.Context(), r.URL.Query().Get("visibility"), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for i := range rows {
		data = append(data, summaryJSON(&rows[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": pg})
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	var in DashboardInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	d, err := h.svc.CreateDashboard(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/dashboards/"+d.ID.String())
	writeDash(w, http.StatusCreated, d)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "dashboard_id", CodeDashboardNotFound)
	if !ok {
		return
	}
	d, err := h.svc.GetDashboard(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeDash(w, http.StatusOK, d)
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "dashboard_id", CodeDashboardNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var f map[string]json.RawMessage
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	d, err := h.svc.UpdateDashboard(r.Context(), id, ver, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeDash(w, http.StatusOK, d)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "dashboard_id", CodeDashboardNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteDashboard(r.Context(), id, ver); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) duplicate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "dashboard_id", CodeDashboardNotFound)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if r.ContentLength != 0 && !jsonapi.Decode(w, r, &body, false) {
		return
	}
	d, err := h.svc.DuplicateDashboard(r.Context(), id, body.Name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/dashboards/"+d.ID.String())
	writeDash(w, http.StatusCreated, d)
}

func (h *handler) layout(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "dashboard_id", CodeDashboardNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var in LayoutReplace
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	d, err := h.svc.ReplaceLayout(r.Context(), id, ver, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeDash(w, http.StatusOK, d)
}

func (h *handler) addWidget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "dashboard_id", CodeDashboardNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var in Widget
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	d, err := h.svc.AddWidget(r.Context(), id, ver, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeDash(w, http.StatusCreated, d)
}

func (h *handler) updateWidget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "dashboard_id", CodeDashboardNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var f map[string]json.RawMessage
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	d, err := h.svc.UpdateWidget(r.Context(), id, r.PathValue("widget_id"), ver, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeDash(w, http.StatusOK, d)
}

func (h *handler) deleteWidget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "dashboard_id", CodeDashboardNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	d, err := h.svc.DeleteWidget(r.Context(), id, r.PathValue("widget_id"), ver)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeDash(w, http.StatusOK, d)
}

func (h *handler) widgetData(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "dashboard_id", CodeDashboardNotFound)
	if !ok {
		return
	}
	out, refresh, err := h.svc.WidgetData(r.Context(), id, r.PathValue("widget_id"), r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body, err := json.Marshal(out)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(max(refresh/2, 1)))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *handler) preview(w http.ResponseWriter, r *http.Request) {
	var in PreviewInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	out, err := h.svc.Preview(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, out)
}

func (h *handler) listPlaylists(w http.ResponseWriter, r *http.Request) {
	limit, ok := limitOf(w, r)
	if !ok {
		return
	}
	rows, pg, err := h.svc.ListPlaylists(r.Context(), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for i := range rows {
		data = append(data, playlistJSON(&rows[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": pg})
}

func (h *handler) createPlaylist(w http.ResponseWriter, r *http.Request) {
	var in PlaylistInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	p, err := h.svc.CreatePlaylist(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(p.Version))
	jsonapi.Write(w, http.StatusCreated, playlistJSON(p))
}

func (h *handler) updatePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "playlist_id", problem.CodeNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	var in PlaylistInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	p, err := h.svc.UpdatePlaylist(r.Context(), id, ver, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(p.Version))
	jsonapi.Write(w, http.StatusOK, playlistJSON(p))
}

func (h *handler) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "playlist_id", problem.CodeNotFound)
	if !ok {
		return
	}
	ver, ok := h.ifMatch(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeletePlaylist(r.Context(), id, ver); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) kioskConfig(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.KioskConfig(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, c)
}
