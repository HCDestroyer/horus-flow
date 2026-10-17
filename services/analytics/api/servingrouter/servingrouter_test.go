package servingrouter

import (
	"net/netip"
	"testing"
)

func TestRange(t *testing.T) {
	for _, c := range []struct{ in, lo, hi string }{
		{"10.20.0.41/32", "::ffff:10.20.0.41", "::ffff:10.20.0.41"},
		{"10.20.0.0/24", "::ffff:10.20.0.0", "::ffff:10.20.0.255"},
		{"2001:db8:4af0:100::/56", "2001:db8:4af0:100::", "2001:db8:4af0:1ff:ffff:ffff:ffff:ffff"},
		{"2001:db8::7/128", "2001:db8::7", "2001:db8::7"},
	} {
		lo, hi := Range(netip.MustParsePrefix(c.in))
		if lo != netip.MustParseAddr(c.lo) || hi != netip.MustParseAddr(c.hi) {
			t.Errorf("%s → %s–%s", c.in, lo, hi)
		}
	}
}
