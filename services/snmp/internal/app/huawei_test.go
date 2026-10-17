package app

import (
	"context"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/snmpclient"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// huaweiVRP es un NE40E SINTÉTICO (docs/vendors/huawei.md §9): MPU con CPU/memoria/temperatura en
// HUAWEI-ENTITY-EXTENT-MIB, un puerto de subida, una subinterfaz, NULL0 y una prueba NQA en DISMAN-PING-MIB.
func huaweiVRP() *fakeTransport {
	hw := snmpclient.OIDHwEntityStateTable
	ent := snmpclient.OIDEntPhysicalTable
	ping := snmpclient.OIDPingResultsTable
	test := "5.104.111.114.117.115.23.103.119.45.71.105.103.97.98.105.116.69.116.104.101.114.110.101.116.49.95.48.95.48" // horus / gw-GigabitEthernet1_0_0
	return &fakeTransport{vars: map[string]any{
		snmpclient.OIDSysDescr:  "Huawei Versatile Routing Platform Software\r\nVRP (R) software, Version 8.180 (NE40E V800R011C10SPC100)\r\nHUAWEI NE40E-X8A\r\n",
		snmpclient.OIDSysUpTime: uint64(5000),
		ent + ".5.16777216":     int64(3), ent + ".7.16777216": "NE40E-X8A Frame", ent + ".11.16777216": "2102SYNTH",
		ent + ".5.16842753": int64(9), ent + ".7.16842753": "MPU 9",
		hw + ".5.16842753": int64(23), hw + ".7.16842753": int64(50), hw + ".19.16842753": int64(4096), hw + ".11.16842753": int64(44),
		snmpclient.OIDIfName + ".3": "NULL0", snmpclient.OIDIfType + ".3": int64(1),
		snmpclient.OIDIfName + ".10": "GigabitEthernet1/0/0", snmpclient.OIDIfType + ".10": int64(6), snmpclient.OIDIfHighSpeed + ".10": uint64(10000),
		snmpclient.OIDIfAdminStatus + ".10": int64(1), snmpclient.OIDIfOperStatus + ".10": int64(1), snmpclient.OIDIfAlias + ".10": "UPLINK-TRANSITO",
		snmpclient.OIDIfName + ".13": "GigabitEthernet1/0/1.100", snmpclient.OIDIfType + ".13": int64(135),
		snmpclient.OIDIfAdminStatus + ".13": int64(1), snmpclient.OIDIfOperStatus + ".13": int64(1),
		snmpclient.OIDIfHCInOctets + ".10": uint64(1000), snmpclient.OIDIfHCOutOctets + ".10": uint64(500),
		ping + ".8." + test: uint64(10), ping + ".7." + test: uint64(9), ping + ".6." + test: uint64(3), ping + ".4." + test: uint64(1),
		ping + ".5." + test: uint64(7), ping + ".9." + test: uint64(117), ping + ".3." + test: "\xcb\x00\x71\x01",
	}}
}

func TestEngineHuaweiDirectMode(t *testing.T) {
	h := newHarness(t, &memStore{})
	ctx := context.Background()
	mgmt := netip.MustParseAddr("198.51.100.20")
	rtHW := uuid.MustParse("0192e333-0000-7000-8000-0000000000a1")
	h.dev.mu.Lock()
	h.dev.routers = append(h.dev.routers,
		// Modo directo: se sondea por la dirección de gestión desde que existe, sin esperar a un túnel.
		devapi.RouterInfo{ID: rtHW, TenantID: tenantA, SiteID: siteA2, Name: "ne40e-1", Vendor: devapi.VendorHuawei,
			ConnectionMode: devapi.ConnectionDirect, OnboardingState: devapi.OnboardingPending, ManagementAddress: &mgmt},
		// Directo sin dirección de gestión todavía: no es objetivo.
		devapi.RouterInfo{ID: uuid.New(), TenantID: tenantA, SiteID: siteA, Name: "ne40e-sin-ip", Vendor: devapi.VendorHuawei,
			ConnectionMode: devapi.ConnectionDirect, OnboardingState: devapi.OnboardingPending})
	h.dev.creds[rtHW] = devapi.SNMPCredential{Username: "horus-a1", AuthProtocol: "sha256", AuthPassword: "a", PrivProtocol: "aes128", PrivPassword: "p"}
	h.dev.mu.Unlock()
	h.dial.by[mgmt] = huaweiVRP()
	ver := &fakeVerifier{}
	h.eng.o.Verifier = func() (devapi.DirectVerifier, bool) { return ver, true }
	h.eng.refreshTargets(ctx)
	if n := len(h.eng.snapshot()); n != 4 {
		t.Fatalf("objetivos = %d, quiero 4 (3 MikroTik + 1 Huawei directo)", n)
	}
	h.cycle()
	live, found, _ := h.eng.Live(ctx, tenantA, rtHW)
	if !found || live.Status.Signals.SNMP != api.SignalOK || live.Status.Signals.WireGuard != api.SignalUnknown {
		t.Fatalf("Huawei directo: %+v", live)
	}
	d := live.Status.Device
	if d.Model == nil || *d.Model != "NE40E-X8A" || d.CPUPercent == nil || *d.CPUPercent != 23 || d.MemoryPercent == nil || *d.MemoryPercent != 50 ||
		d.TemperatureCelsius == nil || *d.TemperatureCelsius != 44 || d.Firmware == nil || *d.Firmware != "V800R011C10SPC100" {
		t.Fatalf("muestra Huawei: %+v", d)
	}
	// Prueba de control: primera respuesta authPriv en la dirección de gestión → devices lo verifica (una vez).
	if len(ver.calls) != 1 || ver.calls[0] != (verifyCall{tenantA, rtHW, mgmt}) {
		t.Fatalf("verificación: %+v", ver.calls)
	}
	h.cycle()
	if len(ver.calls) != 1 {
		t.Fatalf("la verificación se reintenta como mucho cada 5 min: %+v", ver.calls)
	}
	ifs, _, _ := h.eng.Interfaces(ctx, tenantA, rtHW)
	monitored := map[string]bool{}
	for _, x := range ifs.Data {
		monitored[x.Name] = x.Monitored
	}
	if !monitored["GigabitEthernet1/0/0"] || !monitored["GigabitEthernet1/0/1.100"] || monitored["NULL0"] {
		t.Fatalf("interfaces sondeadas con el adaptador Huawei: %+v", monitored)
	}
	var snap RouterSnapshot
	for _, s := range h.eng.Snapshot(h.clock()) {
		if s.RouterID == rtHW {
			snap = s
		}
	}
	if snap.Vendor != snmpclient.VendorHuawei {
		t.Fatalf("snapshot: %+v", snap)
	}
	res, err := h.eng.ReadPings(ctx, tenantA, rtHW)
	if err != nil || len(res) != 1 {
		t.Fatalf("ReadPings: %+v %v", res, err)
	}
	samples := NQASamples(snap, res, h.clock())
	if len(samples) != 1 {
		t.Fatalf("muestras: %+v", samples)
	}
	s := samples[0]
	if s.Source != domain.ProbeSourceRouterNQA || s.Ref != domain.ProbeRefGateway || s.Target != "203.0.113.1" || *s.Sent != 10 || *s.Received != 9 ||
		*s.LossPct != 10 || *s.RTTAvgMs != 3 || s.JitterMs == nil || *s.JitterMs != 2 || // sqrt(117/9 − 9) = 2
		s.InterfaceID != api.InterfaceID(rtHW, "GigabitEthernet1/0/0") || s.Label != "Puerta de enlace por GigabitEthernet1/0/0" || !*s.Up {
		t.Fatalf("muestra NQA: %+v", s)
	}
	if _, err := h.eng.ReadPings(ctx, tenantB, rtHW); err == nil || nqaErrorText(err) != "router_not_polled" {
		t.Fatalf("aislamiento por tenant: %v", err)
	}
	// Una prueba que no sigue la convención y otra de enlace sin interfaz conocida.
	other := NQASamples(snap, []snmpclient.PingResult{{Test: "otra"}, {Test: "gw-Eth-Trunk9", Target: "192.0.2.1"}}, h.clock())
	if len(other) != 1 || other[0].InterfaceID != uuid.Nil || other[0].Up != nil {
		t.Fatalf("pruebas ajenas: %+v", other)
	}
}

type verifyCall struct {
	tenant, router uuid.UUID
	addr           netip.Addr
}

type fakeVerifier struct{ calls []verifyCall }

func (f *fakeVerifier) VerifyDirect(_ context.Context, tenant, router uuid.UUID, addr netip.Addr) error {
	f.calls = append(f.calls, verifyCall{tenant, router, addr})
	return nil
}
