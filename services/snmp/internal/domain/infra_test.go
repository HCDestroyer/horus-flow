package domain

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func ptr[T any](v T) *T { return &v }

func TestCheckHysteresis(t *testing.T) {
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	st := NewCheckStatus(Check{ID: uuid.New(), Type: CheckTCP})
	up := CheckObservation{State: StateUp, Reason: ReasonOK, LatencyMs: ptr(1.5)}
	down := CheckObservation{State: StateDown, Reason: ReasonTimeout}
	// Desde unknown basta un éxito.
	if !st.Apply(up, 3, 2, at) || st.State != StateUp {
		t.Fatalf("unknown → up con un éxito: %+v", st)
	}
	// Dos fallos no bastan (failures_before_down = 3).
	for i := range 2 {
		if st.Apply(down, 3, 2, at.Add(time.Duration(i+1)*time.Minute)) {
			t.Fatalf("cambió con %d fallos", i+1)
		}
	}
	if st.State != StateUp || st.ConsecutiveFailures != 2 {
		t.Fatalf("estado %s, fallos %d", st.State, st.ConsecutiveFailures)
	}
	if !st.Apply(down, 3, 2, at.Add(3*time.Minute)) || st.State != StateDown || *st.Reason != ReasonTimeout {
		t.Fatalf("no pasó a down al tercer fallo: %+v", st)
	}
	// Recuperación tras recoveries_before_up = 2 éxitos.
	if st.Apply(up, 3, 2, at.Add(4*time.Minute)) {
		t.Fatal("recuperó con un solo éxito")
	}
	if !st.Apply(up, 3, 2, at.Add(5*time.Minute)) || st.State != StateUp {
		t.Fatalf("no recuperó: %+v", st)
	}
	if st.Since == nil || !st.Since.Equal(at.Add(5*time.Minute)) {
		t.Fatalf("since = %v", st.Since)
	}
	// Fallos alternos de distinto tipo reinician la cuenta.
	st.Apply(down, 2, 1, at.Add(6*time.Minute))
	st.Apply(CheckObservation{State: StateDegraded, Reason: ReasonDDoSInbound}, 2, 1, at.Add(7*time.Minute))
	if st.State != StateUp {
		t.Fatalf("down + degraded alternos no deben cambiar con umbral 2: %s", st.State)
	}
	// Un unknown aislado no mueve un estado conocido.
	st2 := NewCheckStatus(Check{ID: uuid.New(), Type: CheckNoTraffic})
	st2.Apply(up, 3, 1, at)
	if st2.Apply(CheckObservation{State: StateUnknown, Reason: ReasonNoFlowData}, 3, 1, at.Add(time.Minute)) || st2.State != StateUp {
		t.Fatal("un unknown aislado cambió el estado")
	}
	st2.Apply(CheckObservation{State: StateUnknown, Reason: ReasonNoFlowData}, 3, 1, at.Add(2*time.Minute))
	if !st2.Apply(CheckObservation{State: StateUnknown, Reason: ReasonNoFlowData}, 3, 1, at.Add(3*time.Minute)) || st2.State != StateUnknown {
		t.Fatalf("3 unknown seguidos deben dejar unknown: %s", st2.State)
	}
	// Historial acotado.
	for i := range RecentResults + 5 {
		st.Apply(up, 3, 1, at.Add(time.Duration(10+i)*time.Minute))
	}
	if len(st.Recent) != RecentResults || !st.Recent[0].At.After(st.Recent[1].At) {
		t.Fatalf("recent = %d (el más reciente primero)", len(st.Recent))
	}
}

func TestWorst(t *testing.T) {
	cs := []CheckStatus{{Type: CheckICMP, State: StateUp}, {Type: CheckNoTraffic, State: StateUnknown}}
	if s, i := Worst(cs); s != StateUp || i != 0 {
		t.Fatalf("up + unknown = %s (%d), esperaba up", s, i)
	}
	cs = append(cs, CheckStatus{Type: CheckDDoS, State: StateDegraded}, CheckStatus{Type: CheckHTTP, State: StateDown})
	if s, i := Worst(cs); s != StateDown || i != 3 {
		t.Fatalf("peor = %s (%d)", s, i)
	}
	if s, i := Worst(nil); s != StateUnknown || i != -1 {
		t.Fatalf("sin chequeos = %s (%d)", s, i)
	}
	if s, _ := Worst([]CheckStatus{{State: StateUnknown}}); s != StateUnknown {
		t.Fatal("todo unknown")
	}
}

