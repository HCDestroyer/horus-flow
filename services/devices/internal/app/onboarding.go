package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// OnboardingStore es el repositorio del alta de routers (I1-01/I1-02).
type OnboardingStore interface {
	GetRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
	// ListAllRouters: routers activos de todos los tenants (rol de plataforma).
	ListAllRouters(ctx context.Context) ([]domain.Router, error)
	// SetRouterTunnel aplica fn al router bloqueado; si fn devuelve true lo
	// guarda (version+1) y emite los eventos.
	SetRouterTunnel(ctx context.Context, t pgdb.TenantID, id uuid.UUID, fn func(r *domain.Router) bool, ev Events[domain.Router]) error
	// PutCredentials sustituye (baja lógica + alta) las credenciales del router.
	PutCredentials(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, creds []domain.Credential) error
	// GetCredential devuelve la credencial vigente de ese tipo.
	GetCredential(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, kind string) (*domain.Credential, error)
}

// Onboarding implementa devices/api.Onboarding.
type Onboarding struct {
	store  OnboardingStore
	sealer *envelope.Sealer
	now    func() time.Time
}

// NewOnboarding crea el servicio.
func NewOnboarding(store OnboardingStore, sealer *envelope.Sealer, now func() time.Time) *Onboarding {
	if now == nil {
		now = time.Now
	}
	return &Onboarding{store: store, sealer: sealer, now: now}
}

var (
	_ devapi.Onboarding           = (*Onboarding)(nil)
	_ devapi.SNMPCredentialReader = (*Onboarding)(nil)
	_ devapi.DirectVerifier       = (*Onboarding)(nil)
	_ devapi.TunnelSecrets        = (*Onboarding)(nil)
	_ devapi.ViaProjector         = (*Onboarding)(nil)
)

func routerInfo(r *domain.Router) devapi.RouterInfo {
	info := devapi.RouterInfo{ID: r.ID, TenantID: r.TenantID, SiteID: r.SiteID, Name: r.Hostname,
		RouterOSVersion: r.RouterOSVersion, OnboardingState: r.OnboardingState, Vendor: r.Vendor, ConnectionMode: connectionModeOf(r),
		ProtectionLevel: domain.ProtectionOf(connectionModeOf(r), r.ProtectionLevel), DeviceType: deviceTypeOf(r), SFlowEnabled: r.SFlowEnabled}
	if r.SwitchPlatform != nil {
		info.SwitchPlatform = *r.SwitchPlatform
	}
	if r.TunnelRemoteAddress != nil {
		if a, err := netip.ParseAddr(*r.TunnelRemoteAddress); err == nil {
			info.TunnelRemoteAddress = &a
		}
	}
	if r.TunnelRemoteID != nil {
		info.TunnelRemoteID = *r.TunnelRemoteID
	}
	if info.Vendor == "" {
		info.Vendor = domain.VendorMikroTik
	}
	if r.TunnelAddress != nil {
		if a, err := netip.ParseAddr(*r.TunnelAddress); err == nil {
			info.TunnelAddress = &a
		}
	}
	if r.ManagementAddress != nil {
		if a, err := netip.ParseAddr(*r.ManagementAddress); err == nil {
			info.ManagementAddress = &a
		}
	}
	if r.ViaRouterID != nil {
		id := *r.ViaRouterID
		info.ViaRouterID = &id
	}
	if r.ViaAddress != nil {
		if a, err := netip.ParseAddr(*r.ViaAddress); err == nil {
			info.ViaAddress = &a
		}
	}
	return info
}

// GetRouter implementa api.Onboarding.
func (o *Onboarding) GetRouter(ctx context.Context, tenant, routerID uuid.UUID) (devapi.RouterInfo, error) {
	r, err := o.store.GetRouter(ctx, pgdb.TenantID(tenant), routerID)
	if errors.Is(err, domain.ErrNotFound) {
		return devapi.RouterInfo{}, devapi.ErrRouterNotFound
	}
	if err != nil {
		return devapi.RouterInfo{}, err
	}
	return routerInfo(r), nil
}

