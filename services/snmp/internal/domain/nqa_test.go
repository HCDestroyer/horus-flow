package domain

import (
	"net/netip"
	"strings"
	"testing"
)

func TestNQATestNames(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"GigabitEthernet0/1/0":     "gw-GigabitEthernet0_1_0",
		"100GE2/0/0.100":           "gw-100GE2_0_0.100",
		"Eth-Trunk1":               "gw-Eth-Trunk1",
		"XGigabitEthernet0/0/12.3": "gw-XGigabitEthernet0_0_12.3",
	} {
		if got := NQAGatewayTestName(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
		if !NQAMatchesInterface(want, in) {
			t.Errorf("%s no empareja", in)
		}
	}
	long := "XGigabitEthernet10/0/12.4000:trunk"
	n := NQAGatewayTestName(long)
	if len(n) > 32 || !strings.HasPrefix(n, "gw-h") || n != NQAGatewayTestName(long) || !NQAMatchesInterface(n, long) {
		t.Fatalf("nombre largo: %s", n)
	}
	if NQAMatchesInterface("gw-x", "") {
		t.Fatal("interfaz vacía")
	}
	if ref, _, ok := ParseNQATestName("intl-1.1.1.1"); !ok || ref != ProbeRefInternational {
		t.Fatal("intl")
	}
	if ref, tok, ok := ParseNQATestName("gw-Eth-Trunk1"); !ok || ref != ProbeRefGateway || tok != "gw-Eth-Trunk1" {
		t.Fatal("gw")
	}
	for _, bad := range []string{"gw-", "otra", ""} {
		if _, _, ok := ParseNQATestName(bad); ok {
			t.Errorf("%q", bad)
		}
	}
}

func TestBuildNQASnippet(t *testing.T) {
	t.Parallel()
	s := BuildNQASnippet([]SnippetUplink{{Name: "GigabitEthernet1/0/0", Label: "Tránsito A"}, {Name: "bad name; reset", Label: "x"}},
		DefaultInternationalRefs)
	for _, want := range []string{
		"nqa test-instance horus gw-GigabitEthernet1_0_0\n test-type icmp\n destination-address ipv4 {GATEWAY_1}\n source-interface GigabitEthernet1/0/0\n",
		" probe-count 10\n interval seconds 1\n timeout 2\n frequency 30\n start now\n quit\n",
		"nqa test-instance horus intl-1.1.1.1\n test-type icmp\n destination-address ipv4 1.1.1.1\n",
		"system-view\n", "return\n",
	} {
		if !strings.Contains(s.Script, want) {
			t.Errorf("falta %q en:\n%s", want, s.Script)
		}
	}
	if strings.Contains(s.Script, "source-interface bad name") || len(s.Warnings) != 1 || len(s.Placeholders) != 2 {
		t.Fatalf("interfaz no válida: %+v", s)
	}
	for _, line := range strings.Split(s.Script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") || strings.Contains(line, "; reset") {
			t.Fatalf("línea no permitida: %q", line)
		}
	}
	empty := BuildNQASnippet(nil, []netip.Addr{netip.MustParseAddr("2001:db8::1")})
	if len(empty.Warnings) != 1 || strings.Contains(empty.Script, "test-type icmp") {
		t.Fatalf("sin enlaces: %+v", empty)
	}
}
