package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/app"
)

// Cuentas: recuperación de contraseña (público), edición y restablecimiento
// desde un ISP (/members/{user_id}) y desde la plataforma (/platform/users).

func (h *Handler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	delivery, err := h.svc.ForgotPassword(r.Context(), body.Email, loginInput(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusAccepted, map[string]any{"email_delivery": delivery})
}

func (h *Handler) inspectPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	info, err := h.svc.InspectPasswordReset(r.Context(), body.Token)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, map[string]any{"email": info.Email, "display_name": info.DisplayName, "expires_at": ts(info.ExpiresAt)})
}

func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if !jsonapi.Decode(w, r, &body, false) {
		return
	}
	if err := h.svc.ResetPassword(r.Context(), body.Token, body.NewPassword, loginInput(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// passwordResetIssuedJSON es PasswordResetIssued del contrato (lleva el
// enlace con el token: no se cachea).
func passwordResetIssuedJSON(res *app.PasswordResetIssued) map[string]any {
	return map[string]any{"user_id": res.UserID, "reset_url": res.URL, "expires_at": ts(res.ExpiresAt), "email_sent": res.EmailSent}
}

func (h *Handler) updateMember(w http.ResponseWriter, r *http.Request) {
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
	m, err := h.svc.UpdateMember(r.Context(), id, body, clientIP(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(m.Version))
	jsonapi.Write(w, http.StatusOK, MemberJSON(m))
}

func (h *Handler) resetMemberPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "user_id", problem.CodeNotFound)
	if !ok {
		return
	}
	if !h.requireReauth(w, r) {
		return
	}
	res, err := h.svc.ResetMemberPassword(r.Context(), id, clientIP(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, passwordResetIssuedJSON(res))
}

func (h *Handler) resetPlatformUserPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "user_id", problem.CodeNotFound)
	if !ok {
		return
	}
	if !h.requireReauth(w, r) {
		return
	}
	res, err := h.svc.ResetUserPassword(r.Context(), id, clientIP(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, passwordResetIssuedJSON(res))
}

func (h *Handler) deletePlatformUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "user_id", problem.CodeNotFound)
	if !ok {
		return
	}
	if !h.requireReauth(w, r) {
		return
	}
	if err := h.svc.DeleteUser(r.Context(), id, clientIP(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