// ListRouters implementa api.Onboarding.
func (o *Onboarding) ListRouters(ctx context.Context) ([]devapi.RouterInfo, error) {
	rows, err := o.store.ListAllRouters(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]devapi.RouterInfo, 0, len(rows))
	for i := range rows {
		out = append(out, routerInfo(&rows[i]))
	}
	return out, nil
}

var onboardingRank = map[string]int{
	devapi.OnboardingPending: 0, devapi.OnboardingKeyReceived: 1, devapi.OnboardingTunnelUp: 2, devapi.OnboardingSNMPVerified: 2,
	devapi.OnboardingExporting: 3,
}

// SetTunnel implementa api.Onboarding: proyección de horus.wireguard.peer.*
// (actor svc:wireguard) que publica horus.devices.router.updated con la IP de
// túnel (identidad del exportador para flows).
func (o *Onboarding) SetTunnel(ctx context.Context, tenant, routerID uuid.UUID, u devapi.TunnelUpdate) error {
	t := pgdb.TenantID(tenant)
	if !u.Address.IsValid() {
		return errors.New("devices: tunnel address required")
	}
	if _, ok := onboardingRank[u.OnboardingState]; u.OnboardingState != "" && !ok {
		return fmt.Errorf("devices: unknown onboarding state %q", u.OnboardingState)
	}
	var changed []string
	now := o.now().UTC()
	err := o.store.SetRouterTunnel(ctx, t, routerID, func(r *domain.Router) bool {
		if m := connectionModeOf(r); m == domain.ConnectionDirect || m == domain.ConnectionViaRouter {
			return false // modo directo o vía router: sin túnel propio
		}
		addr := u.Address.String()
		if r.TunnelAddress == nil || *r.TunnelAddress != addr {
			r.TunnelAddress = &addr
			changed = append(changed, "tunnel_address")
		}
		if r.WireguardPeerID == nil || *r.WireguardPeerID != u.PeerID {
			id := u.PeerID
			r.WireguardPeerID = &id
			changed = append(changed, "wireguard_peer_id")
		}
		// Retroceso permitido solo a pending (token regenerado / peer nuevo);
		// nunca se sale de exporting desde aquí.
		if u.OnboardingState != "" && u.OnboardingState != r.OnboardingState && r.OnboardingState != devapi.OnboardingExporting &&
			(onboardingRank[u.OnboardingState] > onboardingRank[r.OnboardingState] || u.OnboardingState == devapi.OnboardingPending) {
			r.OnboardingState = u.OnboardingState
			changed = append(changed, "onboarding_state")
		}
		if len(changed) > 0 {
			r.UpdatedAt = now
		}
		return len(changed) > 0
	}, func(r *domain.Router) []outbox.Event {
		tid := t.UUID()
		return []outbox.Event{{Type: "horus.devices.router.updated", Source: "horus/devices", TenantID: &tid, AggregateType: "router",
			AggregateID: r.ID, AggregateVersion: r.Version, Actor: outbox.Actor{Type: "service", ID: "svc:wireguard"}, OccurredAt: now,
			Data: RouterEventData(r, changed)}}
	})
	if errors.Is(err, domain.ErrNotFound) {
		return devapi.ErrRouterNotFound
	}
	return err
}

// DurableExporterState: consumidor de horus.flows.exporter.state_changed
// (streams.yaml consumers_i1, devices-exporter-state).
const DurableExporterState = "devices-exporter-state"

// Estados del exportador que prueban que llegan flujos del router.
var flowingStates = map[string]bool{"exporting": true, "lossy": true, "clock_skew": true}