func TestValidate(t *testing.T) {
	s := Service{Name: " IPTV ", Kind: "IPTV", Target: netip.MustParseAddr("192.0.2.20"),
		Checks: []Check{{Type: CheckICMP}, {Type: CheckHTTP, Path: ptr("/health")}, {Type: CheckNoTraffic}}}
	if errs := s.Validate(); len(errs) > 0 {
		t.Fatalf("válido: %v", errs)
	}
	if s.Name != "IPTV" || s.Kind != "iptv" || s.IntervalSeconds != 60 || s.FailuresBeforeDown != 3 || s.RecoveriesBeforeUp != 2 {
		t.Fatalf("defectos: %+v", s)
	}
	for _, c := range s.Checks {
		if c.ID == uuid.Nil {
			t.Fatal("chequeo sin id")
		}
	}
	bad := Service{Name: "", Kind: "-x", Target: netip.MustParseAddr("224.0.0.1"), IntervalSeconds: 10,
		Checks: []Check{{Type: CheckTCP}, {Type: CheckICMP, Port: ptr(80)}, {Type: CheckDDoS}, {Type: "smtp"},
			{Type: CheckHTTP, Path: ptr("http://x"), ExpectedStatusMin: ptr(500), ExpectedStatusMax: ptr(200)}}}
	fields := map[string]bool{}
	for _, e := range bad.Validate() {
		fields[e.Field] = true
	}
	for _, f := range []string{"name", "kind", "target", "interval_seconds", "checks[0].port", "checks[1].port",
		"checks[2].pps_threshold", "checks[3].type", "checks[4].path", "checks[4].expected_status_min"} {
		if !fields[f] {
			t.Errorf("falta error en %s (%v)", f, fields)
		}
	}
	via := Service{Name: "x", Kind: "dns", Target: netip.MustParseAddr("10.0.0.53"), ProbeViaRouterID: ptr(uuid.New()),
		Checks: []Check{{Type: CheckTCP, Port: ptr(53)}}}
	if errs := via.Validate(); len(errs) != 1 || errs[0].Field != "checks[0].forward_port" {
		t.Fatalf("con router: forward_port obligatorio: %v", errs)
	}
}

func TestIsPrivate(t *testing.T) {
	for ip, want := range map[string]bool{"10.1.2.3": true, "192.168.1.1": true, "100.64.0.9": true, "fd00::1": true,
		"192.0.2.20": false, "8.8.8.8": false, "2001:db8::1": false, "::ffff:10.0.0.1": true} {
		if got := IsPrivate(netip.MustParseAddr(ip)); got != want {
			t.Errorf("%s privado = %v", ip, got)
		}
	}
}

func TestBuildSnippet(t *testing.T) {
	s := &Service{ID: uuid.MustParse("0192f8f8-0000-7000-8000-0000000008a1"), Name: `IPTV "x"; /system reset`, Target: netip.MustParseAddr("192.168.10.20"),
		Checks: []Check{{Type: CheckICMP}, {Type: CheckHTTP, ForwardPort: ptr(18080)}, {Type: CheckTCP, Port: ptr(554), ForwardPort: ptr(18554)},
			{Type: CheckTCP, Port: ptr(22)}}}
	out := BuildSnippet(s, "rt-core", netip.MustParseAddr("10.255.1.7"), netip.MustParsePrefix("10.255.0.0/24"))
	if !out.Applicable {
		t.Fatalf("debería aplicar: %+v", out)
	}
	for _, want := range []string{
		"chain=dstnat in-interface=wg-horus dst-address=10.255.1.7 protocol=tcp dst-port=18080",
		"to-addresses=192.168.10.20 to-ports=80",
		"to-addresses=192.168.10.20 to-ports=554",
		"action=masquerade",
		"chain=forward in-interface=wg-horus src-address=10.255.0.0/24",
		`comment="horus-infra 0192f8f8"`,
	} {
		if !strings.Contains(out.Script, want) {
			t.Errorf("falta %q en el script:\n%s", want, out.Script)
		}
	}
	if strings.Contains(out.Script, `"x"`) || strings.Contains(out.Script, "; /system") {
		t.Fatalf("el nombre no se saneó:\n%s", out.Script)
	}
	if len(out.Warnings) != 2 { // ICMP no reenviable + tcp/22 sin forward_port
		t.Fatalf("avisos: %v", out.Warnings)
	}
	if out.HorusSide == "" {
		t.Fatal("sin nota del lado de Horus")
	}
	if BuildSnippet(s, "rt", netip.Addr{}, netip.MustParsePrefix("10.255.0.0/24")).Applicable {
		t.Fatal("sin túnel no aplica")
	}
}

func TestValidateRejectsLocalTargets(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "169.254.169.254", "fe80::1"} {
		s := Service{Name: "x", Kind: "http", Target: netip.MustParseAddr(ip), Checks: []Check{{Type: CheckICMP}}}
		errs := s.Validate()
		if len(errs) != 1 || errs[0].Field != "target" || errs[0].Code != "not_allowed" {
			t.Errorf("%s: %v", ip, errs)
		}
	}
}

func TestStatusOfResetsOnTypeChange(t *testing.T) {
	id := uuid.New()
	s := Service{CheckStatus: []CheckStatus{{CheckID: id, Type: CheckTCP, State: StateDown}}}
	if st := s.StatusOf(Check{ID: id, Type: CheckTCP}); st.State != StateDown {
		t.Fatalf("mismo tipo conserva el estado: %+v", st)
	}
	if st := s.StatusOf(Check{ID: id, Type: CheckHTTP}); st.State != StateUnknown {
		t.Fatalf("otro tipo empieza de cero: %+v", st)
	}
}
