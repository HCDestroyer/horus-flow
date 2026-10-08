package asn

import (
	"bytes"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
)

func sample(t *testing.T) *Snapshot {
	t.Helper()
	routes := []Entry{
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Route: Route{ASN: 64500, Country: "ES", Source: "ris"}},
		{Prefix: netip.MustParsePrefix("192.0.2.128/25"), Route: Route{ASN: 64501, Source: "ris"}},
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Route: Route{ASN: 1, Source: "iptoasn"}}, // duplicado: gana el primero
		{Prefix: netip.MustParsePrefix("2001:db8::/32"), Route: Route{ASN: 64510, Country: "DE", Source: "iptoasn"}},
	}
	asns := map[uint32]ASInfo{64500: {Name: "Example", NetworkType: "nsp", Source: "peeringdb"}, 64510: {Name: "V6"}}
	s, err := NewSnapshot(datasets.SnapshotMeta{CreatedAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}, routes, asns)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLookup(t *testing.T) {
	s := sample(t)
	if s.Len() != 3 || s.ASCount() != 2 || s.Meta.Kind != Kind {
		t.Fatalf("Len %d ASCount %d", s.Len(), s.ASCount())
	}
	r, ok := s.Lookup(netip.MustParseAddr("192.0.2.1"))
	if !ok || r.ASN != 64500 || r.Country != "ES" || r.AS.Name != "Example" || r.AS.NetworkType != "nsp" {
		t.Fatalf("192.0.2.1: %+v", r)
	}
	if r, _ := s.Lookup(netip.MustParseAddr("192.0.2.200")); r.ASN != 64501 || r.Prefix.Bits() != 25 {
		t.Fatalf("LPM: %+v", r)
	}
	if r, _ := s.Lookup(netip.MustParseAddr("2001:db8:ffff::1")); r.ASN != 64510 {
		t.Fatalf("IPv6: %+v", r)
	}
	if _, ok := s.Lookup(netip.MustParseAddr("198.51.100.1")); ok {
		t.Fatal("no debería haber atribución")
	}
	if _, err := NewSnapshot(datasets.SnapshotMeta{}, []Entry{{Prefix: netip.MustParsePrefix("192.0.2.0/24")}}, nil); err == nil {
		t.Fatal("ASN 0 debería fallar")
	}
}

func TestRoundTripAndDiff(t *testing.T) {
	s := sample(t)
	var buf bytes.Buffer
	if _, err := s.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSnapshot(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Payload(), s.Payload()) {
		t.Fatal("ida y vuelta no conserva el contenido")
	}
	if d := Diff(s, got); d.Ratio != 0 || d.Base != 3 {
		t.Fatalf("diff idéntico: %+v", d)
	}
	next, err := NewSnapshot(datasets.SnapshotMeta{}, []Entry{
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Route: Route{ASN: 64999}},   // cambiado
		{Prefix: netip.MustParsePrefix("203.0.113.0/24"), Route: Route{ASN: 64502}}, // añadido
		{Prefix: netip.MustParsePrefix("2001:db8::/32"), Route: Route{ASN: 64510}},  // igual
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := Diff(s, next)
	if d.Added != 1 || d.Removed != 1 || d.Changed != 1 || d.Ratio != 1 {
		t.Fatalf("diff: %+v", d)
	}
	if d := Diff(nil, next); d.Added != 3 || d.Ratio != 0 {
		t.Fatalf("diff sin anterior: %+v", d)
	}
	b := bytes.Clone(buf.Bytes())
	b[len(b)-1] ^= 0x80
	if _, err := ReadSnapshot(bytes.NewReader(b)); !errors.Is(err, datasets.ErrCorruptSnapshot) {
		t.Fatalf("corrupto: %v", err)
	}
}