// HandleExporterState proyecta el primer flujo del colector en el router:
// onboarding_state pasa a exporting (paso 3 del asistente, I1-19). Solo avanza
// desde tunnel_up o anteriores con túnel asignado (o, en modo directo, desde
// snmp_verified); nunca retrocede.
func (o *Onboarding) HandleExporterState(ctx context.Context, m *natsx.Message) error {
	if m.Envelope == nil || m.Envelope.Type != "horus.flows.exporter.state_changed" {
		return nil
	}
	var d struct {
		RouterID uuid.UUID `json:"router_id"`
		State    string    `json:"state"`
	}
	if err := json.Unmarshal(m.Envelope.Data, &d); err != nil {
		return natsx.Permanent(fmt.Errorf("exporter state_changed: %w", err))
	}
	if !flowingStates[d.State] || d.RouterID == uuid.Nil || m.Tenant == uuid.Nil {
		return nil
	}
	t := pgdb.TenantID(m.Tenant)
	now := o.now().UTC()
	err := o.store.SetRouterTunnel(ctx, t, d.RouterID, func(r *domain.Router) bool {
		if r.OnboardingState == devapi.OnboardingExporting {
			return false
		}
		switch connectionModeOf(r) {
		case domain.ConnectionDirect:
			if r.ManagementAddress == nil || r.OnboardingState != devapi.OnboardingSNMPVerified {
				return false
			}
		case domain.ConnectionViaRouter:
			if r.ViaAddress == nil || r.OnboardingState != devapi.OnboardingSNMPVerified {
				return false
			}
		default:
			if r.TunnelAddress == nil {
				return false
			}
		}
		r.OnboardingState, r.UpdatedAt = devapi.OnboardingExporting, now
		return true
	}, func(r *domain.Router) []outbox.Event {
		tid := t.UUID()
		return []outbox.Event{{Type: "horus.devices.router.updated", Source: "horus/devices", TenantID: &tid, AggregateType: "router",
			AggregateID: r.ID, AggregateVersion: r.Version, Actor: outbox.Actor{Type: "service", ID: "svc:flows"}, OccurredAt: now,
			Data: RouterEventData(r, []string{"onboarding_state"})}}
	})
	if errors.Is(err, domain.ErrNotFound) {
		return nil // router borrado: nada que proyectar
	}
	return err
}

const pwAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// password genera una contraseña aleatoria de n caracteres sin símbolos que
// RouterOS interprete dentro de comillas ($, ", \ ni ?).
func password(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(pwAlphabet)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("devices: random: %w", err)
		}
		b[i] = pwAlphabet[v.Int64()]
	}
	return string(b), nil
}

// CredentialAAD liga el secreto a su tenant, router y tipo.
func CredentialAAD(tenant, router uuid.UUID, kind string) []byte {
	return []byte("devices:credential:" + kind + ":" + tenant.String() + ":" + router.String())
}

type snmpSecret struct {
	AuthProtocol string `json:"auth_protocol"`
	AuthPassword string `json:"auth_password"`
	PrivProtocol string `json:"priv_protocol"`
	PrivPassword string `json:"priv_password"`
}

// IssueCredentials implementa api.Onboarding: SNMPv3 (SHA1/AES, authPriv) y
// usuario `horus` del grupo `horus-ro` con contraseñas nuevas; las anteriores
// quedan invalidadas.
func (o *Onboarding) IssueCredentials(ctx context.Context, tenant, routerID uuid.UUID) (devapi.OnboardingCredentials, error) {
	return o.issue(ctx, tenant, routerID, "sha1", true)
}

// IssueDirectCredentials genera solo la credencial SNMPv3 de un router en
// modo directo (Huawei VRP: sin API REST): authPriv SHA-256 + AES-128
// (`authentication-mode sha2-256`, `privacy-mode aes128`).
func (o *Onboarding) IssueDirectCredentials(ctx context.Context, tenant, routerID uuid.UUID) (devapi.OnboardingCredentials, error) {
	return o.issue(ctx, tenant, routerID, "sha256", false)
}

