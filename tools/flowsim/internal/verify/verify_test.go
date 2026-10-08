package verify_test

import (
	"bytes"
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/expect"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/export"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/selftest"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/signals"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/verify"
)

func testExpected() *expect.Expected {
	return &expect.Expected{
		Schema: expect.Schema,
		Exporters: []expect.Exporter{
			{
				Name: "node-a", ExporterIP: "10.255.0.2", CollectorIP: "10.255.0.1", IPv6ClientLen: 64,
				Interfaces: expect.Interfaces{Upstream: 1, Tunnel: 30, Transit: 3},
				Prefixes: expect.Prefixes{
					Customers:      []string{"10.20.0.0/24", "2001:db8:1000::/40"},
					Infrastructure: []string{"10.20.255.0/24", "203.0.113.0/28"},
					Excluded:       []string{"10.20.250.0/24"},
				},
			},
			{
				Name: "node-b", ExporterIP: "10.255.0.3", CollectorIP: "10.255.0.1", IPv6ClientLen: 64,
				Prefixes: expect.Prefixes{Customers: []string{"10.20.0.0/24", "2001:db8:1100::/40"}},
			},
		},
	}
}

func rec(src, dst string, in, out uint32) flow.Record {
	return flow.Record{SrcIP: netip.MustParseAddr(src), DstIP: netip.MustParseAddr(dst), InIf: in, OutIf: out}
}

// Tabla de docs/traffic-model.md §4.6.
func TestAttribution(t *testing.T) {
	a, err := verify.NewAttributor(testExpected(), 0)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		r    flow.Record
		want signals.Attribution
	}{
		{"subida", rec("10.20.0.5", "142.250.1.1", 12, 1), signals.Attribution{Status: "attributed", Client: "10.20.0.5", Upload: true}},
		{"bajada", rec("142.250.1.1", "10.20.0.5", 1, 12), signals.Attribution{Status: "attributed", Client: "10.20.0.5"}},
		{"ipv6 /64", rec("2001:db8:1000:7::99", "2a00:1450::1", 12, 1), signals.Attribution{Status: "attributed", Client: "2001:db8:1000:7::/64", Upload: true}},
		{"interno", rec("10.20.0.5", "10.20.0.6", 12, 12), signals.Attribution{Status: "internal", Client: "10.20.0.5", Upload: true}},
		{"tránsito", rec("2001:db8:1100::1", "2a00:1450::1", 3, 1), signals.Attribution{Status: "transit"}},
		{"infraestructura", rec("203.0.113.2", "1.1.1.1", 0, 1), signals.Attribution{Status: "infrastructure"}},
		{"excluido", rec("10.20.250.9", "1.1.1.1", 12, 1), signals.Attribution{Status: "excluded"}},
		{"túnel", rec("10.255.0.2", "10.255.0.1", 0, 30), signals.Attribution{Status: "tunnel"}},
		{"fuera de prefijos subida", rec("172.16.50.2", "1.1.1.1", 12, 1), signals.Attribution{Status: "unknown", Unattributed: netip.MustParseAddr("172.16.50.2")}},
		{"fuera de prefijos bajada", rec("1.1.1.1", "172.16.50.2", 1, 12), signals.Attribution{Status: "unknown", Unattributed: netip.MustParseAddr("172.16.50.2")}},
	}
	for _, c := range cases {
		if got := a.Attribute(&c.r); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func scenarioCapture(t *testing.T, name string, p flow.Protocol) ([]capture.Datagram, *expect.Expected) {
	t.Helper()
	data, exp, err := selftest.Generate(context.Background(), selftest.Case{Scenario: name, Protocol: p, Format: capture.FormatHFSim})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := capture.ReadAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return ds, exp
}

func failed(rep *verify.Report) []string {
	var out []string
	for _, c := range rep.Checks {
		if !c.OK {
			out = append(out, c.Name)
		}
	}
	return out
}

func runVerify(t *testing.T, exp *expect.Expected, ds []capture.Datagram, opt verify.Options) *verify.Report {
	t.Helper()
	v, err := verify.New(exp, opt)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		v.Feed(d)
	}
	return v.Report()
}

func TestDetectsLossAndTampering(t *testing.T) {
	ds, exp := scenarioCapture(t, "scan", flow.V9)
	if rep := runVerify(t, exp, ds, verify.Options{}); !rep.OK {
		t.Fatalf("la captura intacta debe pasar: %v", failed(rep))
	}
	// Se pierde un datagrama: falla la secuencia y los totales.
	lossy := append(append([]capture.Datagram{}, ds[:10]...), ds[11:]...)
	rep := runVerify(t, exp, lossy, verify.Options{})
	if rep.OK || !strings.Contains(strings.Join(failed(rep), ","), "secuencia") {
		t.Fatalf("debe detectar la pérdida: %v", failed(rep))
	}
	// Con tolerancia la pérdida se admite.
	if rep := runVerify(t, exp, lossy, verify.Options{Tolerance: 0.05}); !rep.OK {
		t.Fatalf("con tolerancia debe pasar: %v", failed(rep))
	}
	// Las señales de otro escenario no cuadran.
	_, normal := scenarioCapture(t, "normal", flow.V9)
	normal.Exporters[0].Totals = exp.Exporters[0].Totals
	if rep := runVerify(t, normal, ds, verify.Options{}); rep.OK {
		t.Fatal("verificar scan contra normal debe fallar")
	}
	// Un origen desconocido se informa.
	bad := append([]capture.Datagram{}, ds...)
	bad[0].Src = netip.MustParseAddrPort("192.0.2.99:1000")
	if rep := runVerify(t, exp, bad, verify.Options{}); rep.OK {
		t.Fatal("un origen no declarado debe fallar")
	}
}

func TestMissingTemplateDetected(t *testing.T) {
	e := export.New(export.Config{Protocol: flow.IPFIX})
	r := flow.Record{SrcIP: netip.MustParseAddr("10.20.0.5"), DstIP: netip.MustParseAddr("1.1.1.1"), Packets: 1, Bytes: 60}
	_ = e.Export(r.Start, []flow.Record{r}) // primer datagrama (con plantilla) perdido
	second := e.Export(r.Start.Add(1), []flow.Record{r})
	info, recs, err := verify.NewDecoder().Decode("x", second[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.MissingTemplate != 1 || len(recs) != 0 {
		t.Fatalf("debe marcar el set sin plantilla: %+v", info)
	}
}

func FuzzDecode(f *testing.F) {
	for _, p := range []flow.Protocol{flow.IPFIX, flow.V9} {
		e := export.New(export.Config{Protocol: p})
		r := flow.Record{SrcIP: netip.MustParseAddr("10.20.0.5"), DstIP: netip.MustParseAddr("2.2.2.2"), Packets: 1, Bytes: 60}
		for _, d := range e.Export(r.Start, []flow.Record{r}) {
			f.Add(d)
		}
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		d := verify.NewDecoder()
		_, _, _ = d.Decode("x", data)
		_, _, _ = d.Decode("x", data)
	})
}
