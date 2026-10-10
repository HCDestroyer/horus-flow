// Package httpapi es el adaptador REST del módulo auth: rutas de
// packages/schemas/openapi/v0/auth.yaml y platform.yaml (x-module: auth) del
// incremento I0 (+ TOTP y re-auth adelantados por D14).
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// RefreshCookie es la cookie del refresh (security.md S4).
const RefreshCookie = "__Secure-hf_rt"

// refreshPath es el Path de la cookie.
const refreshPath = "/api/v1/auth"

// ReauthMaxAge es la antigüedad máxima de auth_time en rutas 🔒.
const ReauthMaxAge = 5 * time.Minute

// Handler sirve las rutas del módulo.
type Handler struct {
	svc     *app.Service
	guard   *authz.Guard
	origins authz.Origins
	cursor  *pagination.Codec
	baseURL string
	logger  *slog.Logger
	now     func() time.Time
}

// Options configura el handler.
type Options struct {
	Origins       authz.Origins
	Cursor        *pagination.Codec
	PublicBaseURL string
	Logger        *slog.Logger
	Now           func() time.Time
}

// New crea el handler.
func New(svc *app.Service, guard *authz.Guard, o Options) *Handler {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Cursor == nil {
		o.Cursor = pagination.NewCodec(nil)
	}
	return &Handler{svc: svc, guard: guard, origins: o.Origins, cursor: o.Cursor, baseURL: o.PublicBaseURL, logger: o.Logger, now: o.Now}
}

// Mount registra las rutas.
func (h *Handler) Mount(r *httpx.ServiceMux) {
	r.HandleFunc("POST /api/v1/auth/login", h.login)
	r.HandleFunc("POST /api/v1/auth/mfa/verify", h.mfaVerify)
	r.HandleFunc("POST /api/v1/auth/token", h.issueToken)
	r.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	r.HandleFunc("POST /api/v1/auth/logout", h.logout)
	r.Handle("POST /api/v1/auth/reauth", h.guard.Session(h.reauth))
	r.Handle("GET /api/v1/me", h.guard.Session(h.me))
	r.Handle("POST /api/v1/me/password", h.guard.Session(h.changePassword))
	r.Handle("POST /api/v1/me/totp/enroll", h.guard.Session(h.totpEnroll))
	r.Handle("POST /api/v1/me/totp/confirm", h.guard.Session(h.totpConfirm))
	r.Handle("GET /api/v1/permissions", h.guard.Session(h.permissions))
	r.Handle("GET /api/v1/platform/tenants", h.guard.Platform("platform.tenants.read", h.listTenants))
	r.Handle("POST /api/v1/platform/tenants", h.guard.Platform("platform.tenants.manage", h.createTenant))
	r.Handle("GET /api/v1/platform/tenants/{tenant_id}", h.guard.Platform("platform.tenants.read", h.getTenant))
	r.Handle("PATCH /api/v1/platform/tenants/{tenant_id}", h.guard.Platform("platform.tenants.manage", h.updateTenant))
	r.Handle("POST /api/v1/platform/tenants/{tenant_id}/suspend", h.guard.Platform("platform.tenants.manage", h.suspendTenant))
	r.Handle("POST /api/v1/platform/tenants/{tenant_id}/resume", h.guard.Platform("platform.tenants.manage", h.resumeTenant))
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	apperr.WriteHTTP(w, r, h.logger, err)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	if net.ParseIP(host) == nil {
		return ""
	}
	return host
}

func loginInput(r *http.Request) app.LoginInput {
	ua := r.UserAgent()
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return app.LoginInput{IP: clientIP(r), UserAgent: ua}
}

// ts formatea un instante como en el contrato (UTC, milisegundos, Z).
func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

type tokenResponse struct {
	AccessToken string     `json:"access_token"`
	TokenType   string     `json:"token_type"`
	ExpiresAt   string     `json:"expires_at"`
	Scope       string     `json:"scope"`
	TenantID    *uuid.UUID `json:"tenant_id"`
}

