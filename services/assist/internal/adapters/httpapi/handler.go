// Package httpapi es el adaptador REST del asistente de IA
// (packages/schemas/openapi/v0/assist.yaml).
package httpapi

import (
	"encoding/base64"
	"encoding/json"
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
	"github.com/hcdestroyer/horus-flow/services/assist/internal/app"
	"github.com/hcdestroyer/horus-flow/services/assist/internal/domain"
)

// Handler sirve las rutas REST del módulo.
type Handler struct {
	svc    *app.Service
	guard  *authz.Guard
	logger *slog.Logger
}

// New crea el handler.
func New(svc *app.Service, guard *authz.Guard, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Handler{svc: svc, guard: guard, logger: logger}
}

// Mount registra las rutas (permiso grueso en el guard; el fino en app).
func (h *Handler) Mount(r *httpx.ServiceMux) {
	tenant := func(f http.HandlerFunc) http.Handler {
		return h.guard.Wrap(authz.Requirement{Scope: authz.ScopeTenant}, f)
	}
	r.Handle("GET /api/v1/assist/status", tenant(h.status))
	r.Handle("GET /api/v1/assist/settings", h.guard.Tenant(app.PermSettingsRead, h.getSettings))
	r.Handle("PUT /api/v1/assist/settings", h.guard.Tenant(app.PermSettingsManage, h.putSettings))
	r.Handle("POST /api/v1/assist/settings/test", h.guard.Tenant(app.PermSettingsManage, h.test))
	r.Handle("POST /api/v1/assist/analyses", tenant(h.analyze))
	r.Handle("GET /api/v1/assist/analyses", tenant(h.history))
	r.Handle("GET /api/v1/assist/audit", h.guard.Tenant(app.PermSettingsRead, h.audit))
	r.Handle("GET /api/v1/assist/mcp-tokens", h.guard.Tenant(app.PermMCPTokens, h.listTokens))
	r.Handle("POST /api/v1/assist/mcp-tokens", h.guard.Tenant(app.PermMCPTokens, h.createToken))
	r.Handle("DELETE /api/v1/assist/mcp-tokens/{token_id}", h.guard.Tenant(app.PermMCPTokens, h.revokeToken))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	apperr.WriteHTTP(w, r, h.logger, err)
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Status(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, st)
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.Settings(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, v)
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var body domain.Update
	if !jsonapi.Decode(w, r, &body, true) {
		return
	}
	v, err := h.svc.UpdateSettings(r.Context(), body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, v)
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.TestConnection(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, res)
}

func (h *Handler) analyze(w http.ResponseWriter, r *http.Request) {
	var body app.AnalysisRequest
	if !jsonapi.Decode(w, r, &body, true) {
		return
	}
	res, err := h.svc.Analyze(r.Context(), body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, res)
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 10
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 50 {
			problem.Write(w, r, http.StatusBadRequest, problem.CodeValidationFailed, "limit fuera de rango (1–50)")
			return
		}
		limit = n
	}
	list, err := h.svc.History(r.Context(), q.Get("feature"), q.Get("context_id"), limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": list})
}

type cursor struct {
	T  int64     `json:"t"`
	ID uuid.UUID `json:"i"`
}

func encodeCursor(c *app.AuditCursor) *string {
	if c == nil {
		return nil
	}
	b, _ := json.Marshal(cursor{T: c.CreatedAt.UnixMicro(), ID: c.ID})
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func decodeCursor(s string) (*app.AuditCursor, bool) {
	if s == "" {
		return nil, true
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil {
		return nil, false
	}
	return &app.AuditCursor{CreatedAt: time.UnixMicro(c.T).UTC(), ID: c.ID}, true
}

func (h *Handler) audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			problem.Write(w, r, http.StatusBadRequest, problem.CodeValidationFailed, "limit fuera de rango (1–200)")
			return
		}
		limit = n
	}
	before, ok := decodeCursor(q.Get("cursor"))
	if !ok {
		problem.Std(w, r, http.StatusBadRequest, problem.CodeInvalidCursor)
		return
	}
	page, err := h.svc.Audit(r.Context(), limit, before)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	next := encodeCursor(page.Next)
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": page.Data,
		"page": map[string]any{"limit": limit, "has_more": next != nil, "next_cursor": next, "prev_cursor": nil}})
}

func (h *Handler) listTokens(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListMCPTokens(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": list})
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !jsonapi.Decode(w, r, &body, true) {
		return
	}
	t, err := h.svc.CreateMCPToken(r.Context(), body.Name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusCreated, t)
}

func (h *Handler) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("token_id"))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, app.CodeTokenNotFound)
		return
	}
	if err := h.svc.RevokeMCPToken(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
