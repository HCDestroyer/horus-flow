package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// SSO sirve el autologin desde otros sistemas (auth.yaml tag sso y
// /me/sso-links; contrato docs/integrations/argosisp.md §1.5).
type SSO struct {
	h   *Handler
	svc *app.SSO
}

// NewSSO crea el handler (comparte guard, orígenes y logger con h).
func NewSSO(h *Handler, svc *app.SSO) *SSO { return &SSO{h: h, svc: svc} }

// Mount registra las rutas.
func (s *SSO) Mount(r *httpx.ServiceMux) {
	g := s.h.guard
	r.HandleFunc("POST /api/v1/integrations/argosisp/sso/redeem", s.redeem)
	r.Handle("GET /api/v1/me/sso-links", g.Session(s.myLinks))
	r.Handle("POST /api/v1/me/sso-links", g.Session(s.link))
	r.Handle("DELETE /api/v1/me/sso-links/{link_id}", g.Session(s.deleteMine))
	r.Handle("GET /api/v1/integrations/argosisp/sso/links", g.Tenant(app.PermUsersRead, s.tenantLinks))
	r.Handle("DELETE /api/v1/integrations/argosisp/sso/links", g.Tenant(app.PermUsersManage, s.revokeAll))
	r.Handle("DELETE /api/v1/integrations/argosisp/sso/links/{link_id}", g.Tenant(app.PermUsersManage, s.revoke))
}

func targetJSON(t *app.SSOTarget) map[string]any {
	return map[string]any{"tenant_id": t.TenantID, "tenant_slug": t.TenantSlug, "type": t.Type, "external_id": t.ExternalID}
}

// SSOLinkJSON es SsoLink del contrato.
func SSOLinkJSON(l *domain.SSOLink) map[string]any {
	var used any
	if l.LastUsedAt != nil {
		used = ts(*l.LastUsedAt)
	}
	return map[string]any{"id": l.ID, "tenant_id": l.TenantID, "tenant_name": l.TenantName, "system": l.System,
		"connector_id": l.ConnectorID, "user_id": l.UserID, "user_email": l.UserEmail,
		"external_user": map[string]any{"id": l.ExternalUserID, "email": l.ExternalEmail, "name": l.ExternalName},
		"created_at":    ts(l.CreatedAt), "last_used_at": used}
}

// redeem fija la cookie de refresh: misma defensa CSRF que /auth/refresh
// (X-Requested-With y Origin permitido).
func (s *SSO) redeem(w http.ResponseWriter, r *http.Request) {
	if !s.h.origins.CheckCookieRequest(w, r) {
		return
	}
	var body struct {
		Ticket string `json:"ticket"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	if l := len(body.Ticket); l < 20 || l > 128 {
		problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("ticket es obligatorio."))
		return
	}
	res, err := s.svc.Redeem(r.Context(), body.Ticket, loginInput(r))
	if err != nil {
		s.h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	out := map[string]any{"status": res.Status, "system": domain.SSOSystemArgos, "target": targetJSON(&res.Target),
		"external_user": map[string]any{"id": res.ExternalUser.ID, "email": res.ExternalUser.Email, "name": res.ExternalUser.Name}}
	switch res.Status {
	case "authenticated":
		t := res.Tokens
		http.SetCookie(w, &http.Cookie{
			Name: RefreshCookie, Value: t.Refresh, Path: refreshPath, Expires: t.RefreshExpiresAt,
			MaxAge: int(time.Until(t.RefreshExpiresAt).Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
		})
		out["access_token"], out["token_type"], out["expires_at"], out["scope"] = t.Access, "Bearer", ts(t.ExpiresAt), t.Scope
	case "mfa_required":
		out["mfa_token"] = res.MFAToken
	case "link_required":
		out["link_token"], out["link_expires_at"] = res.LinkToken, ts(res.LinkExpiresAt)
	}
	jsonapi.Write(w, http.StatusOK, out)
}

func (s *SSO) link(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LinkToken string `json:"link_token"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	if l := len(body.LinkToken); l < 20 || l > 512 {
		problem.Std(w, r, http.StatusUnprocessableEntity, problem.CodeValidationFailed,
			problem.WithErrors(problem.FieldError{Field: "link_token", Code: "REQUIRED", Message: "Obligatorio."}))
		return
	}
	l, target, err := s.svc.Link(r.Context(), authz.FromContext(r.Context()), body.LinkToken, loginInput(r))
	if err != nil {
		s.h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusCreated, map[string]any{"link": SSOLinkJSON(l), "target": targetJSON(target)})
}

func linksJSON(ls []domain.SSOLink) []map[string]any {
	out := make([]map[string]any, 0, len(ls))
	for i := range ls {
		out = append(out, SSOLinkJSON(&ls[i]))
	}
	return out
}

func (s *SSO) myLinks(w http.ResponseWriter, r *http.Request) {
	ls, err := s.svc.MyLinks(r.Context(), authz.FromContext(r.Context()))
	if err != nil {
		s.h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": linksJSON(ls)})
}

func linkID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("link_id"))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, app.CodeSSOLinkNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func (s *SSO) deleteMine(w http.ResponseWriter, r *http.Request) {
	id, ok := linkID(w, r)
	if !ok {
		return
	}
	if err := s.svc.DeleteMyLink(r.Context(), authz.FromContext(r.Context()), id, loginInput(r)); err != nil {
		s.h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *SSO) tenantLinks(w http.ResponseWriter, r *http.Request) {
	ls, err := s.svc.TenantLinks(r.Context())
	if err != nil {
		s.h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": linksJSON(ls)})
}

func (s *SSO) revoke(w http.ResponseWriter, r *http.Request) {
	id, ok := linkID(w, r)
	if !ok {
		return
	}
	if err := s.svc.RevokeLink(r.Context(), id); err != nil {
		s.h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *SSO) revokeAll(w http.ResponseWriter, r *http.Request) {
	if !s.h.requireReauth(w, r) {
		return
	}
	n, err := s.svc.RevokeAllLinks(r.Context())
	if err != nil {
		s.h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"revoked": n})
}
