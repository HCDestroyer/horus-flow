package httpapi

import (
	"net/http"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

// ReauthMaxAge es la antigüedad máxima de auth_time en las rutas x-reauth
// (el gateway ya lo exige; aquí se repite como defensa en profundidad).
const ReauthMaxAge = 5 * time.Minute

// requireReauth aplica x-reauth (auth_time ≤ 5 min).
func (h *Handler) requireReauth(w http.ResponseWriter, r *http.Request) bool {
	if authz.FromContext(r.Context()).AuthAge(h.svc.Now()) > ReauthMaxAge {
		problem.Std(w, r, http.StatusForbidden, problem.CodeReauthRequired)
		return false
	}
	return true
}

func auditMeta(r *http.Request) app.AuditMeta {
	ip := ""
	if a := clientIP(r); a.IsValid() {
		ip = a.String()
	}
	return app.AuditMeta{IP: ip, UserAgent: r.UserAgent(), RequestID: r.Header.Get("X-Request-Id")}
}

func poolJSON(p *app.Pool) map[string]any {
	created := p.CreatedAt
	return map[string]any{"id": p.ID, "cidr": p.CIDR.String(), "addresses_total": p.Total, "addresses_used": p.Used,
		"created_at": ts(&created)}
}

// hubJSON es WireguardHub (platform.yaml): tunnel_cidr es el primer rango,
// por compatibilidad; pools trae todos.
func hubJSON(x *app.Hub) map[string]any {
	cidr := ""
	if len(x.Pools) > 0 {
		cidr = x.Pools[0].String()
	}
	pools := make([]map[string]any, 0, len(x.PoolInfo))
	for i := range x.PoolInfo {
		pools = append(pools, poolJSON(&x.PoolInfo[i]))
	}
	return map[string]any{
		"id": x.ID, "name": x.Name, "endpoint": x.Endpoint, "listen_port": x.ListenPort, "public_key": x.PublicKey,
		"status": x.Status, "tunnel_cidr": cidr, "services_cidr": x.ServicesCIDR.String(), "addresses_total": x.AddressesTotal,
		"addresses_used": x.AddressesUsed, "peers_active": x.PeersActive, "tenants": x.Tenants, "version": x.Version,
		"mtu": x.MTU, "persistent_keepalive_seconds": x.Keepalive, "pools": pools,
	}
}

func (h *Handler) listHubs(w http.ResponseWriter, r *http.Request) {
	hubs, err := h.svc.ListHubs(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(hubs))
	for i := range hubs {
		out = append(out, hubJSON(&hubs[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": out})
}

func (h *Handler) updateHub(w http.ResponseWriter, r *http.Request) {
	if !h.requireReauth(w, r) {
		return
	}
	id, ok := pathID(w, r, "hub_id", domain.CodeHubNotFound)
	if !ok {
		return
	}
	ver, err := jsonapi.IfMatch(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in app.HubPatch
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	hub, err := h.svc.UpdateHub(r.Context(), id, ver, in, auditMeta(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(hub.Version))
	jsonapi.Write(w, http.StatusOK, hubJSON(hub))
}

func (h *Handler) addPool(w http.ResponseWriter, r *http.Request) {
	if !h.requireReauth(w, r) {
		return
	}
	id, ok := pathID(w, r, "hub_id", domain.CodeHubNotFound)
	if !ok {
		return
	}
	var in app.PoolInput
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	pool, err := h.svc.AddPool(r.Context(), id, in, auditMeta(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	jsonapi.Write(w, http.StatusCreated, poolJSON(pool))
}

func (h *Handler) deletePool(w http.ResponseWriter, r *http.Request) {
	if !h.requireReauth(w, r) {
		return
	}
	hubID, ok := pathID(w, r, "hub_id", domain.CodeHubNotFound)
	if !ok {
		return
	}
	poolID, ok := pathID(w, r, "pool_id", domain.CodePoolNotFound)
	if !ok {
		return
	}
	if err := h.svc.DeletePool(r.Context(), hubID, poolID, auditMeta(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
