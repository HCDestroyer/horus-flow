package app

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/snmpclient"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

var (
	swA1  = uuid.MustParse("0192e333-0000-7000-8000-0000000000b1")
	ipSw1 = netip.MustParseAddr("10.20.0.5")
)

// switchTransport: CRS con RouterOS, 3 puertos ethernet + una VLAN; ether2 tiene por vecino LLDP al router
// core-1 (equipo de Horus del mismo ISP), ether1 tiene descripción, ether3 nada.
func switchTransport() *fakeTransport {
	v := map[string]any{snmpclient.OIDSysDescr: "RouterOS CRS326-24G-2S+", snmpclient.OIDSysUpTime: uint64(1000)}
	for i, p := range []struct {
		name, alias string
		typ         int64
	}{{"ether1", "Cliente edificio A", 6}, {"ether2", "", 6}, {"ether3", "", 6}, {"vlan10", "gestion", 135}} {
		idx := "." + string(rune('1'+i))
		v[snmpclient.OIDIfName+idx], v[snmpclient.OIDIfAlias+idx], v[snmpclient.OIDIfType+idx] = p.name, p.alias, p.typ
		v[snmpclient.OIDIfHighSpeed+idx], v[snmpclient.OIDIfAdminStatus+idx], v[snmpclient.OIDIfOperStatus+idx] = uint64(1000), int64(1), int64(1)
		for _, o := range []string{snmpclient.OIDIfHCInOctets, snmpclient.OIDIfHCOutOctets, snmpclient.OIDIfHCInUcast, snmpclient.OIDIfHCOutUcast} {
			v[o+idx] = uint64(1000)
		}
		for _, o := range []string{snmpclient.OIDIfInErrors, snmpclient.OIDIfOutErrors, snmpclient.OIDIfInDiscards, snmpclient.OIDIfOutDiscards} {
			v[o+idx] = uint64(0)
		}
	}
	// LLDP-MIB: puerto local 2 (= ether2) con vecino core-1.
	v[snmpclient.OIDLldpLocPortTable+".2.2"] = int64(5)
	v[snmpclient.OIDLldpLocPortTable+".3.2"] = "ether2"
	v[snmpclient.OIDLldpRemTable+".7.0.2.1"] = "sfp-sfpplus1"
	v[snmpclient.OIDLldpRemTable+".9.0.2.1"] = "core-1.isp.example"
	return &fakeTransport{vars: v}
}

func addSwitch(h *harness, platform string, cred devapi.SNMPCredential) *fakeTransport {
	addr := ipSw1
	h.dev.mu.Lock()
	h.dev.routers = append(h.dev.routers, devapi.RouterInfo{ID: swA1, TenantID: tenantA, SiteID: siteA, Name: "sw-core-1",
		OnboardingState: devapi.OnboardingSNMPVerified, Vendor: devapi.VendorMikroTik, ConnectionMode: devapi.ConnectionDirect,
		ManagementAddress: &addr, DeviceType: devapi.DeviceTypeSwitch, SwitchPlatform: platform})
	h.dev.creds[swA1] = cred
	h.dev.mu.Unlock()
	tr := switchTransport()
	h.dial.mu.Lock()
	h.dial.by[ipSw1] = tr
	h.dial.mu.Unlock()
	h.eng.refreshTargets(context.Background())
	return tr
}

func portsByName(t *testing.T, e *Engine) map[string]api.SwitchPort {
	t.Helper()
	ps, ok, err := e.SwitchPorts(context.Background(), tenantA, swA1)
	if err != nil || !ok {
		t.Fatalf("SwitchPorts: %v %v", ok, err)
	}
	out := map[string]api.SwitchPort{}
	for _, p := range ps.Data {
		out[p.Name] = p
	}
	return out
}

