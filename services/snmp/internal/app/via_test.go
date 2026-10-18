package app

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// Switches vía router (ADR-0032): se sondean por su DIRECCIÓN HORUS (nunca
// por la IP privada, que otro ISP puede repetir), sin ICMP (el router solo
// deja pasar SNMP), con prueba de control por esa dirección; si el túnel del
// router del sitio cae, el switch queda «unreachable / via_router_tunnel_down»
// en lugar de «offline».
func TestEngineViaRouterSwitch(t *testing.T) {
	h := newHarness(t, &memStore{})
	ctx := context.Background()
	horusA, horusB := netip.MustParseAddr("10.255.1.40"), netip.MustParseAddr("10.255.1.41")
	private := netip.MustParseAddr("192.168.88.2") // la misma IP privada en los dos ISP
	swA := uuid.MustParse("0192e333-0000-7000-8000-0000000000c1")
	swB := uuid.MustParse("0192e333-0000-7000-8000-0000000000c2")
	pending := uuid.MustParse("0192e333-0000-7000-8000-0000000000c3")
	viaA, viaB := rtA1, rtB1
	h.dev.mu.Lock()
	h.dev.routers = append(h.dev.routers,
		devapi.RouterInfo{ID: swA, TenantID: tenantA, SiteID: siteA, Name: "sw-a", Vendor: devapi.VendorMikroTik, DeviceType: devapi.DeviceTypeSwitch,
			SwitchPlatform: devapi.PlatformRouterOS, ConnectionMode: devapi.ConnectionViaRouter, OnboardingState: devapi.OnboardingPending,
			ManagementAddress: &private, ViaRouterID: &viaA, ViaAddress: &horusA},
		devapi.RouterInfo{ID: swB, TenantID: tenantB, SiteID: siteB, Name: "sw-b", Vendor: devapi.VendorMikroTik, DeviceType: devapi.DeviceTypeSwitch,
			SwitchPlatform: devapi.PlatformRouterOS, ConnectionMode: devapi.ConnectionViaRouter, OnboardingState: devapi.OnboardingSNMPVerified,
			ManagementAddress: &private, ViaRouterID: &viaB, ViaAddress: &horusB},
		// Vía router sin dirección Horus todavía: no es objetivo (nunca se sondea la privada).
		devapi.RouterInfo{ID: pending, TenantID: tenantA, SiteID: siteA, Name: "sw-sin-camino", Vendor: devapi.VendorMikroTik,
			DeviceType: devapi.DeviceTypeSwitch, SwitchPlatform: devapi.PlatformRouterOS, ConnectionMode: devapi.ConnectionViaRouter,
			OnboardingState: devapi.OnboardingPending, ManagementAddress: &private, ViaRouterID: &viaA})
	cred := devapi.SNMPCredential{Username: "horus-sw", AuthProtocol: "sha1", AuthPassword: "a", PrivProtocol: "aes128", PrivPassword: "p"}
	h.dev.creds[swA], h.dev.creds[swB] = cred, cred
	h.dev.mu.Unlock()
	h.dial.by[horusA], h.dial.by[horusB] = routerOS(1000, 10), routerOS(1000, 10)
	ver := &fakeVerifier{}
	h.eng.o.Verifier = func() (devapi.DirectVerifier, bool) { return ver, true }
	h.eng.refreshTargets(ctx)
	targets := map[uuid.UUID]netip.Addr{}
	for _, r := range h.eng.snapshot() {
		targets[r.RouterID] = r.Tunnel
	}
	if targets[swA] != horusA || targets[swB] != horusB {
		t.Fatalf("objetivos por dirección Horus: %v", targets)
	}
	if _, ok := targets[pending]; ok {
		t.Fatal("un switch vía router sin dirección Horus no se sondea")
	}
	for _, a := range targets {
		if a == private {
			t.Fatal("nunca se sondea la IP privada")
		}
	}
	h.cycle()
	live, found, _ := h.eng.Live(ctx, tenantA, swA)
	if !found || live.Status.Status != api.StatusOnline || live.Status.Signals.ICMP != api.SignalUnknown || live.Status.Signals.SNMP != api.SignalOK {
		t.Fatalf("switch vía router: %+v", live.Status)
	}
	if h.ping.count(horusA) != 0 {
		t.Fatal("sin ICMP a un switch vía router")
	}
	// Prueba de control solo del pendiente, por su dirección Horus.
	if len(ver.calls) != 1 || ver.calls[0] != (verifyCall{tenantA, swA, horusA}) {
		t.Fatalf("verificación: %+v", ver.calls)
	}
	// El otro ISP no ve el switch de A.
	if _, found, _ := h.eng.Live(ctx, tenantB, swA); found {
		t.Fatal("aislamiento por tenant")
	}
	// Túnel del router del sitio caído y el switch sin respuesta → unreachable, no offline.
	h.eng.SetWireGuard(tenantA, viaA, api.SignalStale, h.clock())
	h.dial.errs[horusA] = errors.New("request timeout")
	h.cycle()
	h.cycle()
	live, _, _ = h.eng.Live(ctx, tenantA, swA)
	if live.Status.Status != api.StatusUnreachable || live.Status.Reason == nil || *live.Status.Reason != api.ReasonViaRouterTunnelDown {
		t.Fatalf("túnel del router caído: %+v", live.Status)
	}
	// El switch de B (su router con túnel sano) no se ve afectado.
	liveB, _, _ := h.eng.Live(ctx, tenantB, swB)
	if liveB.Status.Status != api.StatusOnline {
		t.Fatalf("switch de B: %+v", liveB.Status)
	}
}
