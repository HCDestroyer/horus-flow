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

// SubnetsHandler sirve las redes de gestión declaradas en un router del
// sitio (switches vía router, ADR-0032).
type SubnetsHandler struct {
	svc    *app.Subnets
	guard  *authz.Guard
	logger *slog.Logger
}

// NewSubnets crea el handler.
func NewSubnets(svc *app.Subnets, guard *authz.Guard, logger *slog.Logger) *SubnetsHandler {
	return &SubnetsHandler{svc: svc, guard: guard, logger: logger}
}

// Mount registra las rutas.
func (h *SubnetsHandler) Mount(r *httpx.ServiceMux) {
	r.Handle("GET /api/v1/routers/{router_id}/management-subnets", h.guard.Tenant("devices.read", h.list))
	r.Handle("POST /api/v1/routers/{router_id}/management-subnets", h.guard.Tenant("devices.update", h.create))
	r.Handle("DELETE /api/v1/routers/{router_id}/management-subnets/{subnet_id}", h.guard.Tenant("devices.update", h.delete))
}

func subnetJSON(m *domain.ManagementSubnet) map[string]any {
	return map[string]any{"id": m.ID, "router_id": m.RouterID, "prefix": m.Prefix.String(), "created_at": ts(m.CreatedAt)}
}

func (h *SubnetsHandler) list(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	rows, err := h.svc.List(r.Context(), id)
	if err != nil {
		apperr.WriteHTTP(w, r, h.logger, err)
		return
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": listJSON(rows, subnetJSON)})
}

func (h *SubnetsHandler) create(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	var in app.SubnetInput
	if !jsonapi.Decode(w, r, &in, false) {
		return
	}
	m, err := h.svc.Create(r.Context(), id, in)
	if err != nil {
		apperr.WriteHTTP(w, r, h.logger, err)
		return
	}
	jsonapi.Write(w, http.StatusCreated, subnetJSON(m))
}

func (h *SubnetsHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	sid, ok := pathID(w, r, "subnet_id", domain.CodeManagementSubnetNotFound)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), id, sid); err != nil {
		apperr.WriteHTTP(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
