package domain_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

func TestCanonicalAddress(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.5":                "10.0.0.5",
		"::ffff:10.0.0.5":         "10.0.0.5",
		"2001:db8:1:2:aaaa::1":    "2001:db8:1:2::/64",
		"2001:db8:1:2:bbbb::ffff": "2001:db8:1:2::/64",
	} {
		got := domain.CanonicalString(domain.CanonicalAddress(netip.MustParseAddr(in), 64))
		if got != want {
			t.Errorf("%s → %s, quiero %s", in, got, want)
		}
	}
	if got := domain.CanonicalString(domain.CanonicalAddress(netip.MustParseAddr("2001:db8:1:2::1"), 56)); got != "2001:db8:1::/56" {
		t.Errorf("/56 = %s", got)
	}
}

func TestSetKindAndReset(t *testing.T) {
	now := time.Now()
	alias := "Ferretería"
	c := &domain.Customer{Kind: "residential", KindSource: "default", DefaultKind: "residential", Alias: &alias}
	ch := c.SetKind("commercial", "contrato", nil, now)
	if ch == nil || c.KindSource != "manual" || !c.KindLocked || *ch.FromKind != "residential" {
		t.Fatalf("set-kind: %+v", c)
	}
	if c.SetKind("commercial", "otra", nil, now) != nil {
		t.Fatal("mismo tipo bloqueado debe ser sin cambio")
	}
	r := c.Reset("pasó a otro abonado", nil, now)
	if c.Kind != "residential" || c.KindLocked || c.Alias != nil || c.ResetAt == nil || r.Source != "reset" {
		t.Fatalf("reset: %+v", c)
	}
	if !domain.InactiveBefore(now, 0).Equal(now.AddDate(0, 0, -30)) || !domain.PurgeBefore(now, 0).Equal(now.AddDate(0, -25, 0)) {
		t.Fatal("cortes por defecto")
	}
}
