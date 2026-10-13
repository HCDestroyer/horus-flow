package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
)

func sig(icmp, snmp, wg string) api.Signals {
	return api.Signals{ICMP: icmp, SNMP: snmp, WireGuard: wg}
}

// Tabla de architecture.md §10.4 y api.md §2.6.
func TestCandidate(t *testing.T) {
	t.Parallel()
	silent := "silent"
	cases := []struct {
		name          string
		o             Observation
		state, reason string
	}{
		{"todo ✗ + WG stale", Observation{Signals: sig("fail", "fail", "stale")}, api.StatusOffline, api.ReasonTunnelDown},
		{"todo ✗ + WG never", Observation{Signals: sig("fail", "fail", "never")}, api.StatusOffline, api.ReasonTunnelDown},
		{"todo ✗ + WG ok", Observation{Signals: sig("fail", "fail", "ok")}, api.StatusOffline, api.ReasonHostUnreachable},
		{"todo ✗ + WG unknown", Observation{Signals: sig("fail", "fail", "unknown")}, api.StatusOffline, api.ReasonHostUnreachable},
		{"ICMP ✗ sin credenciales + WG ok", Observation{Signals: sig("fail", "unknown", "ok"), NoCredentials: true}, api.StatusOffline, api.ReasonHostUnreachable},
		{"ICMP ✓ + SNMP ✗", Observation{Signals: sig("ok", "fail", "ok")}, api.StatusDegraded, api.ReasonSNMPUnreachable},
		{"ICMP ✓ + auth", Observation{Signals: sig("ok", "auth_failed", "ok")}, api.StatusDegraded, api.ReasonSNMPAuthFailed},
		{"ICMP ✓ sin credenciales", Observation{Signals: sig("ok", "unknown", "ok"), NoCredentials: true}, api.StatusDegraded, api.ReasonNoCredentials},
		{"sin ICMP, SNMP ✗, WG stale", Observation{Signals: sig("unknown", "fail", "stale")}, api.StatusOffline, api.ReasonTunnelDown},
		{"sin ICMP, SNMP ✗, WG ok", Observation{Signals: sig("unknown", "fail", "ok")}, api.StatusDegraded, api.ReasonSNMPUnreachable},
		{"todo ✓", Observation{Signals: sig("ok", "ok", "ok")}, api.StatusOnline, api.ReasonOK},
		{"SNMP ✓ aunque ICMP ✗ (ICMP filtrado)", Observation{Signals: sig("fail", "ok", "ok")}, api.StatusOnline, api.ReasonOK},
		{"warning cpu", Observation{Signals: sig("ok", "ok", "ok"), Hits: []api.ThresholdHit{{Metric: "cpu_percent", Level: "warning"}}}, api.StatusWarning, api.ReasonCPUHigh},
		{"critical temperatura gana a warning cpu", Observation{Signals: sig("ok", "ok", "ok"), Hits: []api.ThresholdHit{{Metric: "cpu_percent", Level: "warning"}, {Metric: "temperature_celsius", Level: "critical"}}}, api.StatusCritical, api.ReasonTemperatureHigh},
		{"memoria critical", Observation{Signals: sig("ok", "ok", "ok"), Hits: []api.ThresholdHit{{Metric: "memory_percent", Level: "critical"}}}, api.StatusCritical, api.ReasonMemoryHigh},
		{"uplink caído", Observation{Signals: sig("ok", "ok", "ok"), UplinkDown: true}, api.StatusWarning, api.ReasonInterfaceDown},
		{"exportador silencioso solo si todo lo demás bien", Observation{Signals: api.Signals{ICMP: "ok", SNMP: "ok", WireGuard: "ok", Exporter: &silent}}, api.StatusWarning, api.ReasonExporterSilent},
		{"exportador silencioso no tapa degraded", Observation{Signals: api.Signals{ICMP: "ok", SNMP: "fail", WireGuard: "ok", Exporter: &silent}}, api.StatusDegraded, api.ReasonSNMPUnreachable},
	}
	for _, c := range cases {
		s, r := Candidate(c.o)
		if s != c.state || r != c.reason {
			t.Errorf("%s: = %s/%s, quiero %s/%s", c.name, s, r, c.state, c.reason)
		}
	}
}

