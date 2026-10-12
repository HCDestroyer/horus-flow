package remote

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/clientip"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// ReauthMaxAge es la antigüedad máxima de auth_time en las rutas x-reauth
// (el gateway ya lo exige; aquí se repite como defensa en profundidad).
const ReauthMaxAge = 5 * time.Minute

const basePath = "/api/v1/platform/remote-destinations"

// Handler sirve las rutas de destinos remotos (platform.yaml, x-module jobs).
type Handler struct {
	svc     *Service
	guard   *authz.Guard
	baseURL string
	logger  *slog.Logger
}

// NewHandler crea el handler.
func NewHandler(svc *Service, guard *authz.Guard, baseURL string, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Handler{svc: svc, guard: guard, baseURL: baseURL, logger: logger}
}

// Mount registra las rutas (token de plataforma con platform.storage.manage).
func (h *Handler) Mount(r *httpx.ServiceMux) {
	g := h.guard
	r.Handle("GET /api/v1/platform/remote-destinations", g.Platform(PermManage, h.list))
	r.Handle("POST /api/v1/platform/remote-destinations", g.Platform(PermManage, h.create))
	r.Handle("PATCH /api/v1/platform/remote-destinations/{destination_id}", g.Platform(PermManage, h.update))
	r.Handle("DELETE /api/v1/platform/remote-destinations/{destination_id}", g.Platform(PermManage, h.delete))
	r.Handle("PUT /api/v1/platform/remote-destinations/{destination_id}/credentials", g.Platform(PermManage, h.credentials))
	r.Handle("POST /api/v1/platform/remote-destinations/{destination_id}/test", g.Platform(PermManage, h.test))
	r.Handle("POST /api/v1/platform/remote-destinations/{destination_id}/sync", g.Platform(PermManage, h.sync))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	apperr.WriteHTTP(w, r, h.logger, err)
}

func (h *Handler) requireReauth(w http.ResponseWriter, r *http.Request) bool {
	if authz.FromContext(r.Context()).AuthAge(h.svc.Now()) > ReauthMaxAge {
		problem.Std(w, r, http.StatusForbidden, problem.CodeReauthRequired)
		return false
	}
	return true
}

func meta(r *http.Request) AuditMeta {
	return AuditMeta{IP: clientip.String(r), UserAgent: r.UserAgent(), RequestID: r.Header.Get("X-Request-Id")}
}

func (h *Handler) pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("destination_id"))
	if err != nil {
		h.fail(w, r, errNotFound())
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	data, err := h.svc.List(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if !h.requireReauth(w, r) {
		return
	}
	var in CreateInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	v, err := h.svc.Create(r.Context(), in, meta(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, basePath+"/"+v.ID.String())
	w.Header().Set("ETag", jsonapi.ETag(v.Version))
	jsonapi.Write(w, http.StatusCreated, v)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	if !h.requireReauth(w, r) {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in Patch
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	v, err := h.svc.Update(r.Context(), id, ver, in, meta(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(v.Version))
	jsonapi.Write(w, http.StatusOK, v)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if !h.requireReauth(w, r) {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.Delete(r.Context(), id, ver, meta(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) credentials(w http.ResponseWriter, r *http.Request) {
	if !h.requireReauth(w, r) {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var in Credentials
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	v, err := h.svc.SetCredentials(r.Context(), id, in, meta(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(v.Version))
	jsonapi.Write(w, http.StatusOK, v)
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	if !h.requireReauth(w, r) {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		ConfirmHostKey *string `json:"confirm_host_key"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !jsonapi.Decode(w, r, &in, true) {
			return
		}
	}
	confirm := ""
	if in.ConfirmHostKey != nil {
		confirm = strings.TrimSpace(*in.ConfirmHostKey)
	}
	res, err := h.svc.Test(r.Context(), id, confirm, meta(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, res)
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" { // también lo exige el gateway
		h.fail(w, r, apperr.PreconditionRequired("Idempotency-Key es obligatoria."))
		return
	}
	v, err := h.svc.RequestSync(r.Context(), id, key, meta(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusAccepted, v)
}
