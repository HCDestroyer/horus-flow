package app

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
	viaapi "github.com/hcdestroyer/horus-flow/services/wireguard/api"
)

// memViaStore: varios equipos de un tenant y sus redes de gestión.
type memViaStore struct {
	mu      sync.Mutex
	tenant  uuid.UUID
	routers map[uuid.UUID]*domain.Router
	subnets []domain.ManagementSubnet
	creds   []domain.Credential
	events  []outbox.Event
}

func (m *memViaStore) GetRouter(_ context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routers[id]
	if !ok || t.UUID() != m.tenant {
		return nil, domain.ErrNotFound
	}
	c := *r
	return &c, nil
}

func (m *memViaStore) ListAllRouters(context.Context) ([]domain.Router, error) { return nil, nil }

func (m *memViaStore) SetRouterTunnel(_ context.Context, t pgdb.TenantID, id uuid.UUID, fn func(r *domain.Router) bool, ev Events[domain.Router]) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routers[id]
	if !ok || t.UUID() != m.tenant {
		return domain.ErrNotFound
	}
	c := *r
	if !fn(&c) {
		return nil
	}
	c.Version++
	m.routers[id] = &c
	m.events = append(m.events, ev(&c)...)
	return nil
}

func (m *memViaStore) PutCredentials(_ context.Context, _ pgdb.TenantID, _ uuid.UUID, creds []domain.Credential) error {
	m.creds = creds
	return nil
}

