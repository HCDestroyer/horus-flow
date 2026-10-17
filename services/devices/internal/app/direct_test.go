package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// directStore en memoria (un router).
type memDirectStore struct {
	router *domain.Router
	creds  []domain.Credential
	events []outbox.Event
	taken  bool // simula el índice único de direcciones verificadas
}

func (m *memDirectStore) GetRouter(_ context.Context, _ pgdb.TenantID, id uuid.UUID) (*domain.Router, error) {
	if m.router == nil || m.router.ID != id {
		return nil, domain.ErrNotFound
	}
	c := *m.router
	return &c, nil
}

func (m *memDirectStore) ListAllRouters(context.Context) ([]domain.Router, error) {
	return []domain.Router{*m.router}, nil
}

func (m *memDirectStore) SetRouterTunnel(_ context.Context, _ pgdb.TenantID, id uuid.UUID, fn func(r *domain.Router) bool, ev Events[domain.Router]) error {
	if m.router == nil || m.router.ID != id {
		return domain.ErrNotFound
	}
	c := *m.router
	if !fn(&c) {
		return nil
	}
	if m.taken && c.OnboardingState == devapi.OnboardingSNMPVerified {
		return ErrManagementAddressTaken
	}
	c.Version++
	m.router = &c
	m.events = append(m.events, ev(&c)...)
	return nil
}

func (m *memDirectStore) PutCredentials(_ context.Context, _ pgdb.TenantID, _ uuid.UUID, creds []domain.Credential) error {
	m.creds = creds
	return nil
}

