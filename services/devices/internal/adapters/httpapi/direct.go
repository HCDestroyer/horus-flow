package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/app"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// DirectHandler sirve el alta en modo directo (Huawei VRP, sin túnel).
type DirectHandler struct {
	svc    *app.DirectOnboarding
	guard  *authz.Guard
	logger *slog.Logger
}

// NewDirect crea el handler.
func NewDirect(svc *app.DirectOnboarding, guard *authz.Guard, logger *slog.Logger) *DirectHandler {
	return &DirectHandler{svc: svc, guard: guard, logger: logger}
}

// Mount registra las rutas.
func (h *DirectHandler) Mount(r *httpx.ServiceMux) {
	r.Handle("POST /api/v1/routers/{router_id}/direct-onboarding", h.guard.Tenant(app.PermCredentialsWrite, h.create))
}

// create es POST /routers/{id}/direct-onboarding (re-autenticación: emite
// una credencial SNMPv3 que solo aparece en esta respuesta).
func (h *DirectHandler) create(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	if !freshAuth(w, r) {
		return
	}
	var in app.DirectOnboardingInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	res, err := h.svc.Create(r.Context(), id, in)
	if err != nil {
		apperr.WriteHTTP(w, r, h.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusCreated, map[string]any{
		"router_id": res.RouterID, "vendor": res.Vendor, "language": "vrp", "management_address": res.ManagementAddress,
		"collector_address": res.CollectorAddress, "collector_port": res.CollectorPort, "snmp_user": res.SNMPUser,
		"sampling_rate": res.SamplingRate, "config": res.Config, "notes": res.Notes, "placeholders": res.Placeholders,
		"warnings": res.Warnings,
	})
}