func (m *memViaStore) GetCredential(_ context.Context, _ pgdb.TenantID, _ uuid.UUID, kind string) (*domain.Credential, error) {
	for i := range m.creds {
		if m.creds[i].Kind == kind {
			return &m.creds[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *memViaStore) Emit(_ context.Context, evs []outbox.Event) error {
	m.events = append(m.events, evs...)
	return nil
}

func (m *memViaStore) CountViaDevices(context.Context, pgdb.TenantID, uuid.UUID) (int, error) { return 0, nil }
func (m *memViaStore) ListManagementSubnets(context.Context, pgdb.TenantID, uuid.UUID) ([]domain.ManagementSubnet, error) {
	return m.subnets, nil
}
func (m *memViaStore) CreateManagementSubnet(context.Context, pgdb.TenantID, *domain.ManagementSubnet) error { return nil }
func (m *memViaStore) DeleteManagementSubnet(context.Context, pgdb.TenantID, uuid.UUID, uuid.UUID) error  { return nil }
func (m *memViaStore) ViaDevicesIn(context.Context, pgdb.TenantID, uuid.UUID, netip.Prefix) (int, error) {
	return 0, nil
}

func (m *memViaStore) ManagementSubnetFor(_ context.Context, _ pgdb.TenantID, router uuid.UUID, a netip.Addr) (*domain.ManagementSubnet, error) {
	for i := range m.subnets {
		if m.subnets[i].RouterID == router && m.subnets[i].Prefix.Contains(a) {
			return &m.subnets[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

// fakeViaPaths emula wireguard: asigna una dirección Horus y la proyecta en
// el switch por devapi.ViaProjector.
type fakeViaPaths struct {
	proj devapi.ViaProjector
	reqs []viaapi.ViaPathRequest
}

func (f *fakeViaPaths) EnsureViaPath(ctx context.Context, req viaapi.ViaPathRequest) (viaapi.ViaPath, error) {
	f.reqs = append(f.reqs, req)
	addr := netip.MustParseAddr("10.255.1.40")
	if err := f.proj.SetViaAddress(ctx, req.TenantID, req.DeviceID, req.RouterID, req.Target, addr); err != nil {
		return viaapi.ViaPath{}, err
	}
	return viaapi.ViaPath{Address: addr, Transport: "wireguard", ProtectionLevel: "encrypted", RouterName: "rt-centro",
		PollerAddress: netip.MustParseAddr("10.255.0.1"), ServicesCIDR: netip.MustParsePrefix("10.255.0.0/24"),
		CollectorAddress: netip.MustParseAddr("10.255.0.1"), CollectorPort: 6343, TunnelStatus: "active",
		RouterConfig: viaapi.RouterConfig{Language: "routeros", Config: "/ip firewall nat add chain=dstnat ..."}}, nil
}

// Alta de un switch vía router: el router elegido debe ser del ISP, un router
// y con túnel; la configuración del switch apunta al hub (poller/colector por
// el túnel), la dirección Horus queda proyectada, el camino nuevo exige
// verificar y la IP privada sigue siendo la de gestión.
func TestSwitchOnboardingViaRouter(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	now := func() time.Time { return time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC) }
	sw := switchRouter(tenant, domain.PlatformVRP)
	rt := &domain.Router{ID: uuid.New(), TenantID: tenant, SiteID: sw.SiteID, Hostname: "rt-centro", Vendor: domain.VendorMikroTik,
		DeviceType: domain.DeviceTypeRouter, ConnectionMode: domain.ConnectionWireGuard, ProtectionLevel: domain.ProtectionEncrypted, Version: 1}
	direct := &domain.Router{ID: uuid.New(), TenantID: tenant, SiteID: sw.SiteID, Hostname: "ne40e", Vendor: domain.VendorHuawei,
		DeviceType: domain.DeviceTypeRouter, ConnectionMode: domain.ConnectionDirect, ProtectionLevel: domain.ProtectionUnencryptedPrivate, Version: 1}
	other := switchRouter(tenant, domain.PlatformRouterOS)
	other.ID = uuid.New()
	st := &memViaStore{tenant: tenant, routers: map[uuid.UUID]*domain.Router{sw.ID: sw, rt.ID: rt, direct.ID: direct, other.ID: other}}
	on := NewOnboarding(st, envelope.Ephemeral(), now)
	paths := &fakeViaPaths{proj: on}
	settings := DirectSettings{Deny: []netip.Prefix{netip.MustParsePrefix("10.255.0.0/16")}}
	d := NewDirectOnboarding(st, on, settings, nil, nil, now).WithVia(st, func() (viaapi.ViaPaths, bool) { return paths, true })
	ctx := asnCtx(tenant, PermCredentialsWrite, "devices.read")
	mode, mgmt, yes := "via_router", "192.168.88.2", true
	id := func(u uuid.UUID) *string { s := u.String(); return &s }
	// Router inválido: inexistente, en modo directo (sin túnel), un switch o el propio switch.
	for name, via := range map[string]*string{"inexistente": id(uuid.New()), "directo": id(direct.ID), "switch": id(other.ID),
		"él mismo": id(sw.ID), "basura": strPtr("x")} {
		if _, err := d.CreateSwitch(ctx, sw.ID, SwitchOnboardingInput{ConnectionMode: &mode, ViaRouterID: via, ManagementAddress: &mgmt}); !isCode(err, "VALIDATION_FAILED") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := d.CreateSwitch(ctx, sw.ID, SwitchOnboardingInput{ConnectionMode: &mode, ManagementAddress: &mgmt}); !isCode(err, "VALIDATION_FAILED") {
		t.Fatalf("sin router: %v", err)
	}
	// IP de Horus como dirección de gestión: nunca.
	horus := "10.255.0.9"
	if _, err := d.CreateSwitch(ctx, sw.ID, SwitchOnboardingInput{ConnectionMode: &mode, ViaRouterID: id(rt.ID), ManagementAddress: &horus}); !isCode(err, "VALIDATION_FAILED") {
		t.Fatalf("IP de Horus: %v", err)
	}
	res, err := d.CreateSwitch(ctx, sw.ID, SwitchOnboardingInput{ConnectionMode: &mode, ViaRouterID: id(rt.ID), ManagementAddress: &mgmt, SFlow: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if res.ConnectionMode != "via_router" || res.Path == nil || res.Path.HorusAddress != "10.255.1.40" || res.Path.Transport != "wireguard" ||
		res.RouterConfig == nil || res.RouterConfig.Language != "routeros" || res.ManagementAddress != mgmt {
		t.Fatalf("resultado: %+v", res)
	}
	// El switch habla con el hub por el túnel: ACL SNMP y colector sFlow = IP de servicios del hub.
	if !strings.Contains(res.Config, "rule 5 permit source 10.255.0.1 0") || res.SFlowConfig == nil ||
		!strings.Contains(*res.SFlowConfig, "sflow collector 1 ip 10.255.0.1 udp-port 6343") || !strings.Contains(*res.SFlowConfig, "sflow agent ip 192.168.88.2") {
		t.Fatalf("configuración del switch:\n%s\n%v", res.Config, res.SFlowConfig)
	}
	got := st.routers[sw.ID]
	if got.ConnectionMode != domain.ConnectionViaRouter || got.ViaRouterID == nil || *got.ViaRouterID != rt.ID || got.ViaAddress == nil ||
		*got.ViaAddress != "10.255.1.40" || *got.ManagementAddress != mgmt || got.OnboardingState != devapi.OnboardingPending {
		t.Fatalf("switch guardado: %+v", got)
	}
	if len(paths.reqs) != 1 || paths.reqs[0].Target != netip.MustParseAddr(mgmt) || !paths.reqs[0].SFlow || paths.reqs[0].Subnet != nil {
		t.Fatalf("petición a wireguard: %+v", paths.reqs)
	}
	// Prueba de control por la dirección Horus (no por la IP privada).
	if err := on.VerifyDirect(context.Background(), tenant, sw.ID, netip.MustParseAddr(mgmt)); err != nil || st.routers[sw.ID].OnboardingState != devapi.OnboardingPending {
		t.Fatalf("verificar por la IP privada no vale: %v %s", err, st.routers[sw.ID].OnboardingState)
	}
	if err := on.VerifyDirect(context.Background(), tenant, sw.ID, netip.MustParseAddr("10.255.1.40")); err != nil ||
		st.routers[sw.ID].OnboardingState != devapi.OnboardingSNMPVerified {
		t.Fatalf("verificación por la dirección Horus: %v %s", err, st.routers[sw.ID].OnboardingState)
	}
	// Red de gestión declarada en el router: se pasa a wireguard.
	st.subnets = []domain.ManagementSubnet{{ID: uuid.New(), TenantID: tenant, RouterID: rt.ID, Prefix: netip.MustParsePrefix("192.168.88.0/24")}}
	if _, err := d.CreateSwitch(ctx, sw.ID, SwitchOnboardingInput{}); err != nil || paths.reqs[1].Subnet == nil ||
		paths.reqs[1].Subnet.Prefix != netip.MustParsePrefix("192.168.88.0/24") {
		t.Fatalf("red declarada: %v %+v", err, paths.reqs)
	}
	// Vuelta a modo directo: sin router del sitio ni dirección Horus, hay que volver a verificar.
	dm := "direct"
	if _, err := d.CreateSwitch(ctx, sw.ID, SwitchOnboardingInput{ConnectionMode: &dm}); err != nil {
		t.Fatal(err)
	}
	got = st.routers[sw.ID]
	if got.ConnectionMode != domain.ConnectionDirect || got.ViaRouterID != nil || got.ViaAddress != nil || got.OnboardingState != devapi.OnboardingPending {
		t.Fatalf("vuelta a directo: %+v", got)
	}
	// La proyección de una dirección Horus vieja (otro router u otra IP) no se aplica.
	if err := on.SetViaAddress(context.Background(), tenant, sw.ID, rt.ID, netip.MustParseAddr(mgmt), netip.MustParseAddr("10.255.1.99")); err != nil ||
		st.routers[sw.ID].ViaAddress != nil {
		t.Fatalf("proyección obsoleta aplicada: %v %+v", err, st.routers[sw.ID])
	}
}

