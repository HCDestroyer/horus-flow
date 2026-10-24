package app

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/script"
)

// ProvisioningInput es el cuerpo de POST /routers/{id}/provisioning-script.
type ProvisioningInput struct {
	RouterOSVersion *string `json:"routeros_version"`
	NTPServer       *string `json:"ntp_server"`
	// Enrollment: fetch (por defecto) — el router envía su clave pública al
	// HTTPS de Horus con el token; manual — el script solo la muestra y se
	// pega en la consola (instalaciones sin el 443 publicado).
	Enrollment *string `json:"enrollment"`
}

// Modos de alta del script (ProvisioningInput.Enrollment).
const (
	EnrollmentFetch  = "fetch"
	EnrollmentManual = "manual"
)

// Provisioning es el script generado con su token.
type Provisioning struct {
	Script         string
	TokenID        uuid.UUID
	TokenExpiresAt time.Time
}

func errUnsupported() error {
	return apperr.New(apperr.KindInvalid, domain.CodeRouterOSTooOld, "RouterOS v7 ≥ 7.12 requerido")
}

// CodeVendorNotSupported: el script RouterOS y el túnel WireGuard solo
// existen para MikroTik (Huawei VRP no tiene WireGuard: modo directo,
// docs/vendors/huawei.md §5).
const CodeVendorNotSupported = "ROUTER_VENDOR_NOT_SUPPORTED"

// CodeConnectionModeMismatch: la operación es de otro transporte (p. ej. el
// script WireGuard para un MikroTik que se conecta por IPsec).
const CodeConnectionModeMismatch = "CONNECTION_MODE_MISMATCH"

