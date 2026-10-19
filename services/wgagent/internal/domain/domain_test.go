package domain

import (
	"errors"
	"net/netip"
	"testing"
)

const (
	k1 = "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg="
	k2 = "HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw="
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	got, err := Normalize([]DesiredPeer{{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.1.2/32"}, Keepalive: 25}})
	if err != nil || len(got) != 1 || got[k1].AllowedIPs[0] != netip.MustParsePrefix("10.255.1.2/32") {
		t.Fatalf("normalize = %v, %v", got, err)
	}
	bad := [][]DesiredPeer{
		{{PeerID: "a", PublicKey: "corta", AllowedIPs: []string{"10.255.1.2/32"}}},
		{{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.1.0/24"}}},
		{{PeerID: "a", PublicKey: k1}},
		{{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.1.2/32"}}, {PeerID: "b", PublicKey: k1, AllowedIPs: []string{"10.255.1.3/32"}}},
		{{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.1.2/32"}}, {PeerID: "b", PublicKey: k2, AllowedIPs: []string{"10.255.1.2/32"}}},
	}
	for i, b := range bad {
		if _, err := Normalize(b); !errors.Is(err, ErrInvalidState) {
			t.Errorf("caso %d aceptado: %v", i, err)
		}
	}
}

// Switches vía router (ADR-0032): el peer del router lleva, además de su /32
// de túnel, las direcciones Horus de los equipos que alcanza (una /32 o un
// bloque de una red de gestión). Nunca dos peers con prefijos solapados:
// WireGuard elige el peer por destino y un solape cruzaría ISPs.
func TestNormalizeViaRouter(t *testing.T) {
	t.Parallel()
	got, err := Normalize([]DesiredPeer{
		{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.1.2/32", "10.255.1.40/32", "10.255.2.0/28"}},
		{PeerID: "b", PublicKey: k2, AllowedIPs: []string{"10.255.1.3/32", "10.255.1.41/32"}},
	})
	if err != nil || len(got[k1].AllowedIPs) != 3 || got[k1].AllowedIPs[2] != netip.MustParsePrefix("10.255.2.0/28") {
		t.Fatalf("normalize = %v, %v", got, err)
	}
	bad := [][]DesiredPeer{
		// Solo un bloque: falta la /32 de túnel.
		{{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.2.0/28"}}},
		// Bloque demasiado grande o no canónico.
		{{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.1.2/32", "10.254.0.0/16"}}},
		{{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.1.2/32", "10.255.2.1/28"}}},
		// El bloque de un router se solapa con la /32 de otro.
		{{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.1.2/32", "10.255.2.0/28"}},
			{PeerID: "b", PublicKey: k2, AllowedIPs: []string{"10.255.2.5/32"}}},
		// La misma dirección Horus en dos routers.
		{{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.1.2/32", "10.255.1.40/32"}},
			{PeerID: "b", PublicKey: k2, AllowedIPs: []string{"10.255.1.3/32", "10.255.1.40/32"}}},
		// Una IP privada de un ISP nunca llega aquí, pero aunque llegara repetida, se rechaza.
		{{PeerID: "a", PublicKey: k1, AllowedIPs: []string{"10.255.1.2/32", "192.168.88.2/32"}},
			{PeerID: "b", PublicKey: k2, AllowedIPs: []string{"10.255.1.3/32", "192.168.88.2/32"}}},
	}
	for i, b := range bad {
		if _, err := Normalize(b); !errors.Is(err, ErrInvalidState) {
			t.Errorf("caso %d aceptado: %v", i, err)
		}
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()
	ip := func(s string) []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix(s)} }
	desired := map[string]Peer{k1: {PublicKey: k1, AllowedIPs: ip("10.255.1.2/32"), Keepalive: 25e9}}
	observed := map[string]Peer{
		k1: {PublicKey: k1, AllowedIPs: ip("10.255.1.2/32"), Keepalive: 25e9, RxBytes: 10},
		k2: {PublicKey: k2, AllowedIPs: ip("10.255.1.3/32")},
	}
	p := Diff(desired, observed)
	if len(p.Upsert) != 0 || len(p.Remove) != 1 || p.Remove[0] != k2 {
		t.Fatalf("plan = %+v", p)
	}
	observed[k1] = Peer{PublicKey: k1, AllowedIPs: ip("10.255.9.9/32")}
	if p := Diff(desired, observed); len(p.Upsert) != 1 {
		t.Fatalf("cambio de allowed-ips no detectado: %+v", p)
	}
}
