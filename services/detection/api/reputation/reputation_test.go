package reputation

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
)

var day = time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

func sampleEntries() []Entry {
	return []Entry{
		{Prefix: netip.MustParsePrefix("192.0.2.10/32"), Indicator: Indicator{Source: "feodo", Category: CategoryBotnetCC, Confidence: 90, FirstSeen: day.AddDate(0, -1, 0), Port: 443, Threat: "QakBot"}},
		{Prefix: netip.MustParsePrefix("192.0.2.10/32"), Indicator: Indicator{Source: "feodo", Category: CategoryBotnetCC, Confidence: 70, FirstSeen: day.AddDate(0, -2, 0), LastSeen: day, Port: 443, Threat: "QakBot"}},
		{Prefix: netip.MustParsePrefix("192.0.2.10/32"), Indicator: Indicator{Source: "threatfox", Category: CategoryBotnetCC, Confidence: 50, FirstSeen: day, Port: 443, Reference: "1500003"}},
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Indicator: Indicator{Source: "drop", Category: CategoryBlocklist, Confidence: 80, FirstSeen: day, Reference: "SBL1", ExpiresAt: day.AddDate(0, 0, 7)}},
		{Prefix: netip.MustParsePrefix("2001:db8:dead::/48"), Indicator: Indicator{Source: "drop6", Category: CategoryBlocklist, Confidence: 80, FirstSeen: day}},
	}
}

