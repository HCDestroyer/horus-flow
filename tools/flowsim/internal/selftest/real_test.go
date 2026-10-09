package selftest

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/expect"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/verify"
)

func realFixturesDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no se pudo localizar el fichero de test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "tests", "fixtures", "mikrotik-real")
}

// TestRealFixtures: la captura real anonimizada da exactamente lo que fija su
// expected.json (golden).
func TestRealFixtures(t *testing.T) {
	var out bytes.Buffer
	ok, err := CheckRealFixtures(realFixturesDir(t), &out, true)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("fixtures reales:\n%s", out.String())
	}
}

// TestRealNATAttribution fija, con independencia del golden, los recuentos de
// docs/traffic-model.md §4.4.3 en el recorte de 20 s. Los registros (2596) y
// las bajadas con IE 226 privada distinta de dst (728) coinciden con un
// decodificador independiente en Python sobre este fichero; sobre la captura
// completa de 74 s ese decodificador da lo mismo en la original y en la
// anonimizada (3 211 bajadas), y sim-verify atribuye 3 211 bajadas por IE 226
// y 3 258 subidas con postNATSrc pública (README.md del fixture).
func TestRealNATAttribution(t *testing.T) {
	dir := realFixturesDir(t)
	exp, err := expect.Load(filepath.Join(dir, "ipfix-nat-20s.expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ipfix-nat-20s.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := VerifyCapture(bytes.NewReader(data), exp, verify.Options{})
	if err != nil {
		t.Fatal(err)
	}
	g := exp.Exporters[0]
	want := map[string]uint64{
		expect.RuleUploadSrc:          783,
		expect.RuleDownloadPostNATDst: 728,
		expect.RuleDownloadDst:        2,
		expect.RuleInternal:           1072,
	}
	for k, n := range want {
		if g.ByRule[k] != n {
			t.Errorf("by_rule[%s] = %d en expected.json, esperaba %d", k, g.ByRule[k], n)
		}
	}
	if g.Totals.DataRecords != 2596 || g.Totals.RecordsV6 != 0 {
		t.Errorf("registros %d (v6 %d), esperaba 2596 IPv4", g.Totals.DataRecords, g.Totals.RecordsV6)
	}
	if len(g.Templates) != 2 || g.Templates[0].ID != 258 || len(g.Templates[0].Fields) != 37 ||
		g.Templates[1].ID != 259 || len(g.Templates[1].Fields) != 34 {
		t.Errorf("plantillas inesperadas: %+v", g.Templates)
	}
	for _, c := range rep.Checks {
		if !c.OK {
			t.Errorf("%s: %s", c.Name, c.Detail)
		}
		if c.Name == "po-router: NAT: bajada por postNATDst (IE 226)" && c.Detail !=
			"726 subidas con postNATSrc pública, 728 bajadas atribuidas por IE 226, 0 con IP pública fuera de nat_ips" {
			t.Errorf("NAT: %s", c.Detail)
		}
	}
}
