package domain

import "testing"

func TestRouterOSVersion(t *testing.T) {
	t.Parallel()
	for v, want := range map[string][2]bool{
		"7.12": {true, true}, "7.16.1": {true, true}, "7.11.2": {true, false}, "7.1": {true, false},
		"6.49.10": {false, false}, "7": {false, false}, "8.0": {false, false}, "7.x": {false, false},
	} {
		if got := ValidRouterOSVersion(v); got != want[0] {
			t.Errorf("valid(%s) = %v", v, got)
		}
		if want[0] {
			if got := RouterOSSupported(v); got != want[1] {
				t.Errorf("supported(%s) = %v", v, got)
			}
		}
	}
}

func TestRealmAndPrefix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"10.20.0.0/24": "node_private", "100.64.0.0/10": "node_private", "172.16.5.0/24": "node_private",
		"192.168.1.0/24": "node_private", "fd00:1::/48": "node_private", "203.0.113.0/24": "public",
		"2001:db8::/32": "public", "100.0.0.0/8": "public", "10.0.0.0/7": "public",
	}
	for s, want := range cases {
		p, ok := ParseCanonicalPrefix(s)
		if !ok {
			t.Fatalf("%s no parsea", s)
		}
		if got := RealmKindFor(p); got != want {
			t.Errorf("%s: %s, want %s", s, got, want)
		}
	}
	for _, bad := range []string{"10.20.0.1/24", "nope", "10.0.0.0/33", "::ffff:10.0.0.0/104", ""} {
		if _, ok := ParseCanonicalPrefix(bad); ok {
			t.Errorf("%q aceptado", bad)
		}
	}
}
