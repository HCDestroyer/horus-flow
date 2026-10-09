package notify

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// ReauthMaxAge es la antigüedad máxima de auth_time en rutas 🔒.
const ReauthMaxAge = 5 * time.Minute

// Handler sirve las rutas de alerts.yaml.
type Handler struct {
	svc     *Service
	guard   *authz.Guard
	baseURL string
	logger  *slog.Logger
}

// NewHandler crea el handler.
func NewHandler(svc *Service, guard *authz.Guard, baseURL string, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, guard: guard, baseURL: baseURL, logger: logger}
}

// Mount registra las rutas.
func (h *Handler) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/notification-channels", h.guard.Tenant(PermRead, h.list))
	r.Handle("POST /api/v1/notification-channels", h.guard.Tenant(PermManage, h.create))
	r.Handle("GET /api/v1/notification-channels/{channel_id}", h.guard.Tenant(PermRead, h.get))
	r.Handle("PATCH /api/v1/notification-channels/{channel_id}", h.guard.Tenant(PermManage, h.update))
	r.Handle("DELETE /api/v1/notification-channels/{channel_id}", h.guard.Tenant(PermManage, h.delete))
	r.Handle("PUT /api/v1/notification-channels/{channel_id}/credentials", h.guard.Tenant(PermManage, h.credentials))
	r.Handle("POST /api/v1/notification-channels/{channel_id}/connection-test", h.guard.Tenant(PermManage, h.connectionTest))
	r.Handle("POST /api/v1/notification-channels/{channel_id}/test", h.guard.Tenant(PermManage, h.test))
	r.Handle("GET /api/v1/notification-deliveries", h.guard.Tenant(PermRead, h.deliveries))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok && e.Current != nil {
		if c, ok := e.Current.(*Channel); ok {
			e.Current = channelJSON(c)
		}
	}
	apperr.WriteHTTP(w, r, h.logger, err)
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

func channelJSON(c *Channel) map[string]any {
	sub := c.Subscription
	if sub.SiteIDs == nil {
		sub.SiteIDs = []uuid.UUID{}
	}
	return map[string]any{
		"id": c.ID, "tenant_id": c.TenantID, "version": c.Version, "created_at": ts(c.CreatedAt), "updated_at": ts(c.UpdatedAt),
		"name": c.Name, "kind": c.Kind, "enabled": c.Enabled, "config": c.Config, "subscription": sub,
		"include_personal_data": c.IncludePersonalData, "status": c.Status, "has_credentials": c.HasCredentials(),
		"last_delivery_at": tsPtr(c.LastDeliveryAt), "last_error": c.LastError,
	}
}

func deliveryJSON(d *Delivery) map[string]any {
	return map[string]any{
		"id": d.ID, "tenant_id": d.TenantID, "channel_id": d.ChannelID, "channel_kind": d.ChannelKind, "status": d.Status,
		"event_type": d.EventType, "source_event_type": d.SourceEventType, "source_event_id": d.SourceEventID, "is_test": d.IsTest,
		"error": d.Error, "created_at": ts(d.CreatedAt), "sent_at": tsPtr(d.SentAt),
	}
}

func writeChannel(w http.ResponseWriter, status int, c *Channel) {
	w.Header().Set("ETag", jsonapi.ETag(c.Version))
	jsonapi.Write(w, status, channelJSON(c))
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("channel_id"))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, CodeChannelNotFound)
		return uuid.Nil, false
	}
	return id, true
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

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, ok := limitOf(w, r)
	if !ok {
		return
	}
	rows, pg, err := h.svc.List(r.Context(), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for i := range rows {
		data = append(data, channelJSON(&rows[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": pg})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in ChannelInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	c, err := h.svc.Create(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/notification-channels/"+c.ID.String())
	writeChannel(w, http.StatusCreated, c)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeChannel(w, http.StatusOK, c)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in ChannelInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	c, err := h.svc.Update(r.Context(), id, ver, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeChannel(w, http.StatusOK, c)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.Delete(r.Context(), id, ver); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) credentials(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if authz.FromContext(r.Context()).AuthAge(time.Now()) > ReauthMaxAge {
		problem.Std(w, r, http.StatusForbidden, problem.CodeReauthRequired)
		return
	}
	var cr Credentials
	if !jsonapi.Decode(w, r, &cr, true) {
		return
	}
	if err := h.svc.PutCredentials(r.Context(), id, cr); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) connectionTest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, res, at, err := h.svc.ConnectionTest(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"ok": res.OK, "channel_id": c.ID, "channel_kind": c.Kind, "checked_at": ts(at),
		"latency_ms": res.LatencyMS, "remote_version": res.RemoteVersion, "error_code": res.ErrorCode, "error": res.Error})
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := h.svc.Test(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusAccepted, deliveryJSON(d))
}

func (h *Handler) deliveries(w http.ResponseWriter, r *http.Request) {
	limit, ok := limitOf(w, r)
	if !ok {
		return
	}
	qv := r.URL.Query()
	q := DeliveryQuery{Status: qv.Get("status"), Kind: qv.Get("channel_kind"), Limit: limit}
	if v := qv.Get("channel_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeInvalidFilter)
			return
		}
		q.ChannelID = &id
	}
	rows, pg, err := h.svc.Deliveries(r.Context(), q, qv.Get("cursor"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for i := range rows {
		data = append(data, deliveryJSON(&rows[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": pg})
}
