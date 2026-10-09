package app

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/script"
)

// ProvisioningInput es el cuerpo de POST /routers/{id}/provisioning-script.
type ProvisioningInput struct {
	RouterOSVersion *string `json:"routeros_version"`
	NTPServer       *string `json:"ntp_server"`
}

// Provisioning es el script generado con su token.
type Provisioning struct {
	Script         string
	TokenID        uuid.UUID
	TokenExpiresAt time.Time
}

func errUnsupported() error {
	return apperr.New(apperr.KindInvalid, domain.CodeRouterOSTooOld, "RouterOS v7 ≥ 7.12 requerido")
}

// CreateProvisioningScript genera el script de alta (I1-02): asegura el
// peer, invalida los tokens y credenciales anteriores, crea un token nuevo
// de 24 h y credenciales nuevas (que solo aparecen en este script) y lo
// audita.
func (s *Service) CreateProvisioningScript(ctx context.Context, routerID uuid.UUID, in ProvisioningInput) (*Provisioning, error) {
	p, t, r, err := s.routerInScope(ctx, "wireguard.write", routerID)
	if err != nil {
		return nil, err
	}
	version := strOr(in.RouterOSVersion, strOr(r.RouterOSVersion, ""))
	tpl, err := script.TemplateFor(version)
	if err != nil {
		return nil, errUnsupported()
	}
	ntp := strOr(in.NTPServer, s.o.NTPServer)
	if len(ntp) > 253 || strings.ContainsAny(ntp, " \"';$[]{}\\") {
		return nil, apperr.Validation(apperr.Field("ntp_server", "INVALID_VALUE", "Nombre DNS o IP."))
	}
	hubKey := s.o.HubPublicKey()
	if hubKey == "" {
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "El hub WireGuard aún no tiene clave pública (wg-agent).")
	}
	peer, err := s.EnsurePeer(ctx, r)
	if errors.Is(err, domain.ErrPoolExhausted) {
		return nil, apperr.Conflict(domain.CodePoolExhausted, "No quedan IPs de túnel libres: el operador de plataforma debe añadir un rango.")
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	plain, hash, err := domain.NewToken()
	if err != nil {
		return nil, err
	}
	tok := &domain.Token{ID: uuid.Must(uuid.NewV7()), TenantID: t.UUID(), RouterID: r.ID, PeerID: peer.ID, Hash: hash,
		ExpiresAt: now.Add(domain.TokenTTL), CreatedAt: now}
	d, err := s.devices()
	if err != nil {
		return nil, err
	}
	creds, err := d.IssueCredentials(ctx, t.UUID(), r.ID)
	if err != nil {
		return nil, err
	}
	if err := s.o.Store.IssueToken(ctx, t, tok); err != nil {
		return nil, err
	}
	text, err := script.Onboarding(script.Values{
		Template: tpl, RouterID: r.ID.String(), RouterName: r.Name, RouterWGIP: peer.Address, HubPublicKey: hubKey,
		WGEndpoint: s.o.Hub.Endpoint, WGPort: s.o.Hub.ListenPort, ServicesCIDR: s.o.Hub.ServicesCIDR, CollectorIP: s.o.CollectorIP,
		APIUser: creds.APIUser, APIPassword: creds.APIPassword, SNMPUser: creds.SNMPUser, SNMPAuthPass: creds.SNMPAuthPassword,
		SNMPPrivPass: creds.SNMPPrivPassword, NTPServer: ntp, EnrollURL: strings.TrimRight(s.o.PublicBaseURL, "/") + "/api/v1/enroll/wireguard",
		Token: plain, CacheEntries: s.o.CacheEntries, AccessMode: s.o.AccessMode, CACertPEM: s.o.CACertPEM,
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, userAudit(p, t, "wireguard.provisioning_script.created", "router", r.ID.String(),
		map[string]any{"enrollment_token_id": tok.ID.String(), "template": tpl, "credentials_rotated": true}))
	return &Provisioning{Script: text, TokenID: tok.ID, TokenExpiresAt: tok.ExpiresAt}, nil
}

// CreateDeprovisioningScript genera el script inverso (no cambia nada en Horus).
func (s *Service) CreateDeprovisioningScript(ctx context.Context, routerID uuid.UUID) (string, error) {
	p, t, r, err := s.routerInScope(ctx, "wireguard.write", routerID)
	if err != nil {
		return "", err
	}
	var addr netip.Addr
	if peer, err := s.o.Store.ActivePeer(ctx, t, r.ID); err == nil {
		addr = peer.Address
	} else if r.TunnelAddress != nil {
		addr = *r.TunnelAddress
	}
	id := r.ID.String()
	text, err := script.Deprovisioning(script.DeprovisionValues{RouterID: id, RouterName: r.Name, RouterWGIP: addr,
		CollectorIP: s.o.CollectorIP, SNMPUser: "horus-" + id[len(id)-8:]})
	if err != nil {
		return "", err
	}
	s.audit(ctx, userAudit(p, t, "wireguard.deprovisioning_script.created", "router", id, nil))
	return text, nil
}

// RevokeEnrollmentToken revoca un token pendiente.
func (s *Service) RevokeEnrollmentToken(ctx context.Context, id uuid.UUID) error {
	p, t, err := scope(ctx, "wireguard.write")
	if err != nil {
		return err
	}
	if err := s.o.Store.RevokeToken(ctx, t, id, s.now()); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound(problem.CodeNotFound)
		}
		return err
	}
	s.audit(ctx, userAudit(p, t, "wireguard.enrollment_token.revoked", "enrollment_token", id.String(), nil))
	return nil
}

