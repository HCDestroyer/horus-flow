package feeds

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/detection/api/reputation"
)

var fetchedAt = time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)

// fixtures devuelve tests/fixtures/feeds buscando la raíz del repositorio.
func fixtures(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "tests", "fixtures", "feeds")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no se encontró la raíz del repositorio")
		}
		dir = parent
	}
}

func src(id, format, category string, conf int) datasets.Source {
	return datasets.Source{ID: id, Kind: "reputation", Format: format, Category: category, Confidence: conf,
		URL: "https://example.org/x", License: "x", LicenseURL: "https://example.org/l",
		CommercialUse: datasets.CommercialYes, Frequency: datasets.Duration(time.Hour)}
}

func byIP(t *testing.T, res *Result) map[string][]reputation.Entry {
	t.Helper()
	m := map[string][]reputation.Entry{}
	for _, e := range res.Entries {
		m[e.Prefix.String()] = append(m[e.Prefix.String()], e)
	}
	return m
}

func TestParseFeodo(t *testing.T) {
	s := src("abusech-feodo", "abusech-feodo-csv", "botnet_cc", 90)
	s.TTL = datasets.Duration(7 * 24 * time.Hour)
	res, err := ParseFile(filepath.Join(fixtures(t), "abusech-feodo.csv"), s, fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 5 || res.Invalid != 0 {
		t.Fatalf("entradas %d inválidas %d", len(res.Entries), res.Invalid)
	}
	m := byIP(t, res)
	e := m["192.0.2.10/32"]
	if len(e) != 2 || e[0].Port != 443 || e[1].Port != 8443 || e[0].Threat != "QakBot" || e[0].Category != reputation.CategoryBotnetCC {
		t.Fatalf("192.0.2.10: %+v", e)
	}
	if !e[0].FirstSeen.Equal(time.Date(2026, 9, 1, 10, 11, 12, 0, time.UTC)) {
		t.Fatalf("first_seen %v", e[0].FirstSeen)
	}
	if !e[0].ExpiresAt.Equal(fetchedAt.Add(7 * 24 * time.Hour)) {
		t.Fatalf("expires %v", e[0].ExpiresAt)
	}
	if off := m["198.51.100.23/32"][0]; off.Confidence != 70 {
		t.Fatalf("C2 offline debería bajar a 70: %d", off.Confidence)
	}
	if _, ok := m["2001:db8:c2::1/128"]; !ok {
		t.Fatal("falta la entrada IPv6")
	}
}

func TestParseThreatFox(t *testing.T) {
	res, err := ParseFile(filepath.Join(fixtures(t), "abusech-threatfox.csv"),
		src("abusech-threatfox", "abusech-threatfox-csv", "botnet_cc", 75), fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 4 || res.Ignored != 1 || res.Invalid != 0 {
		t.Fatalf("entradas %d ignoradas %d inválidas %d", len(res.Entries), res.Ignored, res.Invalid)
	}
	m := byIP(t, res)
	cs := m["198.51.100.50/32"][0]
	if cs.Category != reputation.CategoryBotnetCC || cs.Confidence != 75 || cs.Port != 443 || cs.Threat != "Cobalt Strike" || cs.Reference != "1500001" {
		t.Fatalf("cobalt strike: %+v", cs)
	}
	mirai := m["203.0.113.9/32"][0]
	if mirai.Category != reputation.CategoryMalware || mirai.LastSeen.IsZero() {
		t.Fatalf("mirai: %+v", mirai)
	}
	if q := m["192.0.2.10/32"][0]; q.Confidence != 50 {
		t.Fatalf("confianza = min(fuente, IOC): %d", q.Confidence)
	}
	if v6 := m["2001:db8:bad::7/128"]; len(v6) != 1 || v6[0].Port != 9001 {
		t.Fatalf("IPv6 ip:port: %+v", v6)
	}
}

func TestParseSpamhaus(t *testing.T) {
	res, err := ParseFile(filepath.Join(fixtures(t), "spamhaus-drop-txt.txt"), src("drop", "spamhaus-drop-txt", "", 80), fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	m := byIP(t, res)
	if len(res.Entries) != 3 || m["198.51.100.0/25"][0].Reference != "SBL000002" || m["198.51.100.0/25"][0].Category != reputation.CategoryBlocklist {
		t.Fatalf("drop.txt: %+v", res.Entries)
	}
	for _, f := range []string{"spamhaus-drop-v4.json", "spamhaus-drop-v6.json"} {
		res, err := ParseFile(filepath.Join(fixtures(t), f), src("drop", "spamhaus-drop-json", "blocklist", 80), fetchedAt)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(res.Entries) < 2 || res.Entries[0].Reference == "" {
			t.Fatalf("%s: %+v", f, res.Entries)
		}
	}
}

func TestParseNetset(t *testing.T) {
	res, err := ParseFile(filepath.Join(fixtures(t), "tor-exit.txt"), src("tor-exit", "netset", "tor", 100), fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 || res.Entries[0].Category != reputation.CategoryTor || res.Entries[0].Confidence != 100 {
		t.Fatalf("tor: %+v", res.Entries)
	}
	res, err = Parse(strings.NewReader("# c\n192.0.2.0/24 # comentario\n::ffff:198.51.100.7\n"), src("x", "netset", "scanner", 0), fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Entries[1].Prefix != netip.MustParsePrefix("198.51.100.7/32") || res.Entries[0].Confidence != 50 {
		t.Fatalf("netset: %+v", res.Entries)
	}
}

func TestRejectsCorruptAndEmpty(t *testing.T) {
	dir := filepath.Join(fixtures(t), "corrupt")
	cases := []struct {
		file, format string
		want         error
	}{
		{"feodo-truncated.csv", "abusech-feodo-csv", ErrCorrupt},
		{"html-error.csv", "abusech-feodo-csv", ErrCorrupt},
		{"html-error.csv", "abusech-threatfox-csv", ErrCorrupt},
		{"html-error.csv", "spamhaus-drop-txt", ErrCorrupt},
		{"html-error.csv", "netset", ErrCorrupt},
		{"empty.txt", "netset", ErrEmpty},
		{"empty.txt", "spamhaus-drop-json", ErrCorrupt},
		{"spamhaus-drop-v6-truncated.json", "spamhaus-drop-json", ErrCorrupt},
		{"netset-garbage.netset", "netset", ErrCorrupt},
	}
	for _, c := range cases {
		_, err := ParseFile(filepath.Join(dir, c.file), src("x", c.format, "blocklist", 50), fetchedAt)
		if !errors.Is(err, c.want) {
			t.Errorf("%s como %s: error %v, quiero %v", c.file, c.format, err, c.want)
		}
	}
	// Pie con número de entradas que no cuadra.
	bad := "# \"first_seen_utc\",\"dst_ip\",\"dst_port\",\"c2_status\",\"last_online\",\"malware\"\n" +
		"\"2026-09-01 10:11:12\",\"192.0.2.10\",\"443\",\"online\",\"2026-10-07\",\"QakBot\"\n# Number of entries: 9\n"
	if _, err := Parse(strings.NewReader(bad), src("x", "abusech-feodo-csv", "", 0), fetchedAt); !errors.Is(err, ErrCorrupt) {
		t.Errorf("pie incoherente: %v", err)
	}
	// Prefijos peligrosos (demasiado amplios, privados, loopback) no entran.
	res, err := Parse(strings.NewReader("192.0.2.1\n0.0.0.0/0\n2001:db8::/32\n::1\n"), src("x", "netset", "", 0), fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Invalid != 2 {
		t.Fatalf("saneamiento: %d entradas, %d inválidas", len(res.Entries), res.Invalid)
	}
	if _, err := Parse(strings.NewReader("x"), src("x", "desconocido", "", 0), fetchedAt); err == nil {
		t.Error("formato desconocido debería fallar")
	}
}

func TestFormats(t *testing.T) {
	if len(Formats()) != 5 || !Supports("netset") || Supports("nope") {
		t.Fatalf("Formats = %v", Formats())
	}
}
