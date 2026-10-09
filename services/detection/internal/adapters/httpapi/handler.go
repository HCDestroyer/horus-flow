// Package httpapi es el adaptador REST de detection: rutas I1 de
// packages/schemas/openapi/v0/detection.yaml (hallazgos, evidencia,
// resumen de seguridad y allowlist del ISP).
package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
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
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Handler{svc: svc, guard: guard, baseURL: baseURL, logger: logger}
}

// Mount registra las rutas (permiso grueso en el guard; alcance fino en app).
func (h *Handler) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/findings", h.guard.Tenant(app.PermRead, h.list))
	r.Handle("GET /api/v1/findings/{finding_id}", h.guard.Tenant(app.PermRead, h.get))
	r.Handle("GET /api/v1/findings/{finding_id}/evidence", h.guard.Tenant(app.PermEvidence, h.evidence))
	r.Handle("POST /api/v1/findings/{finding_id}/acknowledge", h.guard.Tenant(app.PermManage, h.transition("acknowledge")))
	r.Handle("POST /api/v1/findings/{finding_id}/resolve", h.guard.Tenant(app.PermManage, h.transition("resolve")))
	r.Handle("POST /api/v1/findings/{finding_id}/mark-false-positive", h.guard.Tenant(app.PermManage, h.transition("false_positive")))
	r.Handle("GET /api/v1/customers/{customer_id}/findings", h.guard.Tenant(app.PermRead, h.customerFindings))
	r.Handle("GET /api/v1/security/summary", h.guard.Wrap(authz.Requirement{Scope: authz.ScopeTenant, AllowKiosk: true},
		http.HandlerFunc(h.summary)))
	r.Handle("GET /api/v1/reputation/sources", h.guard.Tenant(app.PermRead, h.sources))
	r.Handle("GET /api/v1/reputation/allowlist", h.guard.Tenant(app.PermRead, h.listAllow))
	r.Handle("POST /api/v1/reputation/allowlist", h.guard.Tenant(app.PermManage, h.createAllow))
	r.Handle("DELETE /api/v1/reputation/allowlist/{entry_id}", h.guard.Tenant(app.PermManage, h.deleteAllow))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
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

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, err := h.svc.List(r.Context(), r.URL.Query(), nil)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": page.Data, "page": page.Page})
}

func (h *Handler) customerFindings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "customer_id", "CUSTOMER_NOT_FOUND")
	if !ok {
		return
	}
	page, err := h.svc.List(r.Context(), r.URL.Query(), &id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": page.Data, "page": page.Page})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "finding_id", app.CodeFindingNotFound)
	if !ok {
		return
	}
	doc, ver, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(ver))
	jsonapi.Write(w, http.StatusOK, doc)
}

func (h *Handler) transition(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "finding_id", app.CodeFindingNotFound)
		if !ok {
			return
		}
		ver, err := jsonapi.IfMatch(r)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		var body app.Transition
		raw, err := io.ReadAll(io.LimitReader(r.Body, jsonapi.MaxBody))
		if err != nil {
			problem.Write(w, r, http.StatusBadRequest, problem.CodeValidationFailed, "Cuerpo ilegible")
			return
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if !jsonapi.Decode(w, r, &body, true) {
				return
			}
		}
		doc, v, err := h.svc.Transition(r.Context(), id, ver, op, body)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		w.Header().Set("ETag", jsonapi.ETag(v))
		jsonapi.Write(w, http.StatusOK, doc)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handler) evidence(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "finding_id", app.CodeFindingNotFound)
	if !ok {
		return
	}
	data, page, err := h.svc.Evidence(r.Context(), id, r.URL.Query(), app.AuditMeta{IP: clientIP(r), UserAgent: r.UserAgent(),
		RequestID: r.Header.Get("X-Request-Id")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": page})
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Summary(r.Context(), r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, out)
}

func (h *Handler) listAllow(w http.ResponseWriter, r *http.Request) {
	data, page, err := h.svc.ListAllow(r.Context(), r.URL.Query())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": page})
}

func (h *Handler) createAllow(w http.ResponseWriter, r *http.Request) {
	var in app.AllowInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	out, err := h.svc.CreateAllow(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/reputation/allowlist/"+out["id"].(uuid.UUID).String())
	jsonapi.Write(w, http.StatusCreated, out)
}

func (h *Handler) deleteAllow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "entry_id", problem.CodeNotFound)
	if !ok {
		return
	}
	if err := h.svc.DeleteAllow(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) sources(w http.ResponseWriter, r *http.Request) {
	data, err := h.svc.ListSources(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data})
}
