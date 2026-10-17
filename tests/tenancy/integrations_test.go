//go:build integration

package tenancy

import (
	"context"
	"strings"
	"testing"
)

// H1 (ADR-0030): un API token hf_pat_ del ISP A solo ve datos de A, nunca los
// de B (mismo rango de clientes en ambos), no gestiona tokens, no sale de su
// plantilla, queda auditado con via=api_token y deja de servir al revocarlo.
func TestAPITokenTenantIsolation(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "tok-a"), a.newISP(pt, "tok-b")
	paramsA := a.fixtureISP(A, "10.10.0.0/24")
	paramsB := a.fixtureISP(B, "10.10.0.0/24")

	created := a.must(a.post(A.token, "/api/v1/api-tokens", map[string]any{"name": "ArgosISP", "template": "argosisp"}), 201)
	raw := created.str("token")
	if !strings.HasPrefix(raw, "hf_pat_") || created.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("alta = %s", created.Raw)
	}
	// El listado no muestra el valor ni el hash.
	list := a.must(a.do(req{Method: "GET", Path: "/api/v1/api-tokens", Token: A.token}), 200)
	if strings.Contains(string(list.Raw), raw) || strings.Contains(string(list.Raw), "token_hash") {
		t.Fatalf("el listado no debe exponer el token: %s", list.Raw)
	}

	// Consulta 360 con la misma IP que existe en los dos ISP: devuelve el cliente de A.
	look := a.must(a.post(raw, "/api/v1/integrations/ip-lookup", map[string]any{"address": "10.10.0.41"}), 200)
	cust, _ := look.Body["customer"].(map[string]any)
	if cust["id"] != paramsA["customer_id"] || cust["id"] == paramsB["customer_id"] {
		t.Fatalf("ip-lookup con el token de A = %s", look.Raw)
	}
	// Recursos de B por id → 404; sin permiso fuera de la plantilla → 403.
	if r := a.do(req{Method: "GET", Path: "/api/v1/customers/" + paramsB["customer_id"], Token: raw}); r.Status != 404 {
		t.Fatalf("cliente de B con el token de A = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/customers/" + paramsB["customer_id"] + "/external-identity", Token: raw}); r.Status != 404 {
		t.Fatalf("identidad de B = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/sites", Token: raw}); r.Status != 403 || r.code() != "PERMISSION_DENIED" {
		t.Fatalf("fuera de la plantilla = %d %s", r.Status, r.Raw)
	}
	// Las rutas cuyo x-principals excluye api_token lo rechazan aunque el creador tenga el permiso.
	if r := a.do(req{Method: "GET", Path: "/api/v1/api-tokens", Token: raw}); r.Status != 403 {
		t.Fatalf("un API token no gestiona tokens = %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/integrations/argosisp", Token: raw}); r.Status != 403 {
		t.Fatalf("conector con API token = %d %s", r.Status, r.Raw)
	}
	// Listados con el token de A sin ids de B.
	cl := a.must(a.do(req{Method: "GET", Path: "/api/v1/customers?limit=200", Token: raw}), 200)
	if strings.Contains(string(cl.Raw), paramsB["customer_id"]) || strings.Contains(string(cl.Raw), B.id) {
		t.Fatalf("el listado de A con su token muestra datos de B: %s", cl.Raw)
	}
	// Auditoría: la consulta 360 queda registrada con via=api_token y el id del token.
	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM auth.audit_log WHERE action = 'integrations.ip_lookup'
		AND tenant_id = $1 AND changes->>'via' = 'api_token' AND changes->>'api_token_id' = $2`, A.id, created.str("id")).Scan(&n); err != nil || n != 1 {
		t.Fatalf("auditoría de la consulta 360 con via=api_token: %d %v", n, err)
	}
	if a.auditCount("auth.api_token.created") < 1 {
		t.Fatal("alta de token sin auditar")
	}
	// Revocar: deja de servir de inmediato.
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/api-tokens/" + created.str("id"), Token: A.token}), 204)
	if r := a.post(raw, "/api/v1/integrations/ip-lookup", map[string]any{"address": "10.10.0.41"}); r.Status != 401 {
		t.Fatalf("token revocado = %d %s", r.Status, r.Raw)
	}
	// B no puede revocar tokens de A.
	other := a.must(a.post(A.token, "/api/v1/api-tokens", map[string]any{"name": "otro", "permissions": []string{"customers.read"}}), 201)
	if r := a.do(req{Method: "DELETE", Path: "/api/v1/api-tokens/" + other.str("id"), Token: B.token}); r.Status != 404 {
		t.Fatalf("B revoca un token de A = %d", r.Status)
	}
	if r := a.post(other.str("token"), "/api/v1/integrations/ip-lookup", map[string]any{"address": "10.10.0.41"}); r.Status != 200 {
		t.Fatalf("el token de A sigue vivo tras el intento de B = %d %s", r.Status, r.Raw)
	}
}
