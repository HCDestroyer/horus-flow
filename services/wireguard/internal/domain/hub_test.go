package domain

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestValidateNewPool(t *testing.T) {
	t.Parallel()
	services := pfx("10.255.0.0/24")
	existing := []netip.Prefix{pfx("10.255.0.0/16"), pfx("10.250.0.0/29")}
	for _, ok := range []string{"10.254.0.0/16", "10.253.0.0/28", "172.16.0.0/20", "192.168.10.0/24", " 10.240.0.0/16 "} {
		got, err := ValidateNewPool(ok, services, existing)
		if err != nil || got.String() != strings.TrimSpace(ok) {
			t.Errorf("%q rechazado: %v", ok, err)
		}
	}
	cases := []struct {
		cidr    string
		overlap bool
		detail  string
	}{
		{"10.254.0.0", false, "no es un CIDR"},
		{"nonsense/16", false, "no es un CIDR"},
		{"10.254.0.1/16", false, "la dirección de red es 10.254.0.0/16"}, // bits de host
		{"10.0.0.0/15", false, "entre /16 y /28"},
		{"10.254.0.0/29", false, "entre /16 y /28"},
		{"100.64.0.0/16", false, "100.64.0.0/10"},
		{"100.127.255.0/24", false, "100.64.0.0/10"},
		{"0.0.0.0/16", false, "0.0.0.0/8"},
		{"127.0.0.0/16", false, "loopback"},
		{"169.254.0.0/16", false, "link-local"},
		{"224.0.0.0/16", false, "multicast"},
		{"239.255.0.0/16", false, "multicast"},
		{"240.0.0.0/16", false, "reservado"},
		{"::ffff:10.254.0.0/112", false, "forma IPv4"},
		{"fd00::/63", false, "entre /64 y /120"},
		{"fd00::/121", false, "entre /64 y /120"},
		{"fd00::1/64", false, "bits de host"},
		{"fe80::/64", false, "link-local"},
		{"ff02::/64", false, "multicast"},
		{"::/64", false, "reservado"},
		{"fd00::/64", false, "IPv6 aún no"},             // válido pero no asignable
		{"10.255.0.0/24", true, "red de servicios"},     // igual a servicios
		{"10.255.0.128/25", true, "red de servicios"},   // dentro de servicios
		{"10.255.16.0/20", true, "rango 10.255.0.0/16"}, // dentro de un rango
		{"10.250.0.0/24", true, "rango 10.250.0.0/29"},  // contiene un rango
		{"10.0.0.0/16", false, ""},                      // control: válido
		{"10.250.0.16/28", false, ""},                   // contiguo, sin solape
		{"10.250.0.0/28", true, "rango 10.250.0.0/29"},  // solape parcial
		{"10.248.0.0/14", false, "entre /16 y /28"},     // demasiado grande
		{"192.168.0.0/16", false, ""},                   // control
		{"10.255.0.0/16", true, "red de servicios"},     // el mismo rango
		{"10.255.255.240/28", true, "rango 10.255.0.0/16"},
	}
	for _, c := range cases {
		_, err := ValidateNewPool(c.cidr, services, existing)
		if c.detail == "" {
			if err != nil {
				t.Errorf("%s rechazado: %v", c.cidr, err)
			}
			continue
		}
		var pe *PoolError
		if !errors.As(err, &pe) {
			t.Errorf("%s aceptado (%v)", c.cidr, err)
			continue
		}
		if pe.Overlap != c.overlap || !strings.Contains(pe.Detail, c.detail) {
			t.Errorf("%s: overlap=%v detalle=%q, quiero overlap=%v con %q", c.cidr, pe.Overlap, pe.Detail, c.overlap, c.detail)
		}
	}
}

func TestValidEndpointAndName(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"horus.isp.example", "wg-1.isp.example", "203.0.113.10", "2001:db8::10", "hub", "a.b.c.d.example",
		strings.Repeat("a", 63) + ".example"} {
		if !ValidEndpoint(ok) {
			t.Errorf("%q rechazado", ok)
		}
	}
	for _, bad := range []string{"", "https://horus.example", "horus.example:51820", "horus.example/x", "horus example", "-a.example",
		"a-.example", "a..example", "horus.example.", "1.2.3", "0.0.0.0", "::", "127.0.0.1", "224.0.0.1", "fe80::1%eth0", `a"b`,
		"a;b", strings.Repeat("a", 64) + ".example", strings.Repeat("a.", 127) + "ab"} {
		if ValidEndpoint(bad) {
			t.Errorf("%q aceptado", bad)
		}
	}
	for _, ok := range []string{"hub-1", "Hub Madrid", "Núcleo ñ", strings.Repeat("x", 64)} {
		if !ValidHubName(ok) {
			t.Errorf("nombre %q rechazado", ok)
		}
	}
	for _, bad := range []string{"", " hub", "hub ", "a\nb", "a\x00b", strings.Repeat("x", 65)} {
		if ValidHubName(bad) {
			t.Errorf("nombre %q aceptado", bad)
		}
	}
}

func TestIPAMIgnoresIPv6(t *testing.T) {
	t.Parallel()
	if c := Capacity(pfx("fd00::/64"), pfx("10.255.0.0/24")); c != 0 {
		t.Fatalf("capacidad IPv6 = %d", c)
	}
	if _, err := NextFree(pfx("fd00::/120"), pfx("10.255.0.0/24"), nil); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("NextFree IPv6 = %v", err)
	}
}
