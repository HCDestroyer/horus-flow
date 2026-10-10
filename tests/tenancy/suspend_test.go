//go:build integration

package tenancy

import (
	"context"
	"testing"
)

// POST /platform/tenants/{id}/suspend y /resume: un ISP suspendido no obtiene
// tokens (TENANT_SUSPENDED), la operación es idempotente, se audita y emite
// horus.auth.tenant.suspended|resumed.
func TestPlatformSuspendsAndResumesISP(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	a.createUser("jefa@isp-susp.test")
	id := a.createTenant(pt, "isp-susp", "jefa@isp-susp.test")
	s := a.login("jefa@isp-susp.test", userPassword)
	s.enrollTOTP()
	_ = s.tenantToken(id)

	if r := a.post(pt, "/api/v1/platform/tenants/"+id+"/suspend", map[string]any{}); r.Status != 422 {
		t.Fatalf("sin motivo: %d %s", r.Status, r.Raw)
	}
	sus := a.must(a.post(pt, "/api/v1/platform/tenants/"+id+"/suspend", map[string]any{"reason": "impago"}), 200)
	if sus.str("status") != "suspended" {
		t.Fatalf("suspend = %s", sus.Raw)
	}
	if r := a.post(s.token, "/api/v1/auth/token", map[string]string{"tenant_id": id}); r.code() != "TENANT_SUSPENDED" {
		t.Fatalf("token de un ISP suspendido: %d %s", r.Status, r.Raw)
	}
	again := a.must(a.post(pt, "/api/v1/platform/tenants/"+id+"/suspend", map[string]any{"reason": "impago"}), 200)
	if again.Body["version"] != sus.Body["version"] {
		t.Fatalf("suspender dos veces cambió la versión: %s", again.Raw)
	}
	res := a.must(a.post(pt, "/api/v1/platform/tenants/"+id+"/resume", nil), 200)
	if res.str("status") != "active" {
		t.Fatalf("resume = %s", res.Raw)
	}
	if r := a.post(s.token, "/api/v1/auth/token", map[string]string{"tenant_id": id}); r.Status != 200 {
		t.Fatalf("token tras reactivar: %d %s", r.Status, r.Raw)
	}
	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM auth.outbox WHERE payload->>'type' IN
		('horus.auth.tenant.suspended', 'horus.auth.tenant.resumed') AND tenant_id = $1`, id).Scan(&n); err != nil || n != 2 {
		t.Fatalf("eventos de estado = %d (%v)", n, err)
	}
	if a.auditCount("platform.tenant.suspended") != 1 || a.auditCount("platform.tenant.resumed") != 1 {
		t.Fatal("suspensión o reactivación sin auditar")
	}
}