func checkWireGuardVendor(r devapi.RouterInfo) error {
	if r.Vendor != "" && r.Vendor != devapi.VendorMikroTik {
		return apperr.New(apperr.KindInvalid, CodeVendorNotSupported,
			"El túnel WireGuard y el script RouterOS solo existen para MikroTik.")
	}
	if r.ConnectionMode != "" && r.ConnectionMode != devapi.ConnectionWireGuard {
		return apperr.New(apperr.KindInvalid, CodeConnectionModeMismatch,
			"Este router no usa WireGuard (transporte "+r.ConnectionMode+"): genere su configuración de túnel o cambie el transporte.")
	}
	return nil
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
	if err := checkWireGuardVendor(r); err != nil {
		return nil, err
	}
	version := strOr(in.RouterOSVersion, strOr(r.RouterOSVersion, ""))
	tpl, err := script.TemplateFor(version)
	if err != nil {
		return nil, errUnsupported()
	}
	mode := strOr(in.Enrollment, EnrollmentFetch)
	if mode != EnrollmentFetch && mode != EnrollmentManual {
		return nil, apperr.Validation(apperr.Field("enrollment", "INVALID_VALUE", "fetch o manual."))
	}
	manual := mode == EnrollmentManual
	ntp := strOr(in.NTPServer, s.o.NTPServer)
	if len(ntp) > 253 || strings.ContainsAny(ntp, " \"';$[]{}\\") {
		return nil, apperr.Validation(apperr.Field("ntp_server", "INVALID_VALUE", "Nombre DNS o IP."))
	}
	hubKey := s.o.HubPublicKey()
	if hubKey == "" {
		return nil, apperr.New(apperr.KindUnavailable, problem.CodeServiceUnavailable, "El hub WireGuard aún no tiene clave pública (wg-agent).")
	}
	// Endpoint, MTU y keepalive del hub (editables desde la consola).
	hub, err := s.o.Store.GetHub(ctx, s.o.Hub.ID)
	if err != nil {
		return nil, fmt.Errorf("hub: %w", err)
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
	enrollURL, scriptToken, caPEM := strings.TrimRight(s.o.PublicBaseURL, "/")+"/api/v1/enroll/wireguard", plain, s.o.CACertPEM
	if manual { // el router no llama a Horus: ni URL, ni token, ni certificado en el script
		enrollURL, scriptToken, caPEM = "", "", ""
	}
	text, err := script.Onboarding(script.Values{
		Template: tpl, RouterID: r.ID.String(), RouterName: r.Name, RouterWGIP: peer.Address, HubPublicKey: hubKey,
		WGEndpoint: hub.Endpoint, WGPort: hub.ListenPort, MTU: hub.MTU, Keepalive: hub.Keepalive,
		ServicesCIDR: s.o.Hub.ServicesCIDR, CollectorIP: s.o.CollectorIP,
		APIUser: creds.APIUser, APIPassword: creds.APIPassword, SNMPUser: creds.SNMPUser, SNMPAuthPass: creds.SNMPAuthPassword,
		SNMPPrivPass: creds.SNMPPrivPassword, NTPServer: ntp, EnrollURL: enrollURL,
		Token: scriptToken, CacheEntries: s.o.CacheEntries, AccessMode: s.o.AccessMode, CACertPEM: caPEM, ManualEnroll: manual,
	})
	if err != nil {
		return nil, err
	}
	s.audit(ctx, userAudit(p, t, "wireguard.provisioning_script.created", "router", r.ID.String(),
		map[string]any{"enrollment_token_id": tok.ID.String(), "template": tpl, "credentials_rotated": true, "enrollment": mode}))
	return &Provisioning{Script: text, TokenID: tok.ID, TokenExpiresAt: tok.ExpiresAt}, nil
}

// CreateDeprovisioningScript genera el script inverso (no cambia nada en Horus).
func (s *Service) CreateDeprovisioningScript(ctx context.Context, routerID uuid.UUID) (string, error) {
	p, t, r, err := s.routerInScope(ctx, "wireguard.write", routerID)
	if err != nil {
		return "", err
	}
	if err := checkWireGuardVendor(r); err != nil {
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
	return s.enroll(ctx, domain.HashToken(in.Token), in, svcActor, nil)
}

// EnrollManualInput es el cuerpo de POST /wireguard/enrollment-tokens/{id}/enroll.
type EnrollManualInput struct {
	PublicKey string
	IP        netip.Addr
	UserAgent string
}

// EnrollManual registra la clave pública que la persona copió del router
// (alta manual: el router no llega al HTTPS de Horus). Mismas reglas que
// Enroll —token pendiente, de un uso, 5 fallos lo invalidan, clave única—,
// pero con sesión y permiso wireguard.write en el ISP del token.
func (s *Service) EnrollManual(ctx context.Context, tokenID uuid.UUID, in EnrollManualInput) error {
	p, t, err := scope(ctx, "wireguard.write")
	if err != nil {
		return err
	}
	hash, err := s.o.Store.TokenHash(ctx, t, tokenID)
	if errors.Is(err, domain.ErrNotFound) {
		return apperr.New(apperr.KindInvalid, domain.CodeTokenInvalid, "")
	}
	if err != nil {
		return err
	}
	actor := outbox.Actor{Type: p.Type, ID: p.Subject, ViaPlatform: p.ViaPlatform}
	user := userAudit(p, t, "", "", "", nil)
	return s.enroll(ctx, hash, EnrollInput{PublicKey: strings.TrimSpace(in.PublicKey), IP: in.IP, UserAgent: in.UserAgent}, actor, &user)
}

// enroll registra la clave con el token de hash dado. user es la entrada de
// auditoría del usuario en el alta manual (nil: el router con su token).
func (s *Service) enroll(ctx context.Context, hash []byte, in EnrollInput, actor outbox.Actor, user *authapi.AuditEntry) error {
	now := s.now()
	var tokID uuid.UUID
	peer, err := s.o.Store.Enroll(ctx, hash, in.PublicKey, in.IP, now, func(p *domain.Peer, tok *domain.Token) []outbox.Event {
		tokID = tok.ID
		return []outbox.Event{peerEvent("horus.wireguard.peer.enrolled", p, actor, now,
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
	entry := authapi.AuditEntry{TenantID: peer.TenantID, ActorType: "system", ActorID: "enrollment_token:" + tokID.String(),
		Action: "wireguard.peer.enrolled", ResourceType: "router", ResourceID: peer.RouterID.String(), Outcome: "success",
		IP: in.IP.String(), UserAgent: in.UserAgent, Changes: map[string]any{"peer_id": peer.ID.String(), "public_key": *peer.PublicKey}}
	if user != nil { // alta manual: la hace una persona con sesión
		entry.ActorType, entry.ActorID, entry.ViaPlatform = user.ActorType, user.ActorID, user.ViaPlatform
		entry.Changes["enrollment"], entry.Changes["enrollment_token_id"] = EnrollmentManual, tokID.String()
	}
	s.audit(ctx, entry)
	if d, ok := s.o.Devices(); ok {
		if err := d.SetTunnel(ctx, peer.TenantID, peer.RouterID, devTunnel(peer)); err != nil {
			s.o.Logger.WarnContext(ctx, "project enrollment on router failed", errAttr(err))
		}
	}
	s.Kick() // el agente añade el peer en segundos
	return nil
}
