package domain_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

func TestKioskCode(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		c := domain.NewKioskCode()
		if !domain.ValidKioskCode(c) {
			t.Fatalf("código inválido %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 195 {
		t.Fatal("códigos repetidos")
	}
	for _, bad := range []string{"", "ABCDEFG", "ABCDEFGI", "abcdefgh", "ABCDEFG0", "ABCDEFGH1"} {
		if domain.ValidKioskCode(bad) {
			t.Errorf("%q aceptado", bad)
		}
	}
}

func TestKioskStatusAndCIDR(t *testing.T) {
	now := time.Now()
	k := &domain.Kiosk{Status: domain.KioskActive, ExpiresAt: now.Add(time.Hour)}
	if k.EffectiveStatus(now) != domain.KioskActive || !k.AllowsIP("198.51.100.7") {
		t.Fatal("activo sin CIDR")
	}
	old := now.Add(-15 * 24 * time.Hour)
	k.LastSeenAt = &old
	if k.EffectiveStatus(now) != domain.KioskExpired {
		t.Fatal("inactividad de 14 días")
	}
	k.LastSeenAt, k.ExpiresAt = nil, now.Add(-time.Second)
	if k.EffectiveStatus(now) != domain.KioskExpired {
		t.Fatal("caducidad absoluta")
	}
	k.AllowedCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	if !k.AllowsIP("10.1.2.3") || k.AllowsIP("192.0.2.1") || k.AllowsIP("nope") {
		t.Fatal("allowed_cidrs")
	}
	if len(domain.NewKioskCredential()) != 43 || len(domain.HashSecret("x")) != 32 {
		t.Fatal("credencial")
	}
}
