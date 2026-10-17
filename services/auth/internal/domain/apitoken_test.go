package domain

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestGenerateAPIToken(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 50 {
		raw, err := GenerateAPIToken()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(raw, "hf_pat_") || len(raw) != len("hf_pat_")+43 || !LooksLikeAPIToken(raw) {
			t.Fatalf("token %q", raw)
		}
		if seen[raw] {
			t.Fatal("token repetido")
		}
		seen[raw] = true
		if h := HashAPIToken(raw); len(h) != 32 || bytes.Equal(h, HashAPIToken(raw+"x")) {
			t.Fatal("hash")
		}
	}
	for _, bad := range []string{"", "hf_pat_", "hmcp_abc", "hf_pat_" + strings.Repeat("a", 42), "hf_pat_" + strings.Repeat("-", 43)} {
		if LooksLikeAPIToken(bad) {
			t.Errorf("%q no debería parecer un token", bad)
		}
	}
}

func TestAPITokenActive(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tok := APIToken{ExpiresAt: now.Add(time.Hour)}
	if !tok.Active(now) {
		t.Fatal("vigente")
	}
	if tok.Active(now.Add(2 * time.Hour)) {
		t.Fatal("caducado")
	}
	tok.RevokedAt = &now
	if tok.Active(now) {
		t.Fatal("revocado")
	}
}

func TestIntersectPermissions(t *testing.T) {
	t.Parallel()
	eff := map[string][]string{"customers.read": {"site:a"}, "snmp.read": {"*"}, "devices.read": {"*"}}
	got := IntersectPermissions([]string{"customers.read", "snmp.read", "traffic.customer.read"}, eff)
	if len(got) != 2 || got["customers.read"][0] != "site:a" || got["snmp.read"][0] != "*" {
		t.Fatalf("intersección = %v", got)
	}
	if _, ok := got["devices.read"]; ok {
		t.Fatal("un permiso del creador que el token no lleva no entra")
	}
	if len(IntersectPermissions([]string{"customers.read"}, map[string][]string{})) != 0 {
		t.Fatal("el creador sin permisos deja el token vacío")
	}
}

func TestArgosTemplate(t *testing.T) {
	t.Parallel()
	tpl, ok := APITokenTemplateByKey("argosisp")
	if !ok || len(tpl.Permissions) != 4 {
		t.Fatalf("plantilla %v", tpl)
	}
}
