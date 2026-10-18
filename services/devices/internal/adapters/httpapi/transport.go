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

// TransportHandler sirve PUT /routers/{id}/transport (docs/tunnels.md §3).
type TransportHandler struct {
	svc    *app.Transport
	guard  *authz.Guard
	logger *slog.Logger
}

// NewTransport crea el handler.
func NewTransport(svc *app.Transport, guard *authz.Guard, logger *slog.Logger) *TransportHandler {
	return &TransportHandler{svc: svc, guard: guard, logger: logger}
}

// Mount registra las rutas.
func (h *TransportHandler) Mount(r *httpx.ServiceMux) {
	r.Handle("PUT /api/v1/routers/{router_id}/transport", h.guard.Tenant(app.PermCredentialsWrite, h.put))
}

// put exige re-autenticación (≤ 5 min): cambia cómo viajan la gestión y los
// flujos del router y, sin cifrado por Internet, lo confirma el administrador.
func (h *TransportHandler) put(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	if !freshAuth(w, r) {
		return
	}
	var in app.TransportInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	x, err := h.svc.Set(r.Context(), id, in)
	if err != nil {
		apperr.WriteHTTP(w, r, h.logger, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(x.Version))
	jsonapi.Write(w, http.StatusOK, routerJSON(x))
}
