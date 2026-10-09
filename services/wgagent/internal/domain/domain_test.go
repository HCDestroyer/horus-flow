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