func TestMachineHysteresis(t *testing.T) {
	t.Parallel()
	var m Machine
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	tr := m.Observe(api.StatusOnline, api.ReasonOK, t0)
	if !tr.Changed || m.State != api.StatusOnline || m.Version != 1 || m.Since == nil || !m.Since.Equal(t0) {
		t.Fatalf("primera observación debe adoptarse: %+v %+v", tr, m)
	}
	// Una sola observación distinta no cambia nada.
	tr = m.Observe(api.StatusOffline, api.ReasonTunnelDown, t0.Add(time.Minute))
	if tr.Changed || m.State != api.StatusOnline || m.Pending != api.StatusOffline || m.PendingCount != 1 {
		t.Fatalf("1ª offline no debe cambiar: %+v %+v", tr, m)
	}
	// Vuelve online: se descarta el pendiente.
	m.Observe(api.StatusOnline, api.ReasonOK, t0.Add(2*time.Minute))
	if m.Pending != "" || m.PendingCount != 0 {
		t.Fatalf("pendiente no reiniciado: %+v", m)
	}
	m.Observe(api.StatusOffline, api.ReasonTunnelDown, t0.Add(3*time.Minute))
	tr = m.Observe(api.StatusOffline, api.ReasonTunnelDown, t0.Add(4*time.Minute))
	if !tr.Changed || tr.PreviousState != api.StatusOnline || m.State != api.StatusOffline || m.Version != 2 || !m.Since.Equal(t0.Add(4*time.Minute)) {
		t.Fatalf("2ª offline seguida debe cambiar: %+v %+v", tr, m)
	}
	// Cambio de razón dentro del mismo estado: inmediato y sin mover since.
	tr = m.Observe(api.StatusOffline, api.ReasonHostUnreachable, t0.Add(5*time.Minute))
	if tr.Changed || m.Reason != api.ReasonHostUnreachable || !m.Since.Equal(t0.Add(4*time.Minute)) {
		t.Fatalf("razón: %+v %+v", tr, m)
	}
}

func TestStreaksSustained(t *testing.T) {
	t.Parallel()
	var s Streaks
	th := DefaultThresholds
	cpu, temp := 90.0, 72.0
	d := api.DeviceSample{CPUPercent: &cpu, TemperatureCelsius: &temp}
	h := s.Evaluate(th, d)
	if len(h) != 1 || h[0].Metric != "temperature_celsius" || h[0].Level != "warning" {
		t.Fatalf("ciclo 1: temperatura inmediata, cpu aún no: %+v", h)
	}
	s.Evaluate(th, d)
	h = s.Evaluate(th, d)
	if len(h) != 2 || h[0].Metric != "cpu_percent" || h[0].Level != "warning" || h[0].Threshold != 80 {
		t.Fatalf("ciclo 3: cpu sostenida: %+v", h)
	}
	cpu = 97
	for range 2 {
		h = s.Evaluate(th, d)
	}
	if h[0].Level != "warning" {
		t.Fatalf("critical necesita 3 ciclos: %+v", h)
	}
	h = s.Evaluate(th, d)
	if h[0].Level != "critical" || h[0].Threshold != 95 {
		t.Fatalf("ciclo 3 sobre 95: %+v", h)
	}
	cpu = 10
	if h = s.Evaluate(th, d); len(h) != 1 {
		t.Fatalf("baja la cpu: la racha se corta: %+v", h)
	}
	d.TemperatureCelsius = nil
	if h = s.Evaluate(th, d); len(h) != 0 {
		t.Fatalf("sensor ausente = sin umbral: %+v", h)
	}
}

func TestDelta(t *testing.T) {
	t.Parallel()
	t0 := time.Now()
	prev := Counters{At: t0, Uptime: 1000, InOctets: 1000, OutOctets: 500, InUcast: 10, OutUcast: 5, InErrors: 1, InDiscards: 2}
	cur := Counters{At: t0.Add(60 * time.Second), Uptime: 7000, InOctets: 61000, OutOctets: 30500, InUcast: 610, OutUcast: 305, InErrors: 3, InDiscards: 2}
	if r := Delta(nil, cur, 0); !r.Discontinuity {
		t.Fatal("primer sondeo = discontinuidad")
	}
	r := Delta(&prev, cur, 1_000_000)
	if r.Discontinuity || *r.RxBps != 8000 || *r.TxBps != 4000 || *r.RxPps != 10 || *r.TxPps != 5 || *r.RxErrors != 2 || *r.RxDrops != 0 {
		t.Fatalf("tasas: %+v", r)
	}
	if *r.UtilRx != 0.008 || *r.UtilTx != 0.004 {
		t.Fatalf("utilización: %v %v", *r.UtilRx, *r.UtilTx)
	}
	// Reinicio: uptime menor.
	reb := cur
	reb.Uptime = 10
	if r := Delta(&prev, reb, 0); !r.Discontinuity {
		t.Fatal("uptime menor = discontinuidad")
	}
	// Contador que retrocede (wrap de Counter32 en errores).
	wrap := cur
	wrap.InErrors = 0
	if r := Delta(&prev, wrap, 0); !r.Discontinuity {
		t.Fatal("contador que retrocede = discontinuidad")
	}
	// Sin velocidad: sin utilización; utilización saturada se acota a 1.
	if r := Delta(&prev, cur, 0); r.UtilRx != nil {
		t.Fatal("sin velocidad no hay utilización")
	}
	if r := Delta(&prev, cur, 10); *r.UtilRx != 1 {
		t.Fatalf("clamp: %v", *r.UtilRx)
	}
}