// EnrollInput es el cuerpo de POST /enroll/wireguard.
type EnrollInput struct {
	Token     string
	PublicKey string
	IP        netip.Addr
	UserAgent string
}

// Enroll registra la clave pública del router (público, sin sesión). El
// token se identifica por su hash; usado, caducado, revocado o desconocido
// responden igual (ENROLLMENT_TOKEN_INVALID). Una clave inválida o ya
// registrada cuenta como fallo del token (5 fallos lo invalidan).
func (s *Service) Enroll(ctx context.Context, in EnrollInput) error {
	if l := len(in.Token); l < 43 || l > 64 {
		return apperr.New(apperr.KindInvalid, domain.CodeTokenInvalid, "")
	}
	now := s.now()
	var tokID uuid.UUID
	peer, err := s.o.Store.Enroll(ctx, domain.HashToken(in.Token), in.PublicKey, in.IP, now, func(p *domain.Peer, tok *domain.Token) []outbox.Event {
		tokID = tok.ID
		return []outbox.Event{peerEvent("horus.wireguard.peer.enrolled", p, outbox.Actor{Type: "service", ID: "svc:wireguard"}, now,
			PeerEventData(p, []string{"public_key", "status"}))}
	})
	switch {
	case errors.Is(err, ErrBadKey):
		return apperr.Validation(apperr.Field("public_key", "INVALID_FORMAT", "Clave pública WireGuard (44 caracteres base64)."))
	case errors.Is(err, ErrKeyInUse):
		return apperr.Conflict(domain.CodePublicKeyInUse, "La clave pública ya está registrada en otro router.")
	case errors.Is(err, ErrTokenInvalid):
		return apperr.New(apperr.KindInvalid, domain.CodeTokenInvalid, "")
	case err != nil:
		return err
	}
	s.audit(ctx, authapi.AuditEntry{TenantID: peer.TenantID, ActorType: "system", ActorID: "enrollment_token:" + tokID.String(),
		Action: "wireguard.peer.enrolled", ResourceType: "router", ResourceID: peer.RouterID.String(), Outcome: "success",
		IP: in.IP.String(), UserAgent: in.UserAgent, Changes: map[string]any{"peer_id": peer.ID.String(), "public_key": *peer.PublicKey}})
	if d, ok := s.o.Devices(); ok {
		if err := d.SetTunnel(ctx, peer.TenantID, peer.RouterID, devTunnel(peer)); err != nil {
			s.o.Logger.WarnContext(ctx, "project enrollment on router failed", errAttr(err))
		}
	}
	s.Kick() // el agente añade el peer en segundos
	return nil
}