func TestSnapshotLookup(t *testing.T) {
	s, err := NewSnapshot(datasets.SnapshotMeta{CreatedAt: day}, sampleEntries())
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 3 || s.Meta.Kind != Kind || s.Meta.Entries != 3 {
		t.Fatalf("Len %d meta %+v", s.Len(), s.Meta)
	}
	hits := s.Lookup(netip.MustParseAddr("192.0.2.10"))
	if len(hits) != 3 {
		t.Fatalf("hits = %+v", hits)
	}
	// Las dos entradas iguales de feodo se fusionan: primera aparición más
	// antigua, última más reciente, confianza máxima.
	if h := hits[0]; h.Source != "feodo" || !h.FirstSeen.Equal(day.AddDate(0, -2, 0)) || !h.LastSeen.Equal(day) || h.Confidence != 90 {
		t.Fatalf("fusión: %+v", h)
	}
	if hits[2].Prefix.String() != "192.0.2.0/24" || hits[2].Category != CategoryBlocklist {
		t.Fatalf("orden más específico → menos específico: %+v", hits)
	}
	if b, ok := s.Best(netip.MustParseAddr("192.0.2.10")); !ok || b.Source != "feodo" || b.Confidence != 90 {
		t.Fatalf("Best = %+v %v", b, ok)
	}
	if b, ok := s.Best(netip.MustParseAddr("192.0.2.11")); !ok || b.Source != "drop" {
		t.Fatalf("Best /24 = %+v", b)
	}
	if h := s.Lookup(netip.MustParseAddr("2001:db8:dead:1::9")); len(h) != 1 || h[0].Source != "drop6" {
		t.Fatalf("IPv6: %+v", h)
	}
	if h := s.Lookup(netip.MustParseAddr("198.51.100.1")); h != nil {
		t.Fatalf("sin coincidencia: %+v", h)
	}
	if _, err := NewSnapshot(datasets.SnapshotMeta{}, []Entry{{Prefix: netip.MustParsePrefix("192.0.2.1/32"), Indicator: Indicator{Category: "rara"}}}); err == nil {
		t.Fatal("categoría desconocida debería fallar")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	meta := datasets.SnapshotMeta{CreatedAt: day, Sources: []datasets.SourceRef{{ID: "feodo", License: "CC0-1.0", SHA256: "aa", Entries: 2}}}
	s, err := NewSnapshot(meta, sampleEntries())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := s.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSnapshot(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != s.Len() || got.Meta.Sources[0].License != "CC0-1.0" || !got.Meta.CreatedAt.Equal(day) {
		t.Fatalf("meta = %+v", got.Meta)
	}
	for _, ip := range []string{"192.0.2.10", "192.0.2.200", "2001:db8:dead::1", "203.0.113.1"} {
		a := netip.MustParseAddr(ip)
		if want, have := s.Lookup(a), got.Lookup(a); !slices.Equal(want, have) {
			t.Fatalf("%s: %+v != %+v", ip, have, want)
		}
	}
	// Determinista: misma entrada ⇒ mismo payload.
	if !bytes.Equal(s.Payload(), got.Payload()) {
		t.Fatal("el payload no es determinista")
	}
	// Corrupción: cualquier byte cambiado se detecta.
	b := bytes.Clone(buf.Bytes())
	b[len(b)/2] ^= 0x01
	if _, err := ReadSnapshot(bytes.NewReader(b)); !errors.Is(err, datasets.ErrCorruptSnapshot) {
		t.Fatalf("corrupto: %v", err)
	}
	// Un snapshot de otro tipo se rechaza.
	var other bytes.Buffer
	if err := datasets.EncodeSnapshot(&other, datasets.SnapshotMeta{Kind: "asn", PayloadFormat: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(&other); !errors.Is(err, datasets.ErrCorruptSnapshot) {
		t.Fatalf("otro tipo: %v", err)
	}
	// Payload truncado con checksum válido (escritor defectuoso).
	var trunc bytes.Buffer
	p := s.Payload()
	if err := datasets.EncodeSnapshot(&trunc, s.Meta, p[:len(p)-3]); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(&trunc); err == nil {
		t.Fatal("payload truncado debería fallar")
	}
}

func TestPublish(t *testing.T) {
	s, err := NewSnapshot(datasets.SnapshotMeta{CreatedAt: day}, sampleEntries())
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := s.Publish(datasets.SnapshotDir{Root: t.TempDir()})
	if err != nil || m.Version != "v1" || s.Meta.Version != "v1" {
		t.Fatalf("Publish = %+v %v", m, err)
	}
}

// bigSnapshot simula un snapshot de producción (~200 000 prefijos).
func bigSnapshot(tb testing.TB) (*Snapshot, []netip.Addr) {
	tb.Helper()
	r := rand.New(rand.NewPCG(7, 8))
	entries := make([]Entry, 0, 200_000)
	var probes []netip.Addr
	for i := range 200_000 {
		a := netip.AddrFrom4([4]byte{byte(1 + r.IntN(223)), byte(r.IntN(256)), byte(r.IntN(256)), byte(r.IntN(256))})
		bits := 32
		if i%4 == 0 {
			bits = 16 + r.IntN(16)
		}
		entries = append(entries, Entry{Prefix: netip.PrefixFrom(a, bits).Masked(),
			Indicator: Indicator{Source: "s", Category: CategoryBotnetCC, Confidence: 90, FirstSeen: day}})
		if i%50 == 0 {
			probes = append(probes, a)
		}
	}
	s, err := NewSnapshot(datasets.SnapshotMeta{}, entries)
	if err != nil {
		tb.Fatal(err)
	}
	for range len(probes) {
		probes = append(probes, netip.AddrFrom4([4]byte{byte(1 + r.IntN(223)), byte(r.IntN(256)), byte(r.IntN(256)), byte(r.IntN(256))}))
	}
	return s, probes
}

// TestLookupMedianUnderOneMicrosecond comprueba el criterio 4 de I0-17
// (< 1 µs de mediana). Se omite con -race (instrumentación) y -short.
func TestLookupMedianUnderOneMicrosecond(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("medición de latencia: sin -short ni -race")
	}
	s, probes := bigSnapshot(t)
	const rounds = 4
	samples := make([]time.Duration, 0, 101)
	for range 101 {
		start := time.Now()
		for range rounds {
			for _, a := range probes {
				s.Best(a)
			}
		}
		samples = append(samples, time.Since(start)/time.Duration(rounds*len(probes)))
	}
	slices.Sort(samples)
	median := samples[len(samples)/2]
	t.Logf("mediana por búsqueda: %v (%d prefijos)", median, s.Len())
	if median >= time.Microsecond {
		t.Fatalf("mediana %v ≥ 1 µs", median)
	}
}

func BenchmarkBest(b *testing.B) {
	s, probes := bigSnapshot(b)
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		s.Best(probes[i%len(probes)])
	}
}
