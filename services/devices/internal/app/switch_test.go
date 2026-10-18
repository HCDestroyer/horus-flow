package app

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/crypto/envelope"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

func switchRouter(tenant uuid.UUID, platform string) *domain.Router {
	p := platform
	return &domain.Router{ID: uuid.MustParse("0192e333-0000-7000-8000-0000000000b2"), TenantID: tenant, SiteID: uuid.New(), Hostname: "sw-core-1",
		Vendor: domain.VendorForPlatform(platform), DeviceType: domain.DeviceTypeSwitch, SwitchPlatform: &p, ConnectionMode: domain.ConnectionDirect,
		ProtectionLevel: domain.ProtectionUnencryptedPrivate, OnboardingState: devapi.OnboardingPending, Version: 1}
}

func TestApplyRouterSwitch(t *testing.T) {
	t.Parallel()
	newR := func() *domain.Router { return &domain.Router{Vendor: domain.VendorMikroTik, IsPrimary: true} }
	// Switch Huawei: fabricante por plataforma, directo, nunca principal.
	r := newR()
	var f fieldErrs
	applyRouter(&f, r, body(t, map[string]any{"name": "sw1", "device_type": "switch", "switch_platform": "vrp", "management_address": "10.20.0.5"}), true)
	if f.err() != nil || !r.IsSwitch() || r.Vendor != domain.VendorHuawei || r.IsPrimary || r.ConnectionMode != domain.ConnectionDirect ||
		r.ProtectionLevel != domain.ProtectionUnencryptedPrivate || r.ManagementAddress == nil || *r.ManagementAddress != "10.20.0.5" {
		t.Fatalf("switch vrp: %v %+v", f, r)
	}
	// Sin plataforma, principal o fabricante incoherente → errores de campo.
	for name, in := range map[string]map[string]any{
		"sin plataforma":       {"name": "sw1", "device_type": "switch"},
		"principal":            {"name": "sw1", "device_type": "switch", "switch_platform": "routeros", "is_primary": true},
		"fabricante":           {"name": "sw1", "device_type": "switch", "switch_platform": "vrp", "vendor": "mikrotik"},
		"plataforma en router": {"name": "r1", "switch_platform": "vrp"},
		"routeros_version":     {"name": "sw1", "device_type": "switch", "switch_platform": "routeros", "routeros_version": "7.16"},
		"sflow en router":      {"name": "r1", "sflow_enabled": true},
	} {
		var f fieldErrs
		applyRouter(&f, newR(), body(t, in), true)
		if f.err() == nil && name != "sflow en router" {
			t.Errorf("%s: se esperaba error", name)
		}
	}
	// sFlow: solo vrp; en PATCH.
	sw := switchRouter(uuid.New(), domain.PlatformRouterOS)
	f = nil
	applyRouter(&f, sw, body(t, map[string]any{"sflow_enabled": true}), false)
	if !f.has("sflow_enabled") || sw.SFlowEnabled {
		t.Fatalf("sflow en routeros: %v", f)
	}
	hw := switchRouter(uuid.New(), domain.PlatformVRP)
	f = nil
	applyRouter(&f, hw, body(t, map[string]any{"sflow_enabled": true}), false)
	if f.err() != nil || !hw.SFlowEnabled {
		t.Fatalf("sflow en vrp: %v", f)
	}
	// Un router normal no cambia: MikroTik → wireguard, tipo router.
	rr := newR()
	f = nil
	applyRouter(&f, rr, body(t, map[string]any{"name": "r1"}), true)
	if f.err() != nil || rr.DeviceType != domain.DeviceTypeRouter || rr.ConnectionMode != domain.ConnectionWireGuard || !rr.IsPrimary {
		t.Fatalf("router: %v %+v", f, rr)
	}
}

