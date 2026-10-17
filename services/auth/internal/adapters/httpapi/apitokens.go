package httpapi

import (
	"net/http"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// APITokens sirve los API tokens de integración (auth.yaml /api-tokens*,
// /api-token-templates; docs/security.md §5.4).
type APITokens struct {
	h   *Handler
	svc *app.APITokens
}

// NewAPITokens crea el handler (comparte guard y logger con h).
func NewAPITokens(h *Handler, svc *app.APITokens) *APITokens { return &APITokens{h: h, svc: svc} }

// Mount registra las rutas.
func (a *APITokens) Mount(r *httpx.ServiceMux) {
	g := a.h.guard
	r.Handle("GET /api/v1/api-tokens", g.Tenant(app.PermAPITokensManage, a.list))
	r.Handle("POST /api/v1/api-tokens", g.Tenant(app.PermAPITokensManage, a.create))
	r.Handle("DELETE /api/v1/api-tokens/{api_token_id}", g.Tenant(app.PermAPITokensManage, a.revoke))
	r.Handle("GET /api/v1/api-token-templates", g.Tenant(app.PermAPITokensManage, a.templates))
}

// APITokenJSON es ApiToken del contrato (sin valor ni hash).
func APITokenJSON(t *domain.APIToken) map[string]any {
	var used, revoked any
	if t.LastUsedAt != nil {
		used = ts(*t.LastUsedAt)
	}
	if t.RevokedAt != nil {
		revoked = ts(*t.RevokedAt)
	}
	return map[string]any{
		"id": t.ID, "name": t.Name, "prefix": t.Prefix, "user_id": t.UserID, "permissions": t.Permissions, "template": t.Template,
		"created_at": ts(t.CreatedAt), "expires_at": ts(t.ExpiresAt), "last_used_at": used, "last_used_ip": t.LastUsedIP,
		"revoked_at": revoked,
	}
}

func (a *APITokens) view(t *domain.APIToken) map[string]any {
	v := APITokenJSON(t)
	now := time.Now()
	if a.h.now != nil {
		now = a.h.now()
	}
	v["active"] = t.Active(now)
	return v
}

func (a *APITokens) list(w http.ResponseWriter, r *http.Request) {
	rows, err := a.svc.List(r.Context())
	if err != nil {
		a.h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for i := range rows {
		data = append(data, a.view(&rows[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data})
}

func (a *APITokens) create(w http.ResponseWriter, r *http.Request) {
	var in app.APITokenInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	res, err := a.svc.Create(r.Context(), in)
	if err != nil {
		a.h.fail(w, r, err)
		return
	}
	body := a.view(&res.Token)
	body["token"] = res.Raw
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusCreated, body)
}

func (a *APITokens) revoke(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "api_token_id", app.CodeAPITokenNotFound)
	if !ok {
		return
	}
	if err := a.svc.Revoke(r.Context(), id); err != nil {
		a.h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *APITokens) templates(w http.ResponseWriter, _ *http.Request) {
	data := make([]map[string]any, 0, len(domain.APITokenTemplates))
	for _, t := range domain.APITokenTemplates {
		data = append(data, map[string]any{"key": t.Key, "name": t.Name, "description": t.Description, "permissions": t.Permissions})
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data})
}
