// Package httpapi es el adaptador REST del módulo wireguard
// (packages/schemas/openapi/v0/wireguard.yaml y platform.yaml
// /platform/wireguard/hubs).
package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/httpx"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/pagination"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/app"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

// Handler sirve las rutas del módulo.
type Handler struct {
	svc    *app.Service
	guard  *authz.Guard
	cursor *pagination.Codec
	logger *slog.Logger
}

// New crea el handler.
func New(svc *app.Service, guard *authz.Guard, cursor *pagination.Codec, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Handler{svc: svc, guard: guard, cursor: cursor, logger: logger}
}

// Mount registra las rutas.
func (h *Handler) Mount(r *httpx.ServiceMux) {
	r.Handle("POST /api/v1/routers/{router_id}/provisioning-script", h.guard.Tenant("wireguard.write", h.provisioning))
	r.Handle("POST /api/v1/routers/{router_id}/deprovisioning-script", h.guard.Tenant("wireguard.write", h.deprovisioning))
	r.Handle("POST /api/v1/wireguard/enrollment-tokens/{token_id}/revoke", h.guard.Tenant("wireguard.write", h.revokeToken))
	r.Handle("GET /api/v1/wireguard/peers", h.guard.Tenant("wireguard.read", h.listPeers))
	r.Handle("GET /api/v1/wireguard/peers/{peer_id}", h.guard.Tenant("wireguard.read", h.getPeer))
	r.Handle("GET /api/v1/platform/wireguard/hubs", h.guard.Platform("platform.status.read", h.listHubs))
	r.HandleFunc("POST /api/v1/enroll/wireguard", h.enroll)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	apperr.WriteHTTP(w, r, h.logger, err)
}

func pathID(w http.ResponseWriter, r *http.Request, name, code string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		problem.Std(w, r, http.StatusNotFound, code)
		return uuid.Nil, false
	}
	return id, true
}

func ts(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func writeText(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, text)
}

func (h *Handler) provisioning(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	var in app.ProvisioningInput
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, jsonapi.MaxBody))
	if err != nil {
		problem.Std(w, r, http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge)
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !jsonapi.Decode(w, r, &in, false) {
			return
		}
	}
	res, err := h.svc.CreateProvisioningScript(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("X-Horus-Enrollment-Token-Id", res.TokenID.String())
	w.Header().Set("X-Horus-Enrollment-Token-Expires-At", res.TokenExpiresAt.UTC().Format("2006-01-02T15:04:05.000Z"))
	w.Header().Set("Content-Disposition", `attachment; filename="horus-onboarding-`+id.String()+`.rsc"`)
	writeText(w, http.StatusCreated, res.Script)
}

func (h *Handler) deprovisioning(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "router_id", domain.CodeRouterNotFound)
	if !ok {
		return
	}
	text, err := h.svc.CreateDeprovisioningScript(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="horus-deprovisioning-`+id.String()+`.rsc"`)
	writeText(w, http.StatusOK, text)
}

func (h *Handler) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "token_id", problem.CodeNotFound)
	if !ok {
		return
	}
	if err := h.svc.RevokeEnrollmentToken(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func clientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

// enroll es público (sin sesión): el token del cuerpo es la credencial. El
// rate limit por IP lo aplica el gateway (x-rate-limit: enroll, 10/min).
func (h *Handler) enroll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token     *string `json:"token"`
		PublicKey *string `json:"public_key"`
	}
	if !jsonapi.Decode(w, r, &in, true) {
		return
	}
	if in.Token == nil || in.PublicKey == nil {
		var fields []problem.FieldError
		if in.Token == nil {
			fields = append(fields, apperr.Field("token", "REQUIRED", "Obligatorio."))
		}
		if in.PublicKey == nil {
			fields = append(fields, apperr.Field("public_key", "REQUIRED", "Obligatorio."))
		}
		h.fail(w, r, apperr.Validation(fields...))
		return
	}
	err := h.svc.Enroll(r.Context(), app.EnrollInput{Token: *in.Token, PublicKey: *in.PublicKey, IP: clientIP(r), UserAgent: r.UserAgent()})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusAccepted, map[string]string{"peer_status": domain.StatusPendingHandshake})
}

// ------------------------------------------------------------------ peers

func (h *Handler) peerJSON(p *domain.Peer) map[string]any {
	enr := map[string]any{"token_id": nil, "token_state": "none", "token_expires_at": nil, "enrolled_at": ts(p.EnrolledAt)}
	if p.Token != nil {
		exp := p.Token.ExpiresAt
		enr["token_id"], enr["token_state"], enr["token_expires_at"] = p.Token.ID, p.Token.State(h.svc.Now()), ts(&exp)
	}
	hs := p.HandshakeState
	if p.Status != domain.StatusRevoked && p.LastHandshakeAt != nil {
		hs = h.svc.HandshakeState(p)
	}
	return map[string]any{
		"id": p.ID, "tenant_id": p.TenantID, "version": p.Version, "created_at": ts(&p.CreatedAt), "updated_at": ts(&p.UpdatedAt),
		"server_id": p.ServerID, "router_id": p.RouterID, "status": p.Status, "handshake_state": hs, "address": p.AllowedIP(),
		"public_key": p.PublicKey, "last_handshake_at": ts(p.LastHandshakeAt), "endpoint": p.Endpoint,
		"rx_bytes": strconv.FormatUint(p.RxBytes, 10), "tx_bytes": strconv.FormatUint(p.TxBytes, 10),
		"persistent_keepalive_seconds": p.Keepalive, "enrollment": enr,
	}
}

func (h *Handler) listPeers(w http.ResponseWriter, r *http.Request) {
	page, err := h.svc.ListPeers(r.Context(), r.URL.Query(), h.cursor)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(page.Data))
	for i := range page.Data {
		out = append(out, h.peerJSON(&page.Data[i]))
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": out, "page": page.Page})
}

func (h *Handler) getPeer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "peer_id", problem.CodeNotFound)
	if !ok {
		return
	}
	p, err := h.svc.GetPeer(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", jsonapi.ETag(p.Version))
	jsonapi.Write(w, http.StatusOK, h.peerJSON(p))
}

func (h *Handler) listHubs(w http.ResponseWriter, r *http.Request) {
	hubs, err := h.svc.ListHubs(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(hubs))
	for _, x := range hubs {
		cidr := ""
		if len(x.Pools) > 0 {
			cidr = x.Pools[0].String()
		}
		out = append(out, map[string]any{
			"id": x.ID, "name": x.Name, "endpoint": x.Endpoint, "listen_port": x.ListenPort, "public_key": x.PublicKey,
			"status": x.Status, "tunnel_cidr": cidr, "services_cidr": x.ServicesCIDR.String(), "addresses_total": x.AddressesTotal,
			"addresses_used": x.AddressesUsed, "peers_active": x.PeersActive, "tenants": x.Tenants,
		})
	}
	jsonapi.Write(w, http.StatusOK, map[string]any{"data": out})
}