func (m *memDirectStore) GetCredential(_ context.Context, _ pgdb.TenantID, _ uuid.UUID, kind string) (*domain.Credential, error) {
	for i := range m.creds {
		if m.creds[i].Kind == kind {
			return &m.creds[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *memDirectStore) Emit(_ context.Context, evs []outbox.Event) error {
	m.events = append(m.events, evs...)
	return nil
}

func huaweiRouter(tenant uuid.UUID) *domain.Router {
	return &domain.Router{ID: uuid.MustParse("0192e333-0000-7000-8000-0000000000a1"), TenantID: tenant, SiteID: uuid.New(), Hostname: "ne40e-1",
		Vendor: domain.VendorHuawei, ConnectionMode: domain.ConnectionDirect, OnboardingState: devapi.OnboardingPending, Version: 1}
}

func TestDirectOnboardingCreate(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	st := &memDirectStore{router: huaweiRouter(tenant)}
	sealer := envelope.Ephemeral()
	on := NewOnboarding(st, sealer, nil)
	d := NewDirectOnboarding(st, on, DirectSettings{CollectorAddress: netip.MustParseAddr("203.0.113.10"), CollectorPort: 2055,
		PollerAddress: netip.MustParseAddr("203.0.113.11")}, nil, nil, func() time.Time { return time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC) })
	ctx := asnCtx(tenant, PermCredentialsWrite)
	id := st.router.ID

	if _, err := d.Create(ctx, id, DirectOnboardingInput{}); !isCode(err, "VALIDATION_FAILED") {
		t.Fatalf("sin dirección de gestión: %v", err)
	}
	bad := "127.0.0.1"
	if _, err := d.Create(ctx, id, DirectOnboardingInput{ManagementAddress: &bad}); !isCode(err, "VALIDATION_FAILED") {
		t.Fatalf("loopback: %v", err)
	}
	mgmt := "198.51.100.20"
	rate := 512
	res, err := d.Create(ctx, id, DirectOnboardingInput{ManagementAddress: &mgmt, SamplingRate: &rate})
	if err != nil {
		t.Fatal(err)
	}
	if res.ManagementAddress != mgmt || res.SNMPUser != "horus-000000a1" || res.SamplingRate != 512 || *res.CollectorAddress != "203.0.113.10" {
		t.Fatalf("resultado: %+v", res)
	}
	for _, want := range []string{"rule 5 permit source 203.0.113.11 0", "ip netstream export source 198.51.100.20",
		"ip netstream export host 203.0.113.10 2055", "ip netstream sampler fix-packets 512 inbound", "authentication-mode sha2-256"} {
		if !strings.Contains(res.Config, want) {
			t.Errorf("falta %q", want)
		}
	}
	// Solo credencial SNMPv3 (SHA-256/AES-128), sin usuario de la API de RouterOS; cifrada.
	if len(st.creds) != 1 || st.creds[0].Kind != domain.CredentialSNMPv3 {
		t.Fatalf("credenciales: %+v", st.creds)
	}
	snmp, err := on.RevealSNMPCredential(context.Background(), tenant, id)
	if err != nil || snmp.AuthProtocol != "sha256" || snmp.PrivProtocol != "aes128" || !strings.Contains(res.Config, snmp.AuthPassword) {
		t.Fatalf("credencial: %+v %v", snmp, err)
	}
	if st.router.ManagementAddress == nil || *st.router.ManagementAddress != mgmt || st.events[0].Type != "horus.devices.router.updated" {
		t.Fatalf("dirección fijada y router.updated: %+v %v", st.router, st.events)
	}
	var data map[string]any
	b, _ := json.Marshal(st.events[0].Data)
	_ = json.Unmarshal(b, &data)
	if data["management_address"] != "198.51.100.20/32" || data["connection_mode"] != "direct" || data["vendor"] != "huawei" {
		t.Fatalf("payload: %v", data)
	}
	// Un MikroTik no tiene alta directa.
	mk := &memDirectStore{router: &domain.Router{ID: id, TenantID: tenant, Vendor: domain.VendorMikroTik}}
	if _, err := NewDirectOnboarding(mk, NewOnboarding(mk, sealer, nil), DirectSettings{}, nil, nil, nil).Create(ctx, id,
		DirectOnboardingInput{ManagementAddress: &mgmt}); !isCode(err, domain.CodeVendorNotSupported) {
		t.Fatalf("MikroTik: %v", err)
	}
	// Sin permiso.
	if _, err := d.Create(asnCtx(tenant, "devices.read"), id, DirectOnboardingInput{}); err == nil {
		t.Fatal("sin devices.credentials.write")
	}
}

func TestVerifyDirectAndExporting(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	mgmt := "198.51.100.20"
	r := huaweiRouter(tenant)
	r.ManagementAddress = &mgmt
	st := &memDirectStore{router: r}
	o := NewOnboarding(st, nil, nil)
	ctx := context.Background()
	if err := o.VerifyDirect(ctx, tenant, r.ID, netip.MustParseAddr("198.51.100.99")); err != nil || st.router.OnboardingState != devapi.OnboardingPending {
		t.Fatalf("otra dirección no verifica: %v %s", err, st.router.OnboardingState)
	}
	if err := o.VerifyDirect(ctx, tenant, r.ID, netip.MustParseAddr(mgmt)); err != nil || st.router.OnboardingState != devapi.OnboardingSNMPVerified ||
		len(st.events) != 1 || st.events[0].Actor.ID != "svc:snmp" {
		t.Fatalf("verificación: %v %s %v", err, st.router.OnboardingState, st.events)
	}
	if err := o.VerifyDirect(ctx, tenant, r.ID, netip.MustParseAddr(mgmt)); err != nil || len(st.events) != 1 {
		t.Fatalf("idempotente: %v %d", err, len(st.events))
	}
	if err := o.VerifyDirect(ctx, tenant, uuid.New(), netip.MustParseAddr(mgmt)); !errors.Is(err, devapi.ErrRouterNotFound) {
		t.Fatalf("router inexistente: %v", err)
	}
	// Dirección ya verificada por otro router de la plataforma.
	r2 := huaweiRouter(tenant)
	r2.ManagementAddress = &mgmt
	busy := &memDirectStore{router: r2, taken: true}
	if err := NewOnboarding(busy, nil, nil).VerifyDirect(ctx, tenant, r2.ID, netip.MustParseAddr(mgmt)); !errors.Is(err, devapi.ErrManagementAddressTaken) {
		t.Fatalf("dirección ocupada: %v", err)
	}
	// Un router en modo directo no recibe túnel de wireguard.
	if err := o.SetTunnel(ctx, tenant, r.ID, devapi.TunnelUpdate{PeerID: uuid.New(), Address: netip.MustParseAddr("10.255.0.9")}); err != nil ||
		st.router.TunnelAddress != nil {
		t.Fatalf("túnel en modo directo: %v %+v", err, st.router)
	}
	info, err := o.GetRouter(ctx, tenant, r.ID)
	if err != nil || info.Vendor != devapi.VendorHuawei || info.ConnectionMode != devapi.ConnectionDirect || info.ManagementAddress.String() != mgmt ||
		info.TunnelAddress != nil {
		t.Fatalf("RouterInfo: %+v %v", info, err)
	}
}

func TestApplyRouterVendor(t *testing.T) {
	t.Parallel()
	r := &domain.Router{Vendor: domain.VendorMikroTik, OnboardingState: devapi.OnboardingPending}
	var f fieldErrs
	applyRouter(&f, r, body(t, map[string]any{"name": "ne40e", "vendor": "huawei", "management_address": "2001:db8::1"}), true)
	if f.err() != nil || r.Vendor != domain.VendorHuawei || r.ConnectionMode != domain.ConnectionDirect || *r.ManagementAddress != "2001:db8::1" {
		t.Fatalf("alta Huawei: %v %+v", f.err(), r)
	}
	f = fieldErrs{}
	applyRouter(&f, r, body(t, map[string]any{"routeros_version": "7.16"}), false)
	if f.err() == nil {
		t.Fatal("routeros_version en Huawei")
	}
	// Cambiar la dirección de un router verificado lo devuelve a pending (hay que volver a verificar).
	r.OnboardingState = devapi.OnboardingExporting
	f = fieldErrs{}
	applyRouter(&f, r, body(t, map[string]any{"management_address": "198.51.100.30"}), false)
	if f.err() != nil || r.OnboardingState != devapi.OnboardingPending || *r.ManagementAddress != "198.51.100.30" {
		t.Fatalf("cambio de dirección: %v %+v", f.err(), r)
	}
	mk := &domain.Router{Vendor: domain.VendorMikroTik}
	f = fieldErrs{}
	applyRouter(&f, mk, body(t, map[string]any{"name": "rt", "management_address": "198.51.100.1"}), true)
	if f.err() == nil || mk.ConnectionMode != domain.ConnectionWireGuard {
		t.Fatalf("MikroTik sin dirección de gestión: %+v", mk)
	}
	f = fieldErrs{}
	applyRouter(&f, &domain.Router{Vendor: domain.VendorMikroTik}, body(t, map[string]any{"name": "rt", "vendor": "cisco"}), true)
	if f.err() == nil {
		t.Fatal("fabricante desconocido")
	}
}

func isCode(err error, code string) bool {
	e, ok := apperr.As(err)
	return ok && e.Code == code
}
