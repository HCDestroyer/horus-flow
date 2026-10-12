package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// Roles propios del ISP (auth.yaml POST /roles, PATCH y DELETE
// /roles/{role_id}) y gestión de usuarios de plataforma (platform.yaml PATCH
// /platform/users/{user_id}, PUT /platform/users/{user_id}/platform-roles).

// ifMatchOpt lee If-Match: nil si falta (el caso de uso decide el 428 tras el
// 404 y el ROLE_READ_ONLY), -1 si no es un ETag de versión (412).
func ifMatchOpt(r *http.Request) *int {
	v, err := jsonapi.IfMatch(r)
	if err != nil {
		return nil
	}
	return &v
}

// roleFail escribe un error de roles con el rol actual (412) como Role.
func (h *Handler) roleFail(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok {
		if cur, ok := e.Current.(*domain.TenantRoleRecord); ok {
			e.Current = roleJSON(cur)
		}
	}
	h.fail(w, r, err)
}

func (h *Handler) createRole(w http.ResponseWriter, r *http.Request) {
	var in app.RoleInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	ro, err := h.svc.CreateRole(r.Context(), in, clientIP(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.SetLocation(w, h.baseURL, "/api/v1/roles/"+ro.ID.String())
	w.Header().Set("ETag", jsonapi.ETag(ro.Version))
	jsonapi.Write(w, http.StatusCreated, roleJSON(ro))
}

func (h *Handler) updateRole(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "role_id", app.CodeRoleNotFound)
	if !ok {
		return
	}
	var patch map[string]json.RawMessage
	if !jsonapi.Decode(w, r, &patch, false) {
		return
	}
	ro, err := h.svc.UpdateRole(r.Context(), id, ifMatchOpt(r), patch, clientIP(r))
	if err != nil {
		h.roleFail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(ro.Version))
	jsonapi.Write(w, http.StatusOK, roleJSON(ro))
}

func (h *Handler) deleteRole(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "role_id", app.CodeRoleNotFound)
	if !ok {
		return
	}
	if err := h.svc.DeleteRole(r.Context(), id, ifMatchOpt(r), clientIP(r)); err != nil {
		h.roleFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) updatePlatformUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "user_id", problem.CodeNotFound)
	if !ok {
		return
	}
	if !h.requireReauth(w, r) {
		return
	}
	var body map[string]json.RawMessage
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	u, err := h.svc.SetUserStatus(r.Context(), id, body, clientIP(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, platformUserJSON(u))
}

func (h *Handler) setPlatformRoles(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "user_id", problem.CodeNotFound)
	if !ok {
		return
	}
	if !h.requireReauth(w, r) {
		return
	}
	var body struct {
		PlatformRoles *[]string `json:"platform_roles"`
	}
	if !jsonapi.Decode(w, r, &body, true) {
		return
	}
	var roles []string
	if body.PlatformRoles != nil {
		roles = *body.PlatformRoles
	}
	u, err := h.svc.ReplacePlatformRoles(r.Context(), id, roles, body.PlatformRoles != nil, clientIP(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, platformUserJSON(u))
}
