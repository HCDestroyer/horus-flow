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

// TLSPinHandler sirve la huella TLS del router: estado, captura (TOFU,
// queda pendiente) y confirmación explícita de un administrador.
type TLSPinHandler struct {
	svc    *app.TLSPins
	guard  *authz.Guard
	logger *slog.Logger
	now    func() time.Time
}

// NewTLSPins crea el handler.
func NewTLSPins(svc *app.TLSPins, guard *authz.Guard, logger *slog.Logger) *TLSPinHandler {
	return &TLSPinHandler{svc: svc, guard: guard, logger: logger, now: time.Now}
}

// Mount registra las rutas.
func (h *TLSPinHandler) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/routers/{router_id}/tls-fingerprint", h.guard.Tenant(app.PermTLSPinRead, h.get))
	r.Handle("POST /api/v1/routers/{router_id}/tls-fingerprint/capture", h.guard.Tenant(app.PermTLSPinCapture, h.capture))
	r.Handle("POST /api/v1/routers/{router_id}/tls-fingerprint/confirm", h.guard.Tenant(app.PermTLSPinConfirm, h.confirm))
}

func tlsPinJSON(v *app.TLSPinView) map[string]any {
	return map[string]any{"router_id": v.RouterID, "status": v.Status, "pinned_sha256": v.PinnedSHA256, "observed_sha256": v.ObservedSHA256,
		"observed_at": tsPtr(v.ObservedAt), "confirmed_at": tsPtr(v.ConfirmedAt), "confirmed_by": v.ConfirmedBy, "last_result": v.LastResult,
		"last_used_at": tsPtr(v.LastUsedAt)}
}

func (h *TLSPinHandler) write(w http.ResponseWriter, r *http.Request, v *app.TLSPinView, err error) {
	if err != nil {
		apperr.WriteHTTP(w, r, h.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusOK, tlsPinJSON(v))
}

func (h *TLSPinHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	v, err := h.svc.Get(r.Context(), id)
	h.write(w, r, v, err)
}

func (h *TLSPinHandler) capture(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	v, err := h.svc.Capture(r.Context(), id)
	h.write(w, r, v, err)
}

func (h *TLSPinHandler) confirm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	if authz.FromContext(r.Context()).AuthAge(h.now()) > ReauthMaxAge {
		problem.Std(w, r, http.StatusForbidden, problem.CodeReauthRequired)
		return
	}
	var in struct {
		Fingerprint string `json:"fingerprint_sha256"`
	}
	if !jsonapi.Decode(w, r, &in, false) {
		return
	}
	v, err := h.svc.Confirm(r.Context(), id, in.Fingerprint)
	h.write(w, r, v, err)
}
