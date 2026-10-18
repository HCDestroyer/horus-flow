package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/domain"
)

// PSK de prueba de baja entropía (no es un secreto real).
var testPSK = strings.Repeat("prueba", 8)

func ipsec(idx uint32, addr, id string) domain.DesiredTunnel {
	return domain.DesiredTunnel{PeerID: "p" + addr, Transport: domain.TransportIPsec, Index: idx, TunnelAddress: addr + "/32",
		HubAddress: "10.255.0.1", LocalAddress: "203.0.113.1", LocalID: "203.0.113.1", RemoteID: id, PSK: testPSK}
}

func gre(idx uint32, addr, remote string) domain.DesiredTunnel {
	return domain.DesiredTunnel{PeerID: "p" + addr, Transport: domain.TransportGRE, Index: idx, TunnelAddress: addr,
		HubAddress: "10.255.0.1", LocalAddress: "203.0.113.1", RemoteIP: remote, PSK: "ignorada"}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	got, err := domain.Normalize([]domain.DesiredTunnel{ipsec(1, "10.255.1.2", "rt-1.example.net"), gre(2, "10.255.1.3", "198.51.100.7")})
	if err != nil || len(got) != 2 {
		t.Fatalf("normalize = %v, %v", got, err)
	}
	if got[1].Interface() != "hxfrm1" || got[2].Interface() != "hgre2" || got[1].ConnName() != "horus-1" || got[1].MTU != domain.DefaultMTU {
		t.Fatalf("nombres = %+v", got)
	}
	if got[2].PSK != "" {
		t.Fatal("un túnel GRE no conserva PSK")
	}
	cases := map[string]func() []domain.DesiredTunnel{
		"transporte": func() []domain.DesiredTunnel {
			d := gre(1, "10.255.1.2", "198.51.100.7")
			d.Transport = "wireguard"
			return []domain.DesiredTunnel{d}
		},
		"índice 0": func() []domain.DesiredTunnel { return []domain.DesiredTunnel{gre(0, "10.255.1.2", "198.51.100.7")} },
		"índice repetido": func() []domain.DesiredTunnel {
			return []domain.DesiredTunnel{gre(1, "10.255.1.2", "198.51.100.7"), gre(1, "10.255.1.3", "198.51.100.8")}
		},
		"ip repetida": func() []domain.DesiredTunnel {
			return []domain.DesiredTunnel{gre(1, "10.255.1.2", "198.51.100.7"), gre(2, "10.255.1.2", "198.51.100.8")}
		},
		"no /32": func() []domain.DesiredTunnel {
			d := gre(1, "10.255.1.2", "198.51.100.7")
			d.TunnelAddress = "10.255.1.0/24"
			return []domain.DesiredTunnel{d}
		},
		"gre sin remoto": func() []domain.DesiredTunnel { return []domain.DesiredTunnel{gre(1, "10.255.1.2", "")} },
		"gre remoto dup": func() []domain.DesiredTunnel {
			return []domain.DesiredTunnel{gre(1, "10.255.1.2", "198.51.100.7"), gre(2, "10.255.1.3", "198.51.100.7")}
		},
		"psk corta": func() []domain.DesiredTunnel {
			d := ipsec(1, "10.255.1.2", "rt")
			d.PSK = "corta"
			return []domain.DesiredTunnel{d}
		},
		"psk con símbolos": func() []domain.DesiredTunnel {
			d := ipsec(1, "10.255.1.2", "rt")
			d.PSK = testPSK + "\"; x"
			return []domain.DesiredTunnel{d}
		},
		"id inyectado": func() []domain.DesiredTunnel {
			return []domain.DesiredTunnel{ipsec(1, "10.255.1.2", "rt\" remote_addrs=%any")}
		},
		"id repetido": func() []domain.DesiredTunnel {
			return []domain.DesiredTunnel{ipsec(1, "10.255.1.2", "rt"), ipsec(2, "10.255.1.3", "rt")}
		},
		"mtu": func() []domain.DesiredTunnel {
			d := gre(1, "10.255.1.2", "198.51.100.7")
			d.MTU = 9000
			return []domain.DesiredTunnel{d}
		},
		"remoto ipv6": func() []domain.DesiredTunnel { return []domain.DesiredTunnel{gre(1, "10.255.1.2", "2001:db8::1")} },
	}
	for name, f := range cases {
		if _, err := domain.Normalize(f()); !errors.Is(err, domain.ErrInvalidState) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// ipsec sin IP remota (router tras NAT): válido, se identifica por remote_id.
	if _, err := domain.Normalize([]domain.DesiredTunnel{ipsec(1, "10.255.1.2", "KEYID-rt1")}); err != nil {
		t.Fatalf("ipsec sin remoto: %v", err)
	}
}

func TestParseNames(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]uint32{"hxfrm7": 7, "hgre12": 12} {
		if _, idx, ok := domain.ParseInterface(name); !ok || idx != want {
			t.Errorf("%s = %d, %v", name, idx, ok)
		}
	}
	for _, name := range []string{"gre0", "xfrm1", "hxfrm0", "hxfrm07", "hgre", "wg0", "hgre1a"} {
		if _, _, ok := domain.ParseInterface(name); ok {
			t.Errorf("%s reconocida", name)
		}
	}
	if idx, ok := domain.ParseConnName("horus-9"); !ok || idx != 9 {
		t.Fatal("horus-9")
	}
	if _, ok := domain.ParseConnName("otra-9"); ok {
		t.Fatal("conexión ajena reconocida")
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()
	want, _ := domain.Normalize([]domain.DesiredTunnel{ipsec(1, "10.255.1.2", "rt-1"), gre(2, "10.255.1.3", "198.51.100.7")})
	fp := func(t domain.Tunnel) string { return domain.Fingerprint(t, "x") }
	obs := []domain.Observed{
		{Transport: domain.TransportIPsec, Index: 1, Fingerprint: fp(want[1])},
		{Transport: domain.TransportIPsec, Index: 2, Fingerprint: "vieja"}, // pasó a GRE
		{Transport: domain.TransportGRE, Index: 9},                         // ya no existe
	}
	p := domain.Diff(want, obs, fp)
	if len(p.Upsert) != 1 || p.Upsert[0].Index != 2 {
		t.Fatalf("upsert = %+v", p.Upsert)
	}
	if len(p.Remove) != 2 || p.Remove[0].Index != 2 || p.Remove[1].Index != 9 {
		t.Fatalf("remove = %+v", p.Remove)
	}
	// Sin huella (agente reiniciado): se reaplica todo, no se borra nada.
	p = domain.Diff(want, []domain.Observed{{Transport: domain.TransportIPsec, Index: 1}}, fp)
	if len(p.Upsert) != 2 || len(p.Remove) != 0 {
		t.Fatalf("tras reinicio = %+v", p)
	}
}
