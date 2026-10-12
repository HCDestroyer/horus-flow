package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// ReauthMaxAge es la antigüedad máxima de auth_time en rutas x-reauth (el
// gateway ya lo exige; se revalida aquí por defensa en profundidad).
const ReauthMaxAge = 5 * time.Minute

// CredentialsHandler sirve PUT /routers/{router_id}/credentials/{kind}:
// credenciales write-only que Horus usa para leer del router.
type CredentialsHandler struct {
	svc    *app.Credentials
	guard  *authz.Guard
	logger *slog.Logger
	now    func() time.Time
}

// NewCredentials crea el handler.
func NewCredentials(svc *app.Credentials, guard *authz.Guard, logger *slog.Logger) *CredentialsHandler {
	return &CredentialsHandler{svc: svc, guard: guard, logger: logger, now: time.Now}
}

// Mount registra la ruta.
func (h *CredentialsHandler) Mount(r *httpx.ServiceMux) {
	r.Handle("PUT /api/v1/routers/{router_id}/credentials/{kind}", h.guard.Tenant(app.PermCredentialsWrite, h.put))
}

func (h *CredentialsHandler) put(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	if authz.FromContext(r.Context()).AuthAge(h.now()) > ReauthMaxAge {
		problem.Std(w, r, http.StatusForbidden, problem.CodeReauthRequired)
		return
	}
	var f app.Fields
	if !jsonapi.Decode(w, r, &f, false) {
		return
	}
	st, err := h.svc.PutCredential(r.Context(), id, r.PathValue("kind"), f)
	clear(f) // el cuerpo lleva el secreto: no se retiene más de lo necesario
	if err != nil {
		apperr.WriteHTTP(w, r, h.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, credentialStatusJSON(st))
}

func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

func credentialStatusJSON(s *app.CredentialStatus) map[string]any {
	return map[string]any{"kind": s.Kind, "configured": s.Configured, "updated_at": tsPtr(s.UpdatedAt),
		"last_used_at": tsPtr(s.LastUsedAt), "last_result": s.LastResult, "tls_fingerprint_sha256": s.TLSFingerprintSHA256}
}