func TestSwitchPollingAndPorts(t *testing.T) {
	h := newHarness(t, &memStore{})
	ctx := context.Background()
	tr := addSwitch(h, devapi.PlatformRouterOS, devapi.SNMPCredential{Version: "3", Username: "horus-b1", AuthProtocol: "sha1",
		AuthPassword: "a", PrivProtocol: "aes128", PrivPassword: "p"})
	h.cycle()

	// La lista de routers no cambia (ni sus objetivos); el switch va en la suya.
	rows, health, _ := h.eng.List(ctx, tenantA, nil)
	if len(rows) != 2 || health.Targets != 2 {
		t.Fatalf("routers: %d filas, %d objetivos", len(rows), health.Targets)
	}
	sws, sh, err := h.eng.ListSwitches(ctx, tenantA, nil)
	if err != nil || len(sws) != 1 || sh.Targets != 1 || sws[0].DeviceType != "switch" || sws[0].Platform != "routeros" || sws[0].Status != api.StatusOnline {
		t.Fatalf("switches: %+v %+v %v", sws, sh, err)
	}
	if other, _, _ := h.eng.ListSwitches(ctx, tenantB, nil); len(other) != 0 {
		t.Fatal("switch visible desde otro ISP")
	}
	if _, ok, _ := h.eng.SwitchPorts(ctx, tenantB, swA1); ok {
		t.Fatal("puertos visibles desde otro ISP")
	}
	if _, ok, _ := h.eng.SwitchPorts(ctx, tenantA, rtA1); ok {
		t.Fatal("un router no tiene puertos de switch")
	}

	// Elección por defecto: ether2 (vecino conocido → uplink sugerido) y ether1 (descripción); ether3 no; la
	// VLAN no es un puerto del switch.
	p := portsByName(t, h.eng)
	if !p["ether2"].Monitored || !p["ether2"].Uplink || !p["ether2"].SuggestedUplink || len(p["ether2"].Neighbors) != 1 ||
		p["ether2"].Neighbors[0].KnownDevice == nil || *p["ether2"].Neighbors[0].KnownDevice != "core-1" {
		t.Fatalf("ether2: %+v", p["ether2"])
	}
	if !p["ether1"].Monitored || p["ether1"].Uplink || p["ether3"].Monitored {
		t.Fatalf("elección por defecto: %+v / %+v", p["ether1"], p["ether3"])
	}
	if _, ok := p["vlan10"]; ok {
		t.Fatal("la VLAN no es un puerto del switch")
	}
	h.cycle()
	if p := portsByName(t, h.eng); p["ether2"].Sample == nil || p["ether1"].Sample == nil || p["ether3"].Sample != nil {
		t.Fatalf("muestras: ether2=%v ether1=%v ether3=%v", p["ether2"].Sample, p["ether1"].Sample, p["ether3"].Sample)
	}

	// Elección del operador: ether3 sondeado y de subida; ether1 fuera. Se aplica en caliente.
	h.eng.SetPortSelection(swA1, &domain.PortSelection{RouterID: swA1, Version: 1, Ports: map[string]domain.PortChoice{
		"ether2": {Monitored: true, Uplink: true}, "ether3": {Monitored: true, Uplink: true}}})
	h.cycle()
	p = portsByName(t, h.eng)
	if !p["ether3"].Monitored || !p["ether3"].Uplink || p["ether1"].Monitored || p["ether1"].Sample != nil {
		t.Fatalf("elección del operador: %+v / %+v", p["ether3"], p["ether1"])
	}
	// Capacidad: los dos uplinks del switch entran como enlaces de subida.
	var snap *RouterSnapshot
	for _, s := range h.eng.Snapshot(h.clock()) {
		if s.RouterID == swA1 {
			snap = &s
		}
	}
	ups := 0
	for _, i := range snap.Interfaces {
		if i.AutoUplink {
			ups++
		}
	}
	if snap == nil || !snap.Switch || ups != 2 {
		t.Fatalf("snapshot: %+v", snap)
	}

	// Flaps: el uplink ether3 cambia de estado 4 veces en < 1 h → interface_flapping (aviso); cada cambio de
	// un uplink publica oper_status_changed con device_type switch.
	before := len(h.events.types())
	for i := 0; i < 4; i++ {
		st := int64(2)
		if i%2 == 1 {
			st = 1
		}
		tr.set(snmpclient.OIDIfOperStatus+".3", st)
		h.cycle()
	}
	var changes int
	for _, typ := range h.events.types()[before:] {
		if typ == TypeOperStatusChanged {
			changes++
		}
	}
	ev := h.events.last(TypeOperStatusChanged)
	if changes != 4 || ev == nil || ev.Data.(map[string]any)["device_type"] != "switch" || ev.Data.(map[string]any)["uplink"] != true {
		t.Fatalf("eventos de uplink: %d %+v", changes, ev)
	}
	p = portsByName(t, h.eng)
	if p["ether3"].Flaps1h != 4 || p["ether3"].LastChangeAt == nil {
		t.Fatalf("flaps: %+v", p["ether3"])
	}
	h.cycle() // histéresis
	sws, _, _ = h.eng.ListSwitches(ctx, tenantA, nil)
	if sws[0].Status != api.StatusWarning || sws[0].Reason == nil || *sws[0].Reason != api.ReasonInterfaceFlapping || sws[0].PortsFlapping != 1 {
		t.Fatalf("switch inestable: %+v", sws[0])
	}

	// Un puerto de acceso que cae no avisa (solo registra el flap).
	h.eng.SetPortSelection(swA1, &domain.PortSelection{RouterID: swA1, Version: 2, Ports: map[string]domain.PortChoice{
		"ether1": {Monitored: true}}})
	h.cycle()
	before = len(h.events.types())
	tr.set(snmpclient.OIDIfOperStatus+".1", int64(2))
	h.cycle()
	for _, typ := range h.events.types()[before:] {
		if typ == TypeOperStatusChanged {
			t.Fatal("un puerto de acceso no debe publicar oper_status_changed")
		}
	}
	if p := portsByName(t, h.eng); p["ether1"].Flaps1h != 1 {
		t.Fatalf("flap de acceso: %+v", p["ether1"])
	}
	nb, ok, _ := h.eng.SwitchNeighbors(ctx, tenantA, swA1)
	if !ok || len(nb.Data) != 1 || nb.Data[0].SysName != "core-1.isp.example" {
		t.Fatalf("vecinos: %+v", nb)
	}
}