func (h *Handler) writeTokens(w http.ResponseWriter, t *app.Tokens) {
	if t.Refresh != "" {
		http.SetCookie(w, &http.Cookie{
			Name: RefreshCookie, Value: t.Refresh, Path: refreshPath, Expires: t.RefreshExpiresAt,
			MaxAge: int(time.Until(t.RefreshExpiresAt).Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
		})
	}
	jsonapi.Write(w, http.StatusOK, tokenResponse{AccessToken: t.Access, TokenType: "Bearer", ExpiresAt: ts(t.ExpiresAt), Scope: t.Scope, TenantID: t.TenantID})
}

func clearRefresh(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: RefreshCookie, Value: "", Path: refreshPath, MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func refreshValue(r *http.Request) string {
	c, err := r.Cookie(RefreshCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	if body.Username == "" || body.Password == "" || len(body.Username) > 254 || len(body.Password) > 1024 {
		problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("username y password son obligatorios."))
		return
	}
	in := loginInput(r)
	in.Username, in.Password = body.Username, body.Password
	res, err := h.svc.Login(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if res.MFAToken != "" {
		jsonapi.Write(w, http.StatusOK, map[string]any{"mfa_required": true, "mfa_token": res.MFAToken})
		return
	}
	h.writeTokens(w, res.Tokens)
}

func (h *Handler) mfaVerify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	if body.MFAToken == "" || len(body.Code) < 6 || len(body.Code) > 16 {
		problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("mfa_token y code (6–16) son obligatorios."))
		return
	}
	t, err := h.svc.VerifyMFA(r.Context(), body.MFAToken, body.Code, loginInput(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeTokens(w, t)
}

func (h *Handler) issueToken(w http.ResponseWriter, r *http.Request) {
	var p *authz.Principal
	if authz.BearerToken(r) != "" {
		if p = h.guard.Authenticate(w, r); p == nil {
			return
		}
		if p.Type != authz.TypeUser {
			problem.Std(w, r, http.StatusForbidden, problem.CodeKioskForbidden)
			return
		}
	} else if refreshValue(r) == "" {
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeUnauthenticated)
		return
	} else if !h.origins.CheckCookieRequest(w, r) {
		return
	}
	var body struct {
		TenantID *string `json:"tenant_id"`
		Scope    *string `json:"scope"`
	}
	if !jsonapi.Decode(w, r, &body, true) {
		return
	}
	req := app.TokenRequest{IP: clientIP(r)}
	switch {
	case body.TenantID != nil && body.Scope == nil:
		id, err := uuid.Parse(*body.TenantID)
		if err != nil {
			problem.Std(w, r, http.StatusUnprocessableEntity, problem.CodeValidationFailed,
				problem.WithErrors(problem.FieldError{Field: "tenant_id", Code: "INVALID_UUID", Message: "UUID no válido"}))
			return
		}
		req.TenantID = id
	case body.Scope != nil && body.TenantID == nil && *body.Scope == authz.ScopePlatform:
		req.Platform = true
	default:
		problem.Std(w, r, http.StatusUnprocessableEntity, problem.CodeValidationFailed,
			problem.WithDetail(`Envíe {"tenant_id": "<uuid>"} o {"scope": "platform"}.`))
		return
	}
	t, err := h.svc.IssueToken(r.Context(), p, refreshValue(r), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeTokens(w, t)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	if !h.origins.CheckCookieRequest(w, r) {
		return
	}
	t, err := h.svc.Refresh(r.Context(), refreshValue(r), loginInput(r))
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Kind == apperr.KindUnauthorized {
			clearRefresh(w)
		}
		h.fail(w, r, err)
		return
	}
	h.writeTokens(w, t)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if !h.origins.CheckCookieRequest(w, r) {
		return
	}
	if err := h.svc.Logout(r.Context(), refreshValue(r), loginInput(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	clearRefresh(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) reauth(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string  `json:"password"`
		Code     *string `json:"code"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	code := ""
	if body.Code != nil {
		code = *body.Code
	}
	t, err := h.svc.Reauth(r.Context(), authz.FromContext(r.Context()), body.Password, code, loginInput(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeTokens(w, t)
}

type roleAssignmentJSON struct {
	RoleID  uuid.UUID `json:"role_id"`
	RoleKey string    `json:"role_key"`
	Scope   string    `json:"scope"`
}

type membershipJSON struct {
	TenantID             uuid.UUID            `json:"tenant_id"`
	TenantSlug           string               `json:"tenant_slug"`
	TenantName           string               `json:"tenant_name"`
	TenantStatus         string               `json:"tenant_status"`
	Roles                []roleAssignmentJSON `json:"roles"`
	PermissionsWithScope map[string][]string  `json:"permissions_with_scope"`
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.Me(r.Context(), authz.FromContext(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ms := make([]membershipJSON, 0, len(v.Memberships))
	for _, m := range v.Memberships {
		roles := make([]roleAssignmentJSON, 0, len(m.Assignments))
		for _, a := range m.Assignments {
			roles = append(roles, roleAssignmentJSON{RoleID: a.RoleID, RoleKey: a.RoleKey, Scope: a.Scope()})
		}
		ms = append(ms, membershipJSON{TenantID: m.Tenant.ID, TenantSlug: m.Tenant.Slug, TenantName: m.Tenant.Name,
			TenantStatus: m.Tenant.Status, Roles: roles, PermissionsWithScope: m.EffectivePermissions()})
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{
		"id": v.User.ID, "email": v.User.Email, "display_name": v.User.DisplayName, "locale": v.User.Locale,
		"timezone": v.User.Timezone, "default_tenant_id": v.User.DefaultTenantID, "mfa_enabled": v.MFAEnabled,
		"must_change_password": v.User.MustChangePassword, "platform_roles": v.PlatformRoles,
		"platform_permissions": v.PlatformPermissions, "memberships": ms,
	})
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	if err := h.svc.ChangePassword(r.Context(), authz.FromContext(r.Context()), body.Current, body.New, loginInput(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) totpEnroll(w http.ResponseWriter, r *http.Request) {
	e, err := h.svc.EnrollTOTP(r.Context(), authz.FromContext(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]string{"otpauth_uri": e.URI, "secret": e.Secret})
}

func (h *Handler) totpConfirm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	codes, err := h.svc.ConfirmTOTP(r.Context(), authz.FromContext(r.Context()), body.Code, loginInput(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (h *Handler) permissions(w http.ResponseWriter, _ *http.Request) {
	c := h.svc.Catalog()
	perms := make([]map[string]any, 0, len(c.Permissions))
	for _, p := range c.Permissions {
		perms = append(perms, map[string]any{"key": p.Key, "family": p.Family, "description": p.Description,
			"personal_data": p.PII, "audited": p.Audited})
	}
	roles := make([]map[string]any, 0, len(c.TenantRoles)+len(c.PlatformRoles))
	for _, list := range [][]domain.Role{c.TenantRoles, c.PlatformRoles} {
		for _, ro := range list {
			roles = append(roles, map[string]any{"id": ro.ID, "tenant_id": nil, "key": ro.Key, "name": ro.Name,
				"is_system": true, "requires_mfa": ro.RequiresMFA, "permissions": ro.Permissions})
		}
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"version": c.Version, "permissions": perms, "roles": roles})
}

func tenantJSON(t *domain.Tenant) map[string]any {
	return map[string]any{
		"id": t.ID, "version": t.Version, "slug": t.Slug, "name": t.Name, "status": t.Status, "country": t.Country,
		"timezone": t.Timezone, "quotas": t.Quotas, "settings": t.Settings, "support_access_policy": t.SupportAccessPolicy,
		"counts": map[string]int{"members": t.Members}, "created_at": ts(t.CreatedAt), "updated_at": ts(t.UpdatedAt),
	}
}

func (h *Handler) listTenants(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req, err := pagination.ParseRequest(q)
	if err != nil {
		code := problem.CodeValidationFailed
		if err == pagination.ErrInvalidCursor { //nolint:errorlint // centinela propio
			code = problem.CodeInvalidCursor
		}
		problem.Std(w, r, http.StatusBadRequest, code)
		return
	}
	page, err := h.svc.ListTenants(r.Context(), h.cursor, req, q.Get("q"), q.Get("status"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(page.Data))
	for i := range page.Data {
		data = append(data, tenantJSON(&page.Data[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": page.Page})
}

// requireReauth aplica x-reauth (auth_time ≤ 5 min).
func (h *Handler) requireReauth(w http.ResponseWriter, r *http.Request) bool {
	if authz.FromContext(r.Context()).AuthAge(h.now()) > ReauthMaxAge {
		problem.Std(w, r, http.StatusForbidden, problem.CodeReauthRequired)
		return false
	}
	return true
}

func (h *Handler) createTenant(w http.ResponseWriter, r *http.Request) {
	if !h.requireReauth(w, r) {
		return
	}
	if _, err := uuid.Parse(r.Header.Get("Idempotency-Key")); err != nil {
		problem.Std(w, r, http.StatusPreconditionRequired, problem.CodePreconditionRequired,
			problem.WithDetail("Idempotency-Key (UUID) es obligatoria."))
		return
	}
	var in app.TenantInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	t, err := h.svc.CreateTenant(r.Context(), authz.FromContext(r.Context()), in, clientIP(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/platform/tenants/"+t.ID.String())
	w.Header().Set("ETag", jsonapi.ETag(t.Version))
	jsonapi.Write(w, http.StatusCreated, tenantJSON(t))
}

func pathUUID(w http.ResponseWriter, r *http.Request, name, notFoundCode string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, notFoundCode)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) getTenant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "tenant_id", problem.CodeTenantNotFound)
	if !ok {
		return
	}
	t, err := h.svc.GetTenant(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(t.Version))
	jsonapi.Write(w, http.StatusOK, tenantJSON(t))
}

// suspendTenant es POST /platform/tenants/{id}/suspend.
func (h *Handler) suspendTenant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "tenant_id", problem.CodeTenantNotFound)
	if !ok {
		return
	}
	body := struct {
		Reason      string `json:"reason"`
		PauseIngest *bool  `json:"pause_ingest"`
	}{}
	if !jsonapi.Decode(w, r, &body, true) {
		return
	}
	pause := body.PauseIngest == nil || *body.PauseIngest
	t, err := h.svc.SetTenantStatus(r.Context(), authz.FromContext(r.Context()), id, true, body.Reason, pause, clientIP(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(t.Version))
	jsonapi.Write(w, http.StatusOK, tenantJSON(t))
}

// resumeTenant es POST /platform/tenants/{id}/resume.
func (h *Handler) resumeTenant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "tenant_id", problem.CodeTenantNotFound)
	if !ok {
		return
	}
	t, err := h.svc.SetTenantStatus(r.Context(), authz.FromContext(r.Context()), id, false, "", false, clientIP(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(t.Version))
	jsonapi.Write(w, http.StatusOK, tenantJSON(t))
}

func (h *Handler) updateTenant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "tenant_id", problem.CodeTenantNotFound)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var patch map[string]json.RawMessage
	if !jsonapi.Decode(w, r, &patch, false) {
		return
	}
	t, err := h.svc.UpdateTenant(r.Context(), authz.FromContext(r.Context()), id, ver, patch, clientIP(r))
	if err != nil {
		if e, ok := apperr.As(err); ok {
			if cur, ok := e.Current.(*domain.Tenant); ok {
				e.Current = tenantJSON(cur)
			}
		}
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(t.Version))
	jsonapi.Write(w, http.StatusOK, tenantJSON(t))
}