func (o *Onboarding) issue(ctx context.Context, tenant, routerID uuid.UUID, auth string, withAPI bool) (devapi.OnboardingCredentials, error) {
	t := pgdb.TenantID(tenant)
	if _, err := o.store.GetRouter(ctx, t, routerID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return devapi.OnboardingCredentials{}, devapi.ErrRouterNotFound
		}
		return devapi.OnboardingCredentials{}, err
	}
	var c devapi.OnboardingCredentials
	var err error
	suffix := routerID.String()[len(routerID.String())-8:]
	c.SNMPUser = "horus-" + suffix
	secrets := []*string{&c.SNMPAuthPassword, &c.SNMPPrivPassword}
	if withAPI {
		c.APIUser = "horus"
		secrets = append(secrets, &c.APIPassword)
	}
	for _, p := range secrets {
		if *p, err = password(24); err != nil {
			return c, err
		}
	}
	snmp, _ := json.Marshal(snmpSecret{AuthProtocol: auth, AuthPassword: c.SNMPAuthPassword, PrivProtocol: "aes128", PrivPassword: c.SNMPPrivPassword})
	items := []struct {
		kind, user string
		secret     []byte
	}{{domain.CredentialSNMPv3, c.SNMPUser, snmp}}
	if withAPI {
		items = append(items, struct {
			kind, user string
			secret     []byte
		}{domain.CredentialRouterOSAPI, c.APIUser, []byte(c.APIPassword)})
	}
	var creds []domain.Credential
	for _, x := range items {
		ct, dek, err := o.sealer.Seal(x.secret, CredentialAAD(tenant, routerID, x.kind))
		if err != nil {
			return devapi.OnboardingCredentials{}, err
		}
		creds = append(creds, domain.Credential{ID: uuid.Must(uuid.NewV7()), RouterID: routerID, Kind: x.kind, Username: x.user,
			Ciphertext: ct, DEKWrapped: dek, KEKID: o.sealer.KEKID()})
	}
	if err := o.store.PutCredentials(ctx, t, routerID, creds); err != nil {
		return devapi.OnboardingCredentials{}, err
	}
	return c, nil
}