// SwOS: comunidad v2c (Version "2c" llega al dialer) y sin CPU/memoria (null, sin error).
func TestSwitchSwOSv2c(t *testing.T) {
	h := newHarness(t, &memStore{})
	addSwitch(h, devapi.PlatformSwOS, devapi.SNMPCredential{Version: "2c", Community: "CommunityTestValue2345"})
	h.cycle()
	var seen bool
	h.dial.mu.Lock()
	for _, c := range h.dial.seen {
		if c.Version == "2c" && c.Community == "CommunityTestValue2345" && c.Username == "" {
			seen = true
		}
	}
	h.dial.mu.Unlock()
	if !seen {
		t.Fatal("la comunidad v2c no llegó al dialer")
	}
	sws, _, _ := h.eng.ListSwitches(context.Background(), tenantA, nil)
	if len(sws) != 1 || sws[0].Status != api.StatusOnline || sws[0].Device.CPUPercent != nil || sws[0].Platform != "swos" {
		t.Fatalf("switch SwOS: %+v", sws)
	}
}

type memSwitchRepo struct {
	sel map[uuid.UUID]*domain.PortSelection
}

func (m *memSwitchRepo) PutPortSelection(_ context.Context, _ uuid.UUID, id uuid.UUID, ports map[string]domain.PortChoice, actor string,
	expect int) (*domain.PortSelection, error) {
	cur := m.sel[id]
	if expect != 0 && (cur == nil || cur.Version != expect) {
		return nil, ErrSwitchVersion
	}
	v := 1
	if cur != nil {
		v = cur.Version + 1
	}
	p := &domain.PortSelection{RouterID: id, Ports: ports, Version: v, UpdatedBy: &actor}
	m.sel[id] = p
	return p, nil
}

func (m *memSwitchRepo) DeletePortSelection(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
	delete(m.sel, id)
	return nil
}

