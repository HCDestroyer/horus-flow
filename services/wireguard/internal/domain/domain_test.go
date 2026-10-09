package domain

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

func TestValidatePools(t *testing.T) {
	t.Parallel()
	plan, err := ValidatePools([]netip.Prefix{pfx("10.255.0.0/16")}, pfx("10.255.0.0/24"))
	if err != nil || plan.HubAddress != netip.MustParseAddr("10.255.0.1") {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	bad := []struct {
		pools    []netip.Prefix
		services string
	}{
		{nil, "10.255.0.0/24"},
		{[]netip.Prefix{pfx("100.64.0.0/16")}, "100.64.0.0/24"},                         // CGNAT
		{[]netip.Prefix{pfx("10.255.0.0/16"), pfx("10.255.128.0/17")}, "10.255.0.0/24"}, // solape
		{[]netip.Prefix{pfx("10.255.0.0/16")}, "10.254.0.0/24"},                         // servicios fuera
		{[]netip.Prefix{pfx("10.255.0.0/24")}, "10.255.0.0/24"},                         // sin hueco
		{[]netip.Prefix{pfx("fd00::/64")}, "fd00::/120"},                                // IPv6
	}
	for i, b := range bad {
		if _, err := ValidatePools(b.pools, pfx(b.services)); !errors.Is(err, ErrInvalidPool) {
			t.Errorf("caso %d aceptado: %v", i, err)
		}
	}
}

func TestNextFreeAndExhaustion(t *testing.T) {
	t.Parallel()
	pool, svc := pfx("10.255.0.0/28"), pfx("10.255.0.0/30")
	used := map[netip.Addr]bool{}
	var got []netip.Addr
	for {
		a, err := NextFree(pool, svc, used)
		if errors.Is(err, ErrPoolExhausted) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if svc.Contains(a) || a == netip.MustParseAddr("10.255.0.15") {
			t.Fatalf("asignada %s (servicios o broadcast)", a)
		}
		used[a] = true
		got = append(got, a)
	}
	if len(got) != Capacity(pool, svc) || got[0] != netip.MustParseAddr("10.255.0.4") {
		t.Fatalf("asignadas %v, capacidad %d", got, Capacity(pool, svc))
	}
	// Otro pool sin servicios: todas menos red y broadcast.
	if c := Capacity(pfx("10.254.0.0/24"), svc); c != 254 {
		t.Fatalf("capacidad = %d", c)
	}
	if c := Capacity(pfx("10.255.0.0/16"), pfx("10.255.0.0/24")); c != 65534-255 {
		t.Fatalf("capacidad /16 = %d", c)
	}
}

func TestTokenAndKeys(t *testing.T) {
	t.Parallel()
	plain, hash, err := NewToken()
	if err != nil || len(plain) != 43 || len(hash) != 32 || string(HashToken(plain)) != string(hash) {
		t.Fatalf("token %q %x %v", plain, hash, err)
	}
	now := time.Now()
	tk := &Token{ExpiresAt: now.Add(TokenTTL)}
	if !tk.Usable(now) || tk.State(now.Add(25*time.Hour)) != "expired" {
		t.Fatal("caducidad")
	}
	tk.FailedAttempts = MaxTokenFailures
	if tk.Usable(now) || tk.State(now) != "revoked" {
		t.Fatal("5 fallos no invalidan")
	}
	if !ValidPublicKey("xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=") || ValidPublicKey("xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dh=") ||
		ValidPublicKey("corta=") {
		t.Fatal("validación de clave pública")
	}
	last := now.Add(-time.Minute)
	if HandshakeStateAt(nil, now) != HandshakeNever || HandshakeStateAt(&last, now) != HandshakeOK ||
		HandshakeStateAt(&last, now.Add(5*time.Minute)) != HandshakeStale {
		t.Fatal("estado de handshake")
	}
}