func TestMonitoredAndUplink(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		typ  int
		name string
		want bool
	}{
		"ether": {IfTypeEthernet, "ether1", true}, "sfp": {IfTypeEthernet, "sfp-sfpplus1", true}, "wlan": {IfTypeWLAN, "wlan1", true},
		"vlan": {IfTypeL2VLAN, "vlan100", true}, "bridge": {IfTypeBridge, "bridge", true}, "bond": {IfTypeLAG, "bonding1", true},
		"pppoe dinámica": {IfTypePPP, "<pppoe-juan>", false}, "pppoe ether type": {IfTypeEthernet, "<pppoe-ana>", false},
		"ppp": {IfTypePPP, "ppp-out1", false}, "túnel": {IfTypeTunnel, "wg-horus", false}, "lo": {IfTypeLoopback, "lo", false},
		"pppoe-client": {IfTypeEthernet, "pppoe-out1", false},
	} {
		if got := Monitored(c.typ, c.name); got != c.want {
			t.Errorf("%s: Monitored = %v", name, got)
		}
	}
	tx := func(v float64) *float64 { return &v }
	cands := []UplinkCandidate{
		{IfIndex: 1, IfType: IfTypeEthernet, Name: "ether1", OperUp: true, AvgTxBps: tx(900e6)},
		{IfIndex: 2, IfType: IfTypeEthernet, Name: "sfp-sfpplus1", OperUp: true, AvgTxBps: tx(100e6)},
		{IfIndex: 3, IfType: IfTypeBridge, Name: "bridge", OperUp: true, AvgTxBps: tx(5e9)},
	}
	if got := ChooseUplink(cands); got != 1 {
		t.Fatalf("mayor tx (sin bridge) = %d", got)
	}
	cands[1].Alias = "UPLINK-TRANSIT-A"
	if got := ChooseUplink(cands); got != 2 {
		t.Fatalf("alias UPLINK gana = %d", got)
	}
	cands[0].Upstream = true
	if got := ChooseUplink(cands); got != 1 {
		t.Fatalf("flow_role upstream gana al alias = %d", got)
	}
	if ChooseUplink([]UplinkCandidate{{IfIndex: 9, IfType: IfTypeEthernet, Name: "ether9"}}) != 0 {
		t.Fatal("interfaz caída y sin tráfico ni alias no es uplink")
	}
	if ChooseUplink(nil) != 0 {
		t.Fatal("sin candidatas = 0")
	}
}

func TestEffectiveAndSort(t *testing.T) {
	t.Parallel()
	now := time.Now()
	iv := time.Minute
	if st, _ := Effective("", nil, nil, now, iv, true); st != api.StatusUnknown {
		t.Fatalf("nunca observado = unknown, got %s", st)
	}
	recent := now.Add(-time.Minute)
	if st, since := Effective(api.StatusOnline, &recent, &recent, now, iv, true); st != api.StatusOnline || !since.Equal(recent) {
		t.Fatalf("observado fresco = online, got %s", st)
	}
	old := now.Add(-4 * time.Minute)
	if st, since := Effective(api.StatusOnline, &old, &old, now, iv, true); st != api.StatusStale || !since.Equal(old.Add(3*time.Minute)) {
		t.Fatalf("> 3 intervalos = stale desde el límite, got %s %v", st, since)
	}
	if st, _ := Effective(api.StatusOffline, &recent, &recent, now, iv, false); st != api.StatusStale {
		t.Fatalf("poller sin latido = stale, got %s", st)
	}
	if PollerHealthy(nil, now, iv) || !PollerHealthy(&recent, now, iv) || PollerHealthy(&old, now, iv) {
		t.Fatal("PollerHealthy")
	}
	rows := []api.RouterStatus{
		{RouterID: uuid.New(), Name: "zeta", Status: api.StatusOnline}, {RouterID: uuid.New(), Name: "alfa", Status: api.StatusOnline},
		{RouterID: uuid.New(), Name: "b", Status: api.StatusUnknown}, {RouterID: uuid.New(), Name: "c", Status: api.StatusStale},
		{RouterID: uuid.New(), Name: "d", Status: api.StatusWarning}, {RouterID: uuid.New(), Name: "e", Status: api.StatusDegraded},
		{RouterID: uuid.New(), Name: "f", Status: api.StatusOffline}, {RouterID: uuid.New(), Name: "g", Status: api.StatusCritical},
	}
	SortProblemsFirst(rows)
	var got []string
	for _, r := range rows {
		got = append(got, r.Status+":"+r.Name)
	}
	want := []string{"critical:g", "offline:f", "degraded:e", "warning:d", "stale:c", "unknown:b", "online:alfa", "online:zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orden = %v, quiero %v", got, want)
		}
	}
}
