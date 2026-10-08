package feeds

import (
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
)

func dangerous(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(fixtures(t), "dangerous", name)
}

func TestCustomListRejectsDangerous(t *testing.T) {
	s := customSrc("custom-x", FormatIPList) // OnDangerous vacío = reject
	for _, c := range []struct {
		file, want string
	}{
		{"default-route.txt", "default_route"},
		{"reserved.txt", "reserved"},
		{"too-broad.txt", "too_broad"},
		{"coverage.txt", "cubre 17825792 direcciones IPv4"},
	} {
		_, err := ParseFile(dangerous(t, c.file), s, fetchedAt)
		if !errors.Is(err, ErrDangerous) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.file, err)
		}
	}
}

func TestCustomListWarnDropsDangerous(t *testing.T) {
	s := customSrc("custom-x", FormatIPList)
	s.OnDangerous = datasets.DangerWarn

	res, err := ParseFile(dangerous(t, "reserved.txt"), s, fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 11 || res.Dropped != 2 || res.Dangerous[DangerReserved] != 2 || len(res.Warnings) != 1 ||
		!strings.Contains(res.Warnings[0], "10.0.0.0/8") || !strings.Contains(res.Warnings[0], "100.64.12.34/32") {
		t.Fatalf("warn: %d entradas, %+v", len(res.Entries), res)
	}
	res, err = ParseFile(dangerous(t, "too-broad.txt"), s, fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Dangerous[DangerTooBroad] != 2 {
		t.Fatalf("demasiado amplios: %+v", res)
	}
	// La ruta por defecto y la cobertura excesiva rechazan la lista también en modo aviso.
	for _, f := range []string{"default-route.txt", "coverage.txt"} {
		if _, err := ParseFile(dangerous(t, f), s, fetchedAt); !errors.Is(err, ErrDangerous) {
			t.Errorf("%s en modo aviso: %v", f, err)
		}
	}
	// Si lo peligroso es más del 5 % (y más de 2 entradas), se rechaza igualmente.
	in := "192.0.2.1\n10.0.0.1\n10.0.0.2\n172.16.0.1\n"
	if _, err := Parse(strings.NewReader(in), s, fetchedAt); !errors.Is(err, ErrDangerous) {
		t.Errorf("mayoría peligrosa en modo aviso: %v", err)
	}
}

func TestCatalogPolicyUnchanged(t *testing.T) {
	// Las fuentes del catálogo mantienen el comportamiento de I0-17: lo
	// peligroso cuenta como inválido y no rechaza la lista por sí solo.
	res, err := ParseFile(dangerous(t, "default-route.txt"), src("x", "netset", "", 0), fetchedAt)
	if err != nil || len(res.Entries) != 2 || res.Invalid != 1 {
		t.Fatalf("catálogo: %v %+v", err, res)
	}
	// Un /9 vale en el catálogo (mínimo /8), no en una lista personalizada.
	if res, err := Parse(strings.NewReader("8.0.0.0/9\n"), src("x", "netset", "", 0), fetchedAt); err != nil || len(res.Entries) != 1 {
		t.Fatalf("/9 en el catálogo: %v", err)
	}
	// El catálogo puede pedir explícitamente rechazo.
	s := src("x", "netset", "", 0)
	s.OnDangerous = datasets.DangerReject
	if _, err := ParseFile(dangerous(t, "reserved.txt"), s, fetchedAt); !errors.Is(err, ErrDangerous) {
		t.Fatalf("catálogo con reject: %v", err)
	}
}

func TestProtectedPrefixes(t *testing.T) {
	pol := CustomPolicy(datasets.DangerReject)
	pol.Protected = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	s := customSrc("custom-x", FormatIPList)
	_, err := ParseWithPolicy(strings.NewReader("192.0.2.1\n198.51.100.0/25\n"), s, fetchedAt, pol)
	if !errors.Is(err, ErrDangerous) || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("protegido: %v", err)
	}
	pol.OnDangerous = ActionWarn
	res, err := ParseWithPolicy(strings.NewReader("192.0.2.1\n192.0.2.2\n192.0.2.3\n198.51.100.9\n"), s, fetchedAt, pol)
	if err != nil || len(res.Entries) != 3 || res.Dangerous[DangerProtected] != 1 {
		t.Fatalf("protegido en modo aviso: %v %+v", err, res)
	}
}

func TestClassify(t *testing.T) {
	pol := CustomPolicy("")
	for p, want := range map[string]Danger{
		"0.0.0.0/0": DangerDefaultRoute, "::/0": DangerDefaultRoute,
		"10.1.2.3/32": DangerReserved, "100.64.0.0/10": DangerReserved, "100.0.0.0/9": DangerReserved,
		"127.0.0.1/32": DangerReserved, "169.254.1.1/32": DangerReserved, "224.0.0.1/32": DangerReserved,
		"255.255.255.255/32": DangerReserved, "fd00::1/128": DangerReserved, "fe80::/64": DangerReserved,
		"64:ff9b::c000:201/128": DangerReserved, "::1/128": DangerReserved,
		"8.0.0.0/11": DangerTooBroad, "2001:db8::/31": DangerTooBroad,
		"8.0.0.0/12": "", "192.0.2.1/32": "", "2001:db8::/32": "",
	} {
		if got := pol.Classify(netip.MustParsePrefix(p)); got != want {
			t.Errorf("Classify(%s) = %q, quiero %q", p, got, want)
		}
	}
}

func TestMaxEntries(t *testing.T) {
	s := customSrc("custom-x", FormatIPList)
	s.MaxEntries = 2
	if _, err := Parse(strings.NewReader("192.0.2.1\n192.0.2.2\n192.0.2.3\n"), s, fetchedAt); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("max_entries: %v", err)
	}
}
