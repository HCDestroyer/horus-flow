package sim

import (
	"bytes"
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/scenarios"
)

func TestSegmentsActiveInactive(t *testing.T) {
	const active, inactive = 60_000, 15_000
	// 150 s de flujo con 151 paquetes (uno por segundo): 3 registros.
	segs := segments(0, 150_000, 151, 151_000, flagsSession, active, inactive)
	if len(segs) != 3 {
		t.Fatalf("esperaba 3 registros, hay %d: %+v", len(segs), segs)
	}
	var pk, by uint64
	for i, s := range segs {
		pk += s.pkts
		by += s.bytes
		if s.last-s.first >= active {
			t.Errorf("registro %d dura %d ms (>= active)", i, s.last-s.first)
		}
	}
	if pk != 151 || by != 151_000 {
		t.Errorf("conservación: %d paquetes, %d bytes", pk, by)
	}
	if segs[0].exportAt != 60_000 || segs[1].exportAt != 120_000 || segs[2].exportAt != 150_000+inactive {
		t.Errorf("tiempos de exportación %d %d %d", segs[0].exportAt, segs[1].exportAt, segs[2].exportAt)
	}
	if segs[0].flags&flow.SYN == 0 || segs[1].flags&(flow.SYN|flow.FIN) != 0 || segs[2].flags&flow.FIN == 0 {
		t.Errorf("flags por segmento incorrectos: %#x %#x %#x", segs[0].flags, segs[1].flags, segs[2].flags)
	}
	one := segments(5000, 5000, 1, 60, flagsSynOnly, active, inactive)
	if len(one) != 1 || one[0].flags != flow.SYN || one[0].exportAt != 20_000 {
		t.Errorf("paquete único: %+v", one)
	}
}

func TestAllScenariosParse(t *testing.T) {
	for _, n := range scenarios.Names() {
		if _, err := Load(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	if len(scenarios.Names()) < 10 {
		t.Errorf("faltan escenarios: %v", scenarios.Names())
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"clave desconocida":    "name: x\nfoo: 1\nexporters: [{populations: [{name: a, count: 1, behaviors: [residential]}]}]",
		"sin exportadores":     "name: x\n",
		"range inválido":       "name: x\nexporters: [{populations: [{name: a, count: 1, range: nope, behaviors: [residential]}]}]",
		"param desconocido":    "name: x\nexporters: [{populations: [{name: a, count: 1, behaviors: [{scan: {velocidad: 3}}]}]}]",
		"comportamiento malo":  "name: x\nexporters: [{populations: [{name: a, count: 1, behaviors: [volar]}]}]",
		"access inválido":      "name: x\nexporters: [{interfaces: {access: wifi}, populations: [{name: a, count: 1, behaviors: [residential]}]}]",
		"exportador duplicado": "name: x\nexporters: [{name: a, populations: []}, {name: a, populations: []}]",
	}
	for name, y := range cases {
		sc, err := Parse([]byte(y))
		if err == nil {
			// Los errores de comportamiento aparecen al construir el generador.
			_, err = newGen(sc, Options{Seed: 1})
		}
		if err == nil {
			t.Errorf("%s: se esperaba error", name)
		}
	}
}

func generate(t *testing.T, name string, opt Options) ([]byte, *Expected) {
	t.Helper()
	sc, err := Load(name)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := capture.NewWriter(&buf, capture.FormatHFSim)
	if err != nil {
		t.Fatal(err)
	}
	opt.Fixture = true
	exp, err := Run(context.Background(), sc, opt, func(d capture.Datagram, _ int) error { return w.Write(d) })
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), exp
}

func TestDeterminism(t *testing.T) {
	a, _ := generate(t, "c2", Options{Seed: 7, Protocol: flow.IPFIX})
	b, _ := generate(t, "c2", Options{Seed: 7, Protocol: flow.IPFIX})
	if !bytes.Equal(a, b) {
		t.Fatal("misma semilla, salida distinta")
	}
	c, _ := generate(t, "c2", Options{Seed: 8, Protocol: flow.IPFIX})
	if bytes.Equal(a, c) {
		t.Fatal("semillas distintas, salida idéntica")
	}
	// Con otro instante de arranque solo cambian los timestamps: mismo nº de
	// datagramas y de bytes.
	d, ed := generate(t, "c2", Options{Seed: 7, Protocol: flow.IPFIX, Start: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)})
	if len(d) != len(a) || ed.Start.Year() != 2027 {
		t.Fatalf("cambiar el arranque no debe cambiar el tamaño: %d vs %d", len(d), len(a))
	}
}

func TestNATAndIPv6(t *testing.T) {
	_, nat := generate(t, "normal", Options{Seed: 1, Protocol: flow.V9})
	v4, v6 := 0, 0
	for _, c := range nat.Exporters[0].Clients {
		if c.Family == 4 {
			v4++
			if !flow.IsClientPrivate(netip.MustParseAddr(c.Key)) {
				t.Errorf("con NAT el cliente %s debería ser privado", c.Key)
			}
		} else {
			v6++
			if !strings.HasSuffix(c.Key, "/64") || !strings.HasPrefix(c.Key, "2001:db8:10") {
				t.Errorf("clave IPv6 inesperada %s", c.Key)
			}
		}
	}
	if v4 != 250 || v6 != 50 {
		t.Errorf("normal: %d clientes IPv4 y %d IPv6, se esperaban 250 y 50", v4, v6)
	}
	f := false
	_, pub := generate(t, "normal", Options{Seed: 1, Protocol: flow.V9, NAT: &f, IPv6: &f})
	for _, c := range pub.Exporters[0].Clients {
		if c.Family != 4 || flow.IsClientPrivate(netip.MustParseAddr(c.Key)) {
			t.Errorf("sin NAT ni IPv6, cliente inesperado %s", c.Key)
		}
	}
}

func TestUnmetExpectationsFail(t *testing.T) {
	sc, err := Load("scan")
	if err != nil {
		t.Fatal(err)
	}
	// 20 s no bastan para 100 destinos SYN: el generador debe avisar.
	_, err = Run(context.Background(), sc, Options{Seed: 1, Duration: 5 * time.Second},
		func(capture.Datagram, int) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "no cumple sus expectativas") {
		t.Fatalf("se esperaba error de expectativas, hay %v", err)
	}
	exp, err := Run(context.Background(), sc, Options{Seed: 1, Duration: 5 * time.Second, AllowUnmet: true},
		func(capture.Datagram, int) error { return nil })
	if err != nil || len(exp.Warnings) == 0 {
		t.Fatalf("con AllowUnmet debe terminar con avisos: %v", err)
	}
}

func TestRateScaling(t *testing.T) {
	_, lo := generate(t, "normal", Options{Seed: 1, Rate: 50, Protocol: flow.IPFIX})
	_, hi := generate(t, "normal", Options{Seed: 1, Rate: 400, Protocol: flow.IPFIX})
	rl := float64(lo.Exporters[0].Totals.DataRecords) / lo.DurationSeconds
	rh := float64(hi.Exporters[0].Totals.DataRecords) / hi.DurationSeconds
	if rh < 4*rl || rh < 300 || rh > 500 {
		t.Errorf("la tasa no escala: %.0f reg/s con rate=50, %.0f reg/s con rate=400", rl, rh)
	}
}
