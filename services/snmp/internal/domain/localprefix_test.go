package domain

import (
	"net/netip"
	"testing"
)

func TestLocalPrefixesValidateAndRanges(t *testing.T) {
	s := DefaultCapacitySettings()
	s.LocalPrefixes = []netip.Prefix{netip.MustParsePrefix("198.51.100.77/24"), netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("2001:db8:7a00::/48"), netip.MustParsePrefix("::ffff:203.0.113.0/120")}
	if errs := s.Validate(nil); len(errs) != 0 {
		t.Fatalf("errores: %v", errs)
	}
	want := []string{"198.51.100.0/24", "203.0.113.0/24", "2001:db8:7a00::/48"}
	if len(s.LocalPrefixes) != len(want) {
		t.Fatalf("normalizados = %v", s.LocalPrefixes)
	}
	for i, p := range s.LocalPrefixes {
		if p.String() != want[i] {
			t.Fatalf("normalizados = %v", s.LocalPrefixes)
		}
	}
	bad := DefaultCapacitySettings()
	bad.LocalPrefixes = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/7"), netip.MustParsePrefix("2001::/12"), {}}
	if errs := bad.Validate(nil); len(errs) != 3 || errs[0].Field != "local_prefixes[0]" || errs[2].Code != "INVALID_FORMAT" {
		t.Fatalf("demasiado amplios / inválido: %v", errs)
	}
	lo, hi := LocalPrefixRanges([]netip.Prefix{netip.MustParsePrefix("198.51.100.64/26"), netip.MustParsePrefix("2001:db8:7a00:1::/64"),
		netip.MustParsePrefix("100.64.0.0/10")})
	wantLo := []string{"::ffff:198.51.100.0", "2001:db8:7a00::", "::ffff:100.64.0.0"}
	wantHi := []string{"::ffff:198.51.100.255", "2001:db8:7a00:ffff:ffff:ffff:ffff:ffff", "::ffff:100.127.255.255"}
	for i := range lo {
		if lo[i] != wantLo[i] || hi[i] != wantHi[i] {
			t.Errorf("rango %d = %s–%s", i, lo[i], hi[i])
		}
	}
}

func TestLocalPrefixWarnings(t *testing.T) {
	local := []netip.Prefix{netip.MustParsePrefix("198.51.100.64/26"), netip.MustParsePrefix("100.64.0.0/22"),
		netip.MustParsePrefix("2001:db8:7a00::/48")}
	clients := []netip.Prefix{netip.MustParsePrefix("100.64.2.0/24"), netip.MustParsePrefix("2001:db8:1::/48")}
	w := LocalPrefixWarnings(local, clients)
	if len(w) != 2 || w[0].Code != WarnCountedAsAggregate || w[0].Prefix != "198.51.100.64/26" ||
		w[1].Code != WarnOverlapsClientPrefix || *w[1].ClientPrefix != "100.64.2.0/24" {
		t.Fatalf("avisos = %+v", w)
	}
}

func TestMoveToPrefix(t *testing.T) {
	var s TrafficSplit
	vols := []ASNVolume{{Country: "US", BytesDown: 700}, {Country: "GT", BytesDown: 200}, {Country: "US", Local: true, BytesDown: 100},
		{NoASN: true, BytesUp: 50}}
	for _, v := range vols {
		s.Add(v, "GT")
	}
	s.MoveToPrefix(ASNVolume{Country: "US", BytesDown: 300}, "GT")
	s.MoveToPrefix(ASNVolume{Country: "GT", BytesDown: 50}, "GT")
	s.MoveToPrefix(ASNVolume{NoASN: true, BytesUp: 40}, "GT")
	s.Finish()
	if s.InternationalBytes != 400 || s.LocalBytes != 640 || s.LocalByPrefix != 390 || s.LocalByCountry != 150 || s.LocalByList != 100 ||
		s.UnknownBytes != 10 {
		t.Fatalf("reparto = %+v", s)
	}
}