func TestSwitchOnboarding(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	sealer := envelope.Ephemeral()
	now := func() time.Time { return time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC) }
	settings := DirectSettings{CollectorAddress: netip.MustParseAddr("203.0.113.10"), CollectorPort: 2055,
		PollerAddress: netip.MustParseAddr("203.0.113.11"), Deny: []netip.Prefix{netip.MustParsePrefix("10.255.0.0/16")}}
	ctx := asnCtx(tenant, PermCredentialsWrite)
	mgmt := "10.20.0.5"

	t.Run("routeros", func(t *testing.T) {
		t.Parallel()
		st := &memDirectStore{router: switchRouter(tenant, domain.PlatformRouterOS)}
		on := NewOnboarding(st, sealer, nil)
		d := NewDirectOnboarding(st, on, settings, nil, nil, now)
		yes := true
		if _, err := d.CreateSwitch(ctx, st.router.ID, SwitchOnboardingInput{ManagementAddress: &mgmt, SFlow: &yes}); !isCode(err, "VALIDATION_FAILED") {
			t.Fatalf("sFlow en RouterOS: %v", err)
		}
		res, err := d.CreateSwitch(ctx, st.router.ID, SwitchOnboardingInput{ManagementAddress: &mgmt})
		if err != nil {
			t.Fatal(err)
		}
		if res.Language != "routeros" || res.SNMPVersion != "3" || res.SNMPUser == nil || res.SFlowConfig != nil {
			t.Fatalf("resultado: %+v", res)
		}
		c, err := on.RevealSNMPCredential(context.Background(), tenant, st.router.ID)
		if err != nil || c.AuthProtocol != "sha1" || !strings.Contains(res.Config, c.PrivPassword) ||
			!strings.Contains(res.Config, "addresses=203.0.113.11 read-access=yes write-access=no") {
			t.Fatalf("credencial/config: %+v %v\n%s", c, err, res.Config)
		}
		if *st.router.ManagementAddress != mgmt || st.events[0].Type != "horus.devices.router.updated" {
			t.Fatalf("router: %+v %v", st.router, st.events)
		}
		var data map[string]any
		b, _ := json.Marshal(st.events[0].Data)
		_ = json.Unmarshal(b, &data)
		if data["device_type"] != "switch" || data["switch_platform"] != "routeros" || data["is_primary"] != false {
			t.Fatalf("payload: %v", data)
		}
	})
	t.Run("swos", func(t *testing.T) {
		t.Parallel()
		st := &memDirectStore{router: switchRouter(tenant, domain.PlatformSwOS)}
		on := NewOnboarding(st, sealer, nil)
		res, err := NewDirectOnboarding(st, on, settings, nil, nil, now).CreateSwitch(ctx, st.router.ID, SwitchOnboardingInput{ManagementAddress: &mgmt})
		if err != nil {
			t.Fatal(err)
		}
		if res.SNMPVersion != "2c" || res.SNMPUser != nil || res.Language != "swos_web" || len(res.Warnings) == 0 {
			t.Fatalf("resultado: %+v", res)
		}
		c, err := on.RevealSNMPCredential(context.Background(), tenant, st.router.ID)
		if err != nil || c.Version != "2c" || len(c.Community) != communityLen || !strings.Contains(res.Config, c.Community) ||
			st.creds[0].Kind != domain.CredentialSNMPv2c {
			t.Fatalf("comunidad: %+v %v", c, err)
		}
	})
	t.Run("vrp con sflow", func(t *testing.T) {
		t.Parallel()
		st := &memDirectStore{router: switchRouter(tenant, domain.PlatformVRP)}
		on := NewOnboarding(st, sealer, nil)
		d := NewDirectOnboarding(st, on, settings, nil, nil, now)
		yes, rate := true, 2048
		res, err := d.CreateSwitch(ctx, st.router.ID, SwitchOnboardingInput{ManagementAddress: &mgmt, SFlow: &yes, SamplingRate: &rate})
		if err != nil {
			t.Fatal(err)
		}
		if res.SFlowConfig == nil || !strings.Contains(*res.SFlowConfig, "sflow collector 1 ip 203.0.113.10 udp-port 2055") ||
			!strings.Contains(*res.SFlowConfig, "sflow flow-sampling rate 2048") || !strings.Contains(*res.SFlowConfig, "sflow agent ip 10.20.0.5") ||
			!st.router.SFlowEnabled || !strings.Contains(res.Config, "snmp-agent mib-view included horus-ro 1.0.8802.1.1.2") {
			t.Fatalf("sflow: %+v", res)
		}
		bad := 10
		if _, err := d.CreateSwitch(ctx, st.router.ID, SwitchOnboardingInput{SamplingRate: &bad}); !isCode(err, "VALIDATION_FAILED") {
			t.Fatalf("muestreo: %v", err)
		}
		// Dirección de Horus nunca.
		tun := "10.255.0.9"
		if _, err := d.CreateSwitch(ctx, st.router.ID, SwitchOnboardingInput{ManagementAddress: &tun}); !isCode(err, "VALIDATION_FAILED") {
			t.Fatalf("dirección de Horus: %v", err)
		}
	})
	t.Run("router no", func(t *testing.T) {
		t.Parallel()
		st := &memDirectStore{router: huaweiRouter(tenant)}
		d := NewDirectOnboarding(st, NewOnboarding(st, sealer, nil), settings, nil, nil, now)
		if _, err := d.CreateSwitch(ctx, st.router.ID, SwitchOnboardingInput{ManagementAddress: &mgmt}); !isCode(err, domain.CodeSwitchOnly) {
			t.Fatalf("router: %v", err)
		}
		sw := &memDirectStore{router: switchRouter(tenant, domain.PlatformVRP)}
		if _, err := NewDirectOnboarding(sw, NewOnboarding(sw, sealer, nil), settings, nil, nil, now).Create(ctx, sw.router.ID,
			DirectOnboardingInput{ManagementAddress: &mgmt}); !isCode(err, domain.CodeSwitchNotSupported) {
			t.Fatalf("alta directa de router para un switch: %v", err)
		}
		if _, err := d.CreateSwitch(asnCtx(tenant, "devices.read"), st.router.ID, SwitchOnboardingInput{}); err == nil {
			t.Fatal("sin permiso")
		}
	})
	t.Run("verificación", func(t *testing.T) {
		t.Parallel()
		r := switchRouter(tenant, domain.PlatformSwOS)
		r.ManagementAddress = &mgmt
		st := &memDirectStore{router: r}
		o := NewOnboarding(st, nil, nil)
		if err := o.VerifyDirect(context.Background(), tenant, r.ID, netip.MustParseAddr(mgmt)); err != nil ||
			st.router.OnboardingState != devapi.OnboardingSNMPVerified {
			t.Fatalf("switch verificado: %v %s", err, st.router.OnboardingState)
		}
		info, _ := o.GetRouter(context.Background(), tenant, r.ID)
		if info.DeviceType != devapi.DeviceTypeSwitch || info.SwitchPlatform != devapi.PlatformSwOS {
			t.Fatalf("RouterInfo: %+v", info)
		}
	})
}