// VerifyDirect implementa api.DirectVerifier: el rol snmp respondió con la
// credencial SNMP de un equipo sin túnel propio en addr. Si addr es su
// dirección de sondeo vigente (modo directo: la de gestión; vía router: su
// dirección Horus, que el router traduce a la IP privada), el equipo pasa de
// pending_configuration a snmp_verified (prueba de control: solo entonces el
// colector acepta sus flujos) y se publica router.updated. Nunca retrocede
// ni toca un router con túnel.
func (o *Onboarding) VerifyDirect(ctx context.Context, tenant, routerID uuid.UUID, addr netip.Addr) error {
	t := pgdb.TenantID(tenant)
	now := o.now().UTC()
	err := o.store.SetRouterTunnel(ctx, t, routerID, func(r *domain.Router) bool {
		if r.OnboardingState != devapi.OnboardingPending {
			return false
		}
		var want *string
		switch connectionModeOf(r) {
		case domain.ConnectionDirect:
			want = r.ManagementAddress
		case domain.ConnectionViaRouter:
			want = r.ViaAddress
		}
		if want == nil {
			return false
		}
		if a, err := netip.ParseAddr(*want); err != nil || a != addr.Unmap() {
			return false
		}
		r.OnboardingState, r.UpdatedAt = devapi.OnboardingSNMPVerified, now
		return true
	}, func(r *domain.Router) []outbox.Event {
		tid := t.UUID()
		return []outbox.Event{{Type: "horus.devices.router.updated", Source: "horus/devices", TenantID: &tid, AggregateType: "router",
			AggregateID: r.ID, AggregateVersion: r.Version, Actor: outbox.Actor{Type: "service", ID: "svc:snmp"}, OccurredAt: now,
			Data: RouterEventData(r, []string{"onboarding_state"})}}
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return devapi.ErrRouterNotFound
	case errors.Is(err, ErrManagementAddressTaken):
		return devapi.ErrManagementAddressTaken
	}
	return err
}

// SetViaAddress implementa api.ViaProjector: proyecta la dirección Horus que
// wireguard asignó al switch (actor svc:wireguard) si sigue yendo vía ese
// router con esa IP privada. Una dirección nueva exige volver a verificar
// (el sondeo por el camino nuevo es la prueba de control).
func (o *Onboarding) SetViaAddress(ctx context.Context, tenant, deviceID, routerID uuid.UUID, target, address netip.Addr) error {
	t := pgdb.TenantID(tenant)
	now := o.now().UTC()
	var changed []string
	err := o.store.SetRouterTunnel(ctx, t, deviceID, func(r *domain.Router) bool {
		changed = nil
		if connectionModeOf(r) != domain.ConnectionViaRouter || r.ViaRouterID == nil || *r.ViaRouterID != routerID || r.ManagementAddress == nil {
			return false
		}
		if a, err := netip.ParseAddr(*r.ManagementAddress); err != nil || a != target.Unmap() {
			return false
		}
		next := address.Unmap().String()
		if r.ViaAddress != nil && *r.ViaAddress == next {
			return false
		}
		r.ViaAddress, r.UpdatedAt = &next, now
		changed = append(changed, "via_address")
		if r.OnboardingState != devapi.OnboardingPending {
			r.OnboardingState = devapi.OnboardingPending
			changed = append(changed, "onboarding_state")
		}
		return true
	}, func(r *domain.Router) []outbox.Event {
		tid := t.UUID()
		return []outbox.Event{{Type: "horus.devices.router.updated", Source: "horus/devices", TenantID: &tid, AggregateType: "router",
			AggregateID: r.ID, AggregateVersion: r.Version, Actor: outbox.Actor{Type: "service", ID: "svc:wireguard"}, OccurredAt: now,
			Data: RouterEventData(r, changed)}}
	})
	if errors.Is(err, domain.ErrNotFound) {
		return devapi.ErrRouterNotFound
	}
	return err
}

// RevealSNMPCredential implementa api.SNMPCredentialReader: descifra la
// credencial SNMPv3 vigente del router para el rol snmp (I2). Nunca se
// registra ni se devuelve por HTTP.
func (o *Onboarding) RevealSNMPCredential(ctx context.Context, tenant, routerID uuid.UUID) (devapi.SNMPCredential, error) {
	t := pgdb.TenantID(tenant)
	if _, err := o.store.GetRouter(ctx, t, routerID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return devapi.SNMPCredential{}, devapi.ErrRouterNotFound
		}
		return devapi.SNMPCredential{}, err
	}
	c, err := o.store.GetCredential(ctx, t, routerID, domain.CredentialSNMPv3)
	if errors.Is(err, domain.ErrNotFound) {
		// Switch SwOS: comunidad v2c (sin SNMPv3).
		c, err = o.store.GetCredential(ctx, t, routerID, domain.CredentialSNMPv2c)
	}
	if errors.Is(err, domain.ErrNotFound) {
		return devapi.SNMPCredential{}, devapi.ErrNoCredential
	}
	if err != nil {
		return devapi.SNMPCredential{}, err
	}
	pt, err := o.sealer.Open(c.Ciphertext, c.DEKWrapped, CredentialAAD(tenant, routerID, c.Kind))
	if err != nil {
		return devapi.SNMPCredential{}, fmt.Errorf("devices: open credential: %w", err)
	}
	if c.Kind == domain.CredentialSNMPv2c {
		return devapi.SNMPCredential{Version: "2c", Community: string(pt)}, nil
	}
	var s snmpSecret
	if err := json.Unmarshal(pt, &s); err != nil {
		return devapi.SNMPCredential{}, fmt.Errorf("devices: snmp credential: %w", err)
	}
	return devapi.SNMPCredential{Version: "3", Username: c.Username, AuthProtocol: s.AuthProtocol, AuthPassword: s.AuthPassword,
		PrivProtocol: s.PrivProtocol, PrivPassword: s.PrivPassword}, nil
}

// APICredential descifra la credencial de la API RouterOS del router (para
// el adaptador de lectura, I1-28).
func (o *Onboarding) APICredential(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) (*domain.Credential, string, error) {
	c, err := o.store.GetCredential(ctx, t, routerID, domain.CredentialRouterOSAPI)
	if err != nil {
		return nil, "", err
	}
	pt, err := o.sealer.Open(c.Ciphertext, c.DEKWrapped, CredentialAAD(t.UUID(), routerID, c.Kind))
	if err != nil {
		return nil, "", fmt.Errorf("devices: open credential: %w", err)
	}
	return c, string(pt), nil
}

// pskLen: 48 caracteres del alfabeto sin ambigüedades (~280 bits); el
// agente del hub exige ≥ 32 (RFC 7296 §2.15).
const pskLen = 48

