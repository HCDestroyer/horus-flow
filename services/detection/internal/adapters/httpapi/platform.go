package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
)

// Platform sirve las rutas de plataforma de detection
// (packages/schemas/openapi/v0/platform.yaml, x-module: detection): gestión
// de las fuentes de reputación (D20). Re-autenticación e Idempotency-Key los
// exige el gateway (gateway-routes.yaml).
type Platform struct {
	svc     *app.ReputationSources
	guard   *authz.Guard
	baseURL string
	logger  *slog.Logger
}

// NewPlatform crea el handler de plataforma.
func NewPlatform(svc *app.ReputationSources, guard *authz.Guard, baseURL string, logger *slog.Logger) *Platform {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Platform{svc: svc, guard: guard, baseURL: baseURL, logger: logger}
}

const sourcesPath = "/api/v1/platform/reputation/sources"

// Mount registra las rutas (token de plataforma con platform.reputation_sources.manage).
func (h *Platform) Mount(r *httpx.ServiceMux) {
	perm := app.PermReputationSources
	r.Handle("GET "+sourcesPath, h.guard.Platform(perm, h.list))
	r.Handle("POST "+sourcesPath, h.guard.Platform(perm, h.create))
	r.Handle("GET "+sourcesPath+"/{source_id}", h.guard.Platform(perm, h.get))
	r.Handle("PATCH "+sourcesPath+"/{source_id}", h.guard.Platform(perm, h.update))
	r.Handle("DELETE "+sourcesPath+"/{source_id}", h.guard.Platform(perm, h.delete))
	r.Handle("POST "+sourcesPath+"/{source_id}/refresh", h.guard.Platform(perm, h.refresh))
}

func (h *Platform) fail(w http.ResponseWriter, r *http.Request, err error) {
	apperr.WriteHTTP(w, r, h.logger, err)
}

func meta(r *http.Request) app.AuditMeta {
	return app.AuditMeta{IP: clientIP(r), UserAgent: r.UserAgent(), RequestID: r.Header.Get("X-Request-Id")}
}

func (h *Platform) list(w http.ResponseWriter, r *http.Request) {
	f, err := app.ParseSourceFilter(r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data, err := h.svc.List(r.Context(), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data})
}

func (h *Platform) create(w http.ResponseWriter, r *http.Request) {
	var in app.SourceCreate
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	doc, err := h.svc.Create(r.Context(), in, meta(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, sourcesPath+"/"+doc.ID.String())
	w.Header().Set("ETag", jsonapi.ETag(doc.Version))
	jsonapi.Write(w, http.StatusCreated, doc)
}

func (h *Platform) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "source_id", app.CodeSourceNotFound)
	if !ok {
		return
	}
	doc, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(doc.Version))
	jsonapi.Write(w, http.StatusOK, doc)
}

func (h *Platform) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "source_id", app.CodeSourceNotFound)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var patch app.SourcePatch
	if !jsonapi.Decode(w, r, &patch, false) {
		return
	}
	doc, err := h.svc.Update(r.Context(), id, ver, patch, meta(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(doc.Version))
	jsonapi.Write(w, http.StatusOK, doc)
}

func (h *Platform) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "source_id", app.CodeSourceNotFound)
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

func (h *Platform) refresh(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "source_id", app.CodeSourceNotFound)
	if !ok {
		return
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" { // también lo exige el gateway
		h.fail(w, r, apperr.PreconditionRequired("Idempotency-Key es obligatoria."))
		return
	}
	doc, err := h.svc.Refresh(r.Context(), id, meta(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusAccepted, doc)
}
