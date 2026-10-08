package iptrie

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"
)

func mustBuild(t *testing.T, prefixes ...string) *Table[string] {
	t.Helper()
	b := NewBuilder[string](nil)
	for _, p := range prefixes {
		if err := b.Insert(netip.MustParsePrefix(p), p); err != nil {
			t.Fatalf("Insert(%s): %v", p, err)
		}
	}
	return b.Build()
}

func TestLookupLPM(t *testing.T) {
	tbl := mustBuild(t,
		"10.0.0.0/8", "10.1.0.0/16", "10.1.2.0/24", "10.1.2.3/32",
		"192.0.2.0/24", "0.0.0.0/0",
		"2001:db8::/32", "2001:db8:1::/48", "2001:db8:1:2::/64", "::/0",
		"255.255.255.255/32", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128",
	)
	cases := []struct{ ip, want string }{
		{"10.1.2.3", "10.1.2.3/32"},
		{"10.1.2.4", "10.1.2.0/24"},
		{"10.1.3.1", "10.1.0.0/16"},
		{"10.2.0.0", "10.0.0.0/8"},
		{"10.255.255.255", "10.0.0.0/8"},
		{"11.0.0.0", "0.0.0.0/0"},
		{"9.255.255.255", "0.0.0.0/0"},
		{"192.0.2.255", "192.0.2.0/24"},
		{"255.255.255.255", "255.255.255.255/32"},
		{"255.255.255.254", "0.0.0.0/0"},
		{"::ffff:10.1.2.3", "10.1.2.3/32"}, // IPv4 mapeada en IPv6
		{"2001:db8:1:2::1", "2001:db8:1:2::/64"},
		{"2001:db8:1:3::1", "2001:db8:1::/48"},
		{"2001:db8:ffff::1", "2001:db8::/32"},
		{"2001:db9::1", "::/0"},
		{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128"},
		{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe", "::/0"},
	}
	for _, c := range cases {
		p, v, ok := tbl.Lookup(netip.MustParseAddr(c.ip))
		if !ok || p.String() != c.want || v != c.want {
			t.Errorf("Lookup(%s) = %v %q %v, quiero %s", c.ip, p, v, ok, c.want)
		}
	}
}

func TestLookupMiss(t *testing.T) {
	tbl := mustBuild(t, "198.51.100.0/24", "2001:db8::/32")
	for _, ip := range []string{"198.51.99.255", "198.51.101.0", "0.0.0.0", "2001:db7::1", "::1"} {
		if p, _, ok := tbl.Lookup(netip.MustParseAddr(ip)); ok {
			t.Errorf("Lookup(%s) = %v, quiero sin coincidencia", ip, p)
		}
	}
	if _, _, ok := tbl.Lookup(netip.Addr{}); ok {
		t.Error("Lookup(dirección vacía) debería fallar")
	}
	empty := NewBuilder[int](nil).Build()
	if _, _, ok := empty.Lookup(netip.MustParseAddr("1.1.1.1")); ok {
		t.Error("tabla vacía no debería coincidir")
	}
}

func TestCoveringAndParent(t *testing.T) {
	tbl := mustBuild(t, "10.0.0.0/8", "10.1.0.0/16", "10.1.2.0/24", "10.200.0.0/16")
	var got []string
	for p := range tbl.Covering(netip.MustParseAddr("10.1.2.9")) {
		got = append(got, p.String())
	}
	want := []string{"10.1.2.0/24", "10.1.0.0/16", "10.0.0.0/8"}
	if !slices.Equal(got, want) {
		t.Fatalf("Covering = %v, quiero %v", got, want)
	}
	p, _, ok := tbl.CoveringPrefix(netip.MustParsePrefix("10.1.2.128/25"))
	if !ok || p.String() != "10.1.2.0/24" {
		t.Fatalf("CoveringPrefix(/25) = %v %v", p, ok)
	}
	p, _, ok = tbl.CoveringPrefix(netip.MustParsePrefix("10.1.0.0/15"))
	if !ok || p.String() != "10.0.0.0/8" {
		t.Fatalf("CoveringPrefix(/15) = %v %v", p, ok)
	}
	if _, _, ok := tbl.CoveringPrefix(netip.MustParsePrefix("10.0.0.0/7")); ok {
		t.Fatal("CoveringPrefix(/7) no debería coincidir")
	}
}

func TestMergeAndNormalize(t *testing.T) {
	b := NewBuilder(func(old, n int) int { return old + n })
	_ = b.Insert(netip.MustParsePrefix("10.1.2.3/24"), 1) // bits de host se enmascaran
	_ = b.Insert(netip.MustParsePrefix("10.1.2.0/24"), 2)
	_ = b.Insert(netip.MustParsePrefix("::ffff:10.1.2.0/120"), 4) // mapeada => 10.1.2.0/24
	if b.Len() != 1 {
		t.Fatalf("Len = %d, quiero 1", b.Len())
	}
	_, v, ok := b.Build().Lookup(netip.MustParseAddr("10.1.2.200"))
	if !ok || v != 7 {
		t.Fatalf("valor fusionado = %d %v, quiero 7", v, ok)
	}
	if err := b.Insert(netip.Prefix{}, 0); err == nil {
		t.Fatal("Insert de prefijo inválido debería fallar")
	}
}

func TestAllOrder(t *testing.T) {
	tbl := mustBuild(t, "2001:db8::/32", "10.1.0.0/16", "10.0.0.0/8", "1.0.0.0/24")
	var got []string
	for p := range tbl.All() {
		got = append(got, p.String())
	}
	want := []string{"1.0.0.0/24", "10.0.0.0/8", "10.1.0.0/16", "2001:db8::/32"}
	if !slices.Equal(got, want) {
		t.Fatalf("All = %v, quiero %v", got, want)
	}
}

// TestLookupRandomAgainstLinear compara contra una búsqueda lineal con
// prefijos aleatorios anidados y solapados.
func TestLookupRandomAgainstLinear(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var prefixes []netip.Prefix
	b := NewBuilder[string](nil)
	for range 2000 {
		var p netip.Prefix
		if r.IntN(2) == 0 {
			a := netip.AddrFrom4([4]byte{10, byte(r.IntN(4)), byte(r.IntN(256)), byte(r.IntN(256))})
			p = netip.PrefixFrom(a, 8+r.IntN(25)).Masked()
		} else {
			var raw [16]byte
			raw[0], raw[1], raw[2], raw[3] = 0x20, 0x01, 0x0d, 0xb8
			raw[4], raw[5], raw[15] = byte(r.IntN(4)), byte(r.IntN(256)), byte(r.IntN(256))
			p = netip.PrefixFrom(netip.AddrFrom16(raw), 32+r.IntN(97)).Masked()
		}
		prefixes = append(prefixes, p)
		_ = b.Insert(p, p.String())
	}
	tbl := b.Build()
	for range 20000 {
		var a netip.Addr
		if r.IntN(2) == 0 {
			a = netip.AddrFrom4([4]byte{10, byte(r.IntN(5)), byte(r.IntN(256)), byte(r.IntN(256))})
		} else {
			var raw [16]byte
			raw[0], raw[1], raw[2], raw[3] = 0x20, 0x01, 0x0d, 0xb8
			raw[4], raw[5], raw[15] = byte(r.IntN(5)), byte(r.IntN(256)), byte(r.IntN(256))
			a = netip.AddrFrom16(raw)
		}
		best := netip.Prefix{}
		for _, p := range prefixes {
			if p.Contains(a) && (!best.IsValid() || p.Bits() > best.Bits()) {
				best = p
			}
		}
		got, _, ok := tbl.Lookup(a)
		if ok != best.IsValid() || (ok && got != best) {
			t.Fatalf("Lookup(%s) = %v %v, quiero %v", a, got, ok, best)
		}
	}
}

func TestRangeToPrefixes(t *testing.T) {
	cases := []struct {
		first, last string
		want        []string
	}{
		{"1.0.0.0", "1.0.0.255", []string{"1.0.0.0/24"}},
		{"1.0.0.0", "1.0.1.255", []string{"1.0.0.0/23"}},
		{"1.0.0.1", "1.0.0.6", []string{"1.0.0.1/32", "1.0.0.2/31", "1.0.0.4/31", "1.0.0.6/32"}},
		{"0.0.0.0", "255.255.255.255", []string{"0.0.0.0/0"}},
		{"10.0.0.0", "10.0.0.0", []string{"10.0.0.0/32"}},
		{"2001:db8::", "2001:db8:0:ffff:ffff:ffff:ffff:ffff", []string{"2001:db8::/48"}},
		{"::", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", []string{"::/0"}},
		{"2001:db8::1", "2001:db8::2", []string{"2001:db8::1/128", "2001:db8::2/128"}},
	}
	for _, c := range cases {
		got, err := RangeToPrefixes(netip.MustParseAddr(c.first), netip.MustParseAddr(c.last))
		if err != nil {
			t.Fatalf("RangeToPrefixes(%s,%s): %v", c.first, c.last, err)
		}
		if s := fmt.Sprint(got); s != fmt.Sprint(toPrefixes(c.want)) {
			t.Errorf("RangeToPrefixes(%s,%s) = %s, quiero %v", c.first, c.last, s, c.want)
		}
	}
	if _, err := RangeToPrefixes(netip.MustParseAddr("1.0.0.2"), netip.MustParseAddr("1.0.0.1")); err == nil {
		t.Error("rango invertido debería fallar")
	}
	if _, err := RangeToPrefixes(netip.MustParseAddr("1.0.0.2"), netip.MustParseAddr("::1")); err == nil {
		t.Error("familias mezcladas deberían fallar")
	}
	got, err := RangeFromCount(netip.MustParseAddr("192.0.2.0"), 768)
	if err != nil || fmt.Sprint(got) != "[192.0.2.0/23 192.0.4.0/24]" {
		t.Errorf("RangeFromCount = %v %v", got, err)
	}
	if _, err := RangeFromCount(netip.MustParseAddr("255.255.255.0"), 512); err == nil {
		t.Error("RangeFromCount fuera de rango debería fallar")
	}
	if PrefixLast(netip.MustParsePrefix("10.0.0.0/8")).String() != "10.255.255.255" {
		t.Error("PrefixLast incorrecto")
	}
}

func toPrefixes(ss []string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// BenchmarkLookup mide la búsqueda con 1 millón de prefijos (objetivo I0-17:
// < 1 µs de mediana).
func BenchmarkLookup(b *testing.B) {
	r := rand.New(rand.NewPCG(3, 4))
	bld := NewBuilder[uint32](nil)
	for i := range 1_000_000 {
		var p netip.Prefix
		if i%5 == 0 {
			var raw [16]byte
			raw[0], raw[1] = 0x20, 0x01
			for j := 2; j < 8; j++ {
				raw[j] = byte(r.IntN(256))
			}
			p = netip.PrefixFrom(netip.AddrFrom16(raw), 32+r.IntN(33)).Masked()
		} else {
			a := netip.AddrFrom4([4]byte{byte(1 + r.IntN(223)), byte(r.IntN(256)), byte(r.IntN(256)), 0})
			p = netip.PrefixFrom(a, 16+r.IntN(17)).Masked()
		}
		_ = bld.Insert(p, uint32(i))
	}
	tbl := bld.Build()
	addrs := make([]netip.Addr, 4096)
	for i := range addrs {
		addrs[i] = netip.AddrFrom4([4]byte{byte(1 + r.IntN(223)), byte(r.IntN(256)), byte(r.IntN(256)), byte(r.IntN(256))})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		tbl.Lookup(addrs[i&4095])
	}
}
