package actions

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

func finding(kind, addr string, target domain.Target, ev map[string]any) *domain.Finding {
	a := netip.MustParseAddr(addr)
	bits := 32
	if a.Is6() {
		bits = 64
	}
	return &domain.Finding{ID: uuid.MustParse("0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f40"), Kind: kind,
		Address: netip.PrefixFrom(a, bits), Target: target, Evidence: ev, CustomerKind: "residential"}
}

func TestEveryKindHasValidManualActions(t *testing.T) {
	kinds := []string{domain.KindC2, domain.KindScanning, domain.KindFanout, domain.KindSpam, domain.KindDDoS,
		domain.KindBeaconing, domain.KindOpenProxy, domain.KindCryptomining, domain.KindReputationHit}
	for _, k := range kinds {
		for _, addr := range []string{"10.20.0.41", "2001:db8:1000:5::"} {
			f := finding(k, addr, domain.Target{Type: domain.TargetRemotePort, Value: "23"}, map[string]any{"destination_ports": []int{23, 2323}})
			list := Build(f)
			if len(list) == 0 {
				t.Fatalf("%s: sin acciones", k)
			}
			if err := Check(list); err != nil {
				t.Fatalf("%s %s: %v", k, addr, err)
			}
			for i, a := range list {
				if a.Priority != i+1 {
					t.Fatalf("%s: prioridad %d en %d", k, a.Priority, i)
				}
				if a.Code == "contact_customer" && (a.CustomerMessage == nil || strings.Contains(*a.CustomerMessage, "infectado")) {
					t.Fatalf("%s: mensaje al cliente %v", k, a.CustomerMessage)
				}
			}
		}
	}
}

func TestRenderIPv4AndIPv6(t *testing.T) {
	f := finding(domain.KindScanning, "10.20.0.41", domain.Target{Type: domain.TargetRemotePort, Value: "23"},
		map[string]any{"destination_ports": []any{23.0, 2323.0}})
	list := Render(Build(f), Values(f), true)
	var block *Action
	for i := range list {
		if list[i].Code == "block_outbound_port" {
			block = &list[i]
		}
	}
	if block == nil {
		t.Fatal("falta block_outbound_port")
	}
	want := `/ip firewall filter add chain=forward src-address=10.20.0.41 protocol=tcp dst-port=23,2323 action=drop comment="horus finding 0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f40" place-before=0`
	if got := (*block.RouterOS.RenderedCommands)[0]; got != want {
		t.Fatalf("render:\n%s\n%s", got, want)
	}
	if got := (*block.RouterOS.RenderedUndoCommands)[0]; got != `/ip firewall filter remove [find comment="horus finding 0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f40"]` {
		t.Fatalf("undo: %s", got)
	}
	// Sin customers.read (o eventos/kiosco): rendered_* = null, plantillas intactas.
	plain := Render(Build(f), Values(f), false)
	for _, a := range plain {
		if a.RouterOS != nil && (a.RouterOS.RenderedCommands != nil || strings.Contains(strings.Join(a.RouterOS.Commands, ""), "10.20.0.41")) {
			t.Fatalf("%s: plantilla con IP o render sin permiso", a.Code)
		}
	}

	f6 := finding(domain.KindC2, "2001:db8:1000:5::", domain.Target{Type: domain.TargetRemoteIP, Value: "2001:db8:ffff::66"}, nil)
	for _, a := range Render(Build(f6), Values(f6), true) {
		if a.RouterOS == nil {
			continue
		}
		c := (*a.RouterOS.RenderedCommands)[0]
		if !strings.HasPrefix(c, "/ipv6 firewall") || !strings.Contains(c, "2001:db8:1000:5::/64") {
			t.Fatalf("IPv6 (D22): %s", c)
		}
	}
}