func TestSwitchesPutPorts(t *testing.T) {
	h := newHarness(t, &memStore{})
	ctx := context.Background()
	addSwitch(h, devapi.PlatformRouterOS, devapi.SNMPCredential{Version: "3", Username: "horus-b1", AuthProtocol: "sha1",
		AuthPassword: "a", PrivProtocol: "aes128", PrivPassword: "p"})
	h.cycle()
	repo := &memSwitchRepo{sel: map[uuid.UUID]*domain.PortSelection{}}
	s := &Switches{Engine: h.eng, Repo: repo}
	// Validación: puerto inexistente, repetido, demasiados.
	_, _, errs, err := s.PutPorts(ctx, tenantA, nil, swA1, PortSelectionInput{Ports: []PortInput{{Name: "ether9"}, {Name: "ether1"}, {Name: "ether1"}}},
		"user:x", 0)
	if err != nil || len(errs) != 2 {
		t.Fatalf("validación: %v %v", errs, err)
	}
	many := PortSelectionInput{}
	for i := 0; i < domain.MaxSwitchPorts+1; i++ {
		many.Ports = append(many.Ports, PortInput{Name: "ether1"})
	}
	if _, _, errs, _ := s.PutPorts(ctx, tenantA, nil, swA1, many, "user:x", 0); len(errs) == 0 || errs[0].Code != "TOO_MANY" {
		t.Fatalf("máximo de puertos: %v", errs)
	}
	// Otro ISP o fuera del alcance por nodo: no encontrado.
	if _, _, _, err := s.PutPorts(ctx, tenantB, nil, swA1, PortSelectionInput{Ports: []PortInput{}}, "user:x", 0); err != ErrSwitchNotFound {
		t.Fatalf("otro ISP: %v", err)
	}
	if _, _, _, err := s.PutPorts(ctx, tenantA, []uuid.UUID{siteA2}, swA1, PortSelectionInput{Ports: []PortInput{}}, "user:x", 0); err != ErrSwitchNotFound {
		t.Fatalf("fuera de alcance: %v", err)
	}
	out, sel, errs, err := s.PutPorts(ctx, tenantA, nil, swA1, PortSelectionInput{Ports: []PortInput{{Name: "ether3", Uplink: true}}}, "user:x", 0)
	if err != nil || errs != nil || sel.Version != 1 || out.Selection.Version == nil || !out.Selection.Configured || out.MonitoredCount != 1 {
		t.Fatalf("guardar: %+v %+v %v %v", out, sel, errs, err)
	}
	if _, _, _, err := s.PutPorts(ctx, tenantA, nil, swA1, PortSelectionInput{Ports: []PortInput{}}, "user:x", 7); err != ErrSwitchVersion {
		t.Fatalf("If-Match: %v", err)
	}
	out, err = s.ResetPorts(ctx, tenantA, nil, swA1)
	if err != nil || out.Selection.Configured || out.MonitoredCount != 2 {
		t.Fatalf("volver a la elección por defecto: %+v %v", out, err)
	}
	// Sin PostgreSQL: 503; sin ClickHouse: sin top.
	if _, _, _, err := (&Switches{Engine: h.eng}).PutPorts(ctx, tenantA, nil, swA1, PortSelectionInput{Ports: []PortInput{}}, "u", 0); err != ErrSwitchNoStore {
		t.Fatalf("sin PostgreSQL: %v", err)
	}
	if _, err := s.Top(ctx, tenantA, nil, swA1, 0, domain.SwitchTopSrc, time.Hour, 10); err != ErrSwitchNoFlows {
		t.Fatalf("sin ClickHouse: %v", err)
	}
}

type memSwitchFlows struct{ by string }

func (m *memSwitchFlows) SwitchTop(_ context.Context, _, _ uuid.UUID, ifIndex uint32, by string, from, to time.Time, limit int) (
	[]domain.SwitchTopRow, uint64, uint64, error) {
	m.by = by
	return []domain.SwitchTopRow{{Key: "10.20.1.9", Bytes: 4096000, Packets: 8192}}, 4096000, 1, nil
}

func TestSwitchesTop(t *testing.T) {
	h := newHarness(t, &memStore{})
	addSwitch(h, devapi.PlatformVRP, devapi.SNMPCredential{Version: "3", Username: "horus-b1", AuthProtocol: "sha256",
		AuthPassword: "a", PrivProtocol: "aes128", PrivPassword: "p"})
	h.cycle()
	f := &memSwitchFlows{}
	s := &Switches{Engine: h.eng, Flows: func() (SwitchFlows, bool) { return f, true }, Now: h.clock}
	out, err := s.Top(context.Background(), tenantA, nil, swA1, 54, domain.SwitchTopProtocol, time.Hour, 10)
	if err != nil || f.by != domain.SwitchTopProtocol || out.Source != "secondary" || len(out.Data) != 1 || out.IfIndex == nil || *out.IfIndex != 54 ||
		out.To.Sub(out.From) != time.Hour {
		t.Fatalf("top: %+v %v", out, err)
	}
}
