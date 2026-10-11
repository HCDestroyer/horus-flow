package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Rutas de cuenta, miembros, roles y usuarios de plataforma del incremento
// I1 (auth.yaml /auth/invitations/accept, PATCH /me, /members*, /roles;
// platform.yaml /platform/users).

func assignmentsJSON(as []domain.Assignment) []roleAssignmentJSON {
	out := make([]roleAssignmentJSON, 0, len(as))
	for _, a := range as {
		out = append(out, roleAssignmentJSON{RoleID: a.RoleID, RoleKey: a.RoleKey, Scope: a.Scope()})
	}
	return out
}

// MemberJSON es Member del contrato.
func MemberJSON(m *domain.Member) map[string]any {
	return map[string]any{
		"user_id": m.UserID, "tenant_id": m.TenantID, "display_name": m.DisplayName, "email": m.Email, "status": m.Status,
		"mfa_enabled": m.MFAEnabled, "role_assignments": assignmentsJSON(m.Assignments), "version": m.Version,
	}
}

func roleJSON(r *domain.TenantRoleRecord) map[string]any {
	perms := r.Permissions
	if perms == nil {
		perms = []string{}
	}
	return map[string]any{"id": r.ID, "tenant_id": r.TenantID, "key": r.Key, "name": r.Name, "is_system": r.IsSystem,
		"requires_mfa": r.RequiresMFA, "permissions": perms}
}

func platformUserJSON(u *domain.PlatformUser) map[string]any {
	ms := make([]map[string]any, 0, len(u.Memberships))
	for _, m := range u.Memberships {
		keys := m.RoleKeys
		if keys == nil {
			keys = []string{}
		}
		ms = append(ms, map[string]any{"tenant_id": m.TenantID, "tenant_slug": m.TenantSlug, "role_keys": keys})
	}
	roles := u.PlatformRoles
	if roles == nil {
		roles = []string{}
	}
	return map[string]any{"id": u.ID, "email": u.Email, "display_name": u.DisplayName, "status": u.Status,
		"mfa_enabled": u.MFAEnabled, "platform_roles": roles, "memberships": ms}
}

// pageRequest lee limit, cursor e include_total (400 si no son válidos).
func pageRequest(w http.ResponseWriter, r *http.Request) (pagination.Request, bool) {
	req, err := pagination.ParseRequest(r.URL.Query())
	if err != nil {
		code := problem.CodeValidationFailed
		if err == pagination.ErrInvalidCursor { //nolint:errorlint // centinela propio
			code = problem.CodeInvalidCursor
		}
		problem.Std(w, r, http.StatusBadRequest, code)
		return req, false
	}
	return req, true
}

func (h *Handler) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var body app.AcceptInvitationInput
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	if err := h.svc.AcceptInvitation(r.Context(), body, loginInput(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	var patch map[string]json.RawMessage
	if !jsonapi.Decode(w, r, &patch, false) {
		return
	}
	if _, err := h.svc.UpdateMe(r.Context(), authz.FromContext(r.Context()), patch, clientIP(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	h.me(w, r)
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	req, ok := pageRequest(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	page, err := h.svc.ListMembers(r.Context(), h.cursor, req, q.Get("q"), q.Get("status"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(page.Data))
	for i := range page.Data {
		data = append(data, MemberJSON(&page.Data[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": page.Page})
}

func (h *Handler) inviteMember(w http.ResponseWriter, r *http.Request) {
	if k := r.Header.Get("Idempotency-Key"); k != "" {
		if _, err := uuid.Parse(k); err != nil {
			problem.Std(w, r, http.StatusBadRequest, problem.CodeValidationFailed, problem.WithDetail("Idempotency-Key debe ser un UUID."))
			return
		}
	}
	var body struct {
		Email           string                `json:"email"`
		RoleAssignments []app.AssignmentInput `json:"role_assignments"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	if err := h.svc.InviteMember(r.Context(), body.Email, body.RoleAssignments, clientIP(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "user_id", problem.CodeNotFound)
	if !ok {
		return
	}
	if err := h.svc.RemoveMember(r.Context(), id, clientIP(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) replaceAssignments(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "user_id", problem.CodeNotFound)
	if !ok {
		return
	}
	if !h.requireReauth(w, r) {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var body json.RawMessage
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	m, err := h.svc.ReplaceAssignments(r.Context(), id, ver, body, clientIP(r))
	if err != nil {
		if e, ok := apperr.As(err); ok {
			if cur, ok := e.Current.(*domain.Member); ok {
				e.Current = MemberJSON(cur)
			}
		}
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(m.Version))
	jsonapi.Write(w, http.StatusOK, assignmentsJSON(m.Assignments))
}

func (h *Handler) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.svc.ListRoles(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(roles))
	for i := range roles {
		data = append(data, roleJSON(&roles[i]))
	}
	limit := len(data)
	if limit == 0 {
		limit = pagination.DefaultLimit
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": pagination.Page{Limit: limit}})
}

func (h *Handler) listPlatformUsers(w http.ResponseWriter, r *http.Request) {
	req, ok := pageRequest(w, r)
	if !ok {
		return
	}
	page, err := h.svc.ListPlatformUsers(r.Context(), h.cursor, req, r.URL.Query().Get("q"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(page.Data))
	for i := range page.Data {
		data = append(data, platformUserJSON(&page.Data[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": data, "page": page.Page})
}
