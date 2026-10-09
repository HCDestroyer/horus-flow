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

var _ devapi.Onboarding = (*Onboarding)(nil)

func routerInfo(r *domain.Router) devapi.RouterInfo {
	info := devapi.RouterInfo{ID: r.ID, TenantID: r.TenantID, SiteID: r.SiteID, Name: r.Hostname,
		RouterOSVersion: r.RouterOSVersion, OnboardingState: r.OnboardingState}
	if r.TunnelAddress != nil {
		if a, err := netip.ParseAddr(*r.TunnelAddress); err == nil {
			info.TunnelAddress = &a
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
	devapi.OnboardingPending: 0, devapi.OnboardingKeyReceived: 1, devapi.OnboardingTunnelUp: 2, devapi.OnboardingExporting: 3,
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
	c.SNMPUser, c.APIUser = "horus-"+suffix, "horus"
	for _, p := range []*string{&c.SNMPAuthPassword, &c.SNMPPrivPassword, &c.APIPassword} {
		if *p, err = password(24); err != nil {
			return c, err
		}
	}
	snmp, _ := json.Marshal(snmpSecret{AuthProtocol: "sha1", AuthPassword: c.SNMPAuthPassword, PrivProtocol: "aes128", PrivPassword: c.SNMPPrivPassword})
	var creds []domain.Credential
	for _, x := range []struct {
		kind, user string
		secret     []byte
	}{{domain.CredentialSNMPv3, c.SNMPUser, snmp}, {domain.CredentialRouterOSAPI, c.APIUser, []byte(c.APIPassword)}} {
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