// IssueTunnelPSK implementa api.TunnelSecrets.
func (o *Onboarding) IssueTunnelPSK(ctx context.Context, tenant, routerID uuid.UUID) (string, error) {
	t := pgdb.TenantID(tenant)
	if _, err := o.store.GetRouter(ctx, t, routerID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", devapi.ErrRouterNotFound
		}
		return "", err
	}
	psk, err := password(pskLen)
	if err != nil {
		return "", err
	}
	ct, dek, err := o.sealer.Seal([]byte(psk), CredentialAAD(tenant, routerID, domain.CredentialIPsecPSK))
	if err != nil {
		return "", err
	}
	if err := o.store.PutCredentials(ctx, t, routerID, []domain.Credential{{ID: uuid.Must(uuid.NewV7()), RouterID: routerID,
		Kind: domain.CredentialIPsecPSK, Username: "ike", Ciphertext: ct, DEKWrapped: dek, KEKID: o.sealer.KEKID()}}); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", devapi.ErrRouterNotFound
		}
		return "", err
	}
	return psk, nil
}

// RevealTunnelPSK implementa api.TunnelSecrets.
func (o *Onboarding) RevealTunnelPSK(ctx context.Context, tenant, routerID uuid.UUID) (string, error) {
	c, err := o.store.GetCredential(ctx, pgdb.TenantID(tenant), routerID, domain.CredentialIPsecPSK)
	if errors.Is(err, domain.ErrNotFound) {
		return "", devapi.ErrNoCredential
	}
	if err != nil {
		return "", err
	}
	pt, err := o.sealer.Open(c.Ciphertext, c.DEKWrapped, CredentialAAD(tenant, routerID, c.Kind))
	if err != nil {
		return "", fmt.Errorf("devices: open credential: %w", err)
	}
	return string(pt), nil
}

// communityLen: comunidad v2c de 32 caracteres del alfabeto sin ambigüedades
// (~180 bits). Viaja en claro (v2c): solo en la red de gestión.
const communityLen = 32

// IssueSwitchCredentials genera la credencial SNMP de un switch según su
// plataforma: SNMPv3 SHA1+AES (RouterOS: lo que admite /snmp community),
// SHA-256+AES-128 (Huawei serie S) o una comunidad v2c (SwOS). Invalida la
// anterior; se guarda cifrada y se devuelve en claro una sola vez.
func (o *Onboarding) IssueSwitchCredentials(ctx context.Context, tenant, routerID uuid.UUID, platform string) (devapi.OnboardingCredentials, string, error) {
	switch platform {
	case domain.PlatformRouterOS:
		c, err := o.issue(ctx, tenant, routerID, "sha1", false)
		return c, "", err
	case domain.PlatformVRP:
		c, err := o.issue(ctx, tenant, routerID, "sha256", false)
		return c, "", err
	}
	t := pgdb.TenantID(tenant)
	if _, err := o.store.GetRouter(ctx, t, routerID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return devapi.OnboardingCredentials{}, "", devapi.ErrRouterNotFound
		}
		return devapi.OnboardingCredentials{}, "", err
	}
	community, err := password(communityLen)
	if err != nil {
		return devapi.OnboardingCredentials{}, "", err
	}
	ct, dek, err := o.sealer.Seal([]byte(community), CredentialAAD(tenant, routerID, domain.CredentialSNMPv2c))
	if err != nil {
		return devapi.OnboardingCredentials{}, "", err
	}
	if err := o.store.PutCredentials(ctx, t, routerID, []domain.Credential{{ID: uuid.Must(uuid.NewV7()), RouterID: routerID,
		Kind: domain.CredentialSNMPv2c, Username: "community", Ciphertext: ct, DEKWrapped: dek, KEKID: o.sealer.KEKID()}}); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return devapi.OnboardingCredentials{}, "", devapi.ErrRouterNotFound
		}
		return devapi.OnboardingCredentials{}, "", err
	}
	return devapi.OnboardingCredentials{}, community, nil
}

// IssueSNMPCredentials implementa api.TunnelSecrets (Huawei con túnel).
func (o *Onboarding) IssueSNMPCredentials(ctx context.Context, tenant, routerID uuid.UUID) (devapi.OnboardingCredentials, error) {
	return o.IssueDirectCredentials(ctx, tenant, routerID)
}
