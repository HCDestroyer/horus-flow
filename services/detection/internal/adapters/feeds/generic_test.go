package feeds

import (
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
)

func customSrc(id, format string) datasets.Source {
	s := src(id, format, "scanner", 70)
	s.Origin = datasets.OriginCustom
	s.TTL = datasets.Duration(72 * time.Hour)
	return s
}

func TestParseIPListFixture(t *testing.T) {
	res, err := ParseFile(filepath.Join(fixtures(t), "custom-honeypot.txt"), customSrc("custom-honeypot", FormatIPList), fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if res.Lines != 7 || res.Invalid != 0 || len(res.Entries) != 10 {
		t.Fatalf("líneas %d inválidas %d entradas %d: %+v", res.Lines, res.Invalid, len(res.Entries), res.Entries)
	}
	m := byIP(t, res)
	for _, p := range []string{
		"192.0.2.50/32", "192.0.2.51/32", "198.51.100.128/28",
		"203.0.113.10/31", "203.0.113.12/30", "203.0.113.16/30", "203.0.113.20/32", // rango
		"203.0.113.77/32", "2001:db8:beef::/48", "2001:db8:cafe::9/128",
	} {
		if len(m[p]) != 1 {
			t.Errorf("falta %s", p)
		}
	}
	e := m["203.0.113.77/32"][0]
	if e.Port != 2222 || e.Category != reputation.CategoryScanner || e.Confidence != 70 || e.Source != "custom-honeypot" ||
		!e.ExpiresAt.Equal(fetchedAt.Add(72*time.Hour)) {
		t.Fatalf("ip:puerto: %+v", e)
	}
	if m["2001:db8:cafe::9/128"][0].Port != 22 {
		t.Fatal("[ipv6]:puerto")
	}
}

func TestParseIPListEdgeCases(t *testing.T) {
	in := "\uFEFF192.0.2.1\r\n198.51.100.1 - 198.51.100.3\n// solo comentario\n; otro\n::ffff:203.0.113.5\n"
	res, err := Parse(strings.NewReader(in), customSrc("custom-x", FormatIPList), fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 4 || res.Invalid != 0 {
		t.Fatalf("entradas %+v", res.Entries)
	}
	// Una página HTML servida como lista: todo inválido → corrupto.
	if _, err := ParseFile(filepath.Join(fixtures(t), "corrupt", "html-error.csv"), customSrc("custom-x", FormatIPList), fetchedAt); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("html: %v", err)
	}
	if _, err := ParseFile(filepath.Join(fixtures(t), "corrupt", "empty.txt"), customSrc("custom-x", FormatIPList), fetchedAt); !errors.Is(err, ErrEmpty) {
		t.Fatalf("vacío: %v", err)
	}
	// Rangos invertidos, de familias distintas o con demasiados prefijos.
	for _, bad := range []string{"192.0.2.9-192.0.2.1", "192.0.2.1-2001:db8::1", "2001:db8::1-2001:db8:ffff::2", "192.0.2.1:0", "x"} {
		if ps, _, ok := parseToken(bad); ok {
			t.Errorf("parseToken(%q) = %v", bad, ps)
		}
	}
}

func TestParseCSVFixture(t *testing.T) {
	s := customSrc("custom-scanners", FormatCSV)
	s.CSV = &datasets.CSVOptions{Column: "IP_Address", Delimiter: ";"}
	res, err := ParseFile(filepath.Join(fixtures(t), "custom-scanners.csv"), s, fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if res.Lines != 4 || res.Invalid != 1 || len(res.Entries) != 3 {
		t.Fatalf("líneas %d inválidas %d entradas %+v", res.Lines, res.Invalid, res.Entries)
	}
	if m := byIP(t, res); len(m["198.51.100.200/32"]) != 1 || len(m["2001:db8:5ca::1/128"]) != 1 {
		t.Fatalf("entradas %+v", res.Entries)
	}

	// Columna por posición, sin cabecera ni comentarios, separador tabulador.
	s.CSV = &datasets.CSVOptions{Column: "2", Delimiter: "\t", Comment: "-"}
	res, err = Parse(strings.NewReader("a\t192.0.2.1\nb\t198.51.100.0/24\n"), s, fetchedAt)
	if err != nil || len(res.Entries) != 2 {
		t.Fatalf("por posición: %v %+v", err, res)
	}
	// Columna por posición con cabecera explícita.
	yes := true
	s.CSV = &datasets.CSVOptions{Column: "1", Header: &yes}
	res, err = Parse(strings.NewReader("ip,motivo\n192.0.2.1,x\n"), s, fetchedAt)
	if err != nil || len(res.Entries) != 1 {
		t.Fatalf("cabecera explícita: %v %+v", err, res)
	}
	// La cabecera no tiene la columna (p. ej. una página de error).
	s.CSV = &datasets.CSVOptions{Column: "ip"}
	if _, err := Parse(strings.NewReader("<html><body>502</body></html>\n"), s, fetchedAt); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("sin columna: %v", err)
	}
	if _, err := Parse(strings.NewReader("# solo comentarios\n"), s, fetchedAt); !errors.Is(err, ErrEmpty) {
		t.Fatalf("sin cabecera: %v", err)
	}
	s.CSV = nil
	if _, err := Parse(strings.NewReader("ip\n192.0.2.1\n"), s, fetchedAt); !errors.Is(err, errCSVOptions) {
		t.Fatalf("sin opciones: %v", err)
	}
}

func TestCheckCSVOptions(t *testing.T) {
	no := false
	for _, o := range []*datasets.CSVOptions{
		{Column: "ip"}, {Column: "3", Delimiter: "|"}, {Column: "ip", Delimiter: ";", Comment: "%"}, {Column: "1", Comment: "-"},
	} {
		if err := CheckCSVOptions(o); err != nil {
			t.Errorf("%+v: %v", o, err)
		}
	}
	for _, o := range []*datasets.CSVOptions{
		nil, {}, {Column: " "}, {Column: "0"}, {Column: "999"}, {Column: "ip", Delimiter: "::"},
		{Column: "ip", Delimiter: "x"}, {Column: "ip", Comment: "ab"}, {Column: "ip", Delimiter: ";", Comment: ";"},
		{Column: "ip", Header: &no},
	} {
		if err := CheckCSVOptions(o); err == nil {
			t.Errorf("%+v debería ser inválido", o)
		}
	}
}

func TestParseTokenNormalizes(t *testing.T) {
	ps, port, ok := parseToken("192.0.2.77/24")
	if !ok || port != 0 || ps[0] != netip.MustParsePrefix("192.0.2.0/24") {
		t.Fatalf("%v %d %v", ps, port, ok)
	}
}
