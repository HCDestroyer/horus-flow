//go:build integration

package tenancy

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fixtureDetection crea en el ISP x un hallazgo abierto (como lo dejaría el
// motor) y una entrada de allowlist por la API; devuelve sus IDs.
func (a *app) fixtureDetection(x isp, site, router string) map[string]string {
	a.t.Helper()
	id, customer := uuid.Must(uuid.NewV7()), uuid.New()
	now := time.Now().UTC()
	_, err := a.admin.Exec(context.Background(), `INSERT INTO detection.finding (id, tenant_id, state, kind, severity, confidence, customer_id,
			realm_id, site_id, router_id, address, target_type, target_value, signals, summary, reasons, evidence, window_from, window_to,
			first_seen_at, last_seen_at, opened_at, updated_at, rule_version)
		VALUES ($1, $2, 'open', 'outbound_scanning', 'high', 0.9, $3, $4, $5, $6, '10.10.0.41', 'remote_port', '23', '{scanning}',
			'{"code": "outbound_scanning_port", "text": "Escaneo del puerto 23 a 1240 destinos en 5 min"}',
			'[{"code": "syn_only_ratio_high", "detail": "96 %", "weight": 0.45, "data": {"syn_ratio": 0.96}}]',
			'{"destination_ports": [23]}', $7, $8, $7, $8, $8, $8, 'outbound-scan@1')`,
		id, x.id, customer, uuid.New(), site, router, now.Add(-5*time.Minute), now)
	if err != nil {
		a.t.Fatal(err)
	}
	entry := a.must(a.post(x.token, "/api/v1/reputation/allowlist", map[string]any{"prefix": "192.0.2.0/24", "reason": "servidores propios"}), 201)
	return map[string]string{"finding_id": id.String(), "customer_id": customer.String(), "entry_id": entry.str("id")}
}

// I1-12 criterio 4: un isp_viewer no cambia estados (403) ni lee evidencia
// sin security.evidence.read (403); otro ISP → 404. Ciclo por la API.
func TestDetectionPermissions(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")
	ids := a.fixtureISP(A, "10.10.0.0/24")
	fid := ids["finding_id"]

	viewerID := a.createUser("visor@isp-a.test")
	a.grant(viewerID, A.id, "viewer")
	viewer := a.login("visor@isp-a.test", userPassword).tenantToken(A.id)

	get := a.must(a.do(req{Method: "GET", Path: "/api/v1/findings/" + fid, Token: viewer}), 200)
	if get.Body["customer"] != nil || get.Header.Get("ETag") != `"1"` || len(get.Body["recommended_actions"].([]any)) == 0 {
		t.Fatalf("viewer GET (sin customers.read no ve la IP): %s", get.Raw)
	}
	for _, op := range []string{"acknowledge", "resolve", "mark-false-positive"} {
		r := a.do(req{Method: "POST", Path: "/api/v1/findings/" + fid + "/" + op, Token: viewer, Header: map[string]string{"If-Match": `"1"`},
			Body: map[string]any{"comment": "no autorizado"}})
		if r.Status != 403 {
			t.Errorf("viewer %s: %d %s", op, r.Status, r.Raw)
		}
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/findings/" + fid + "/evidence", Token: viewer}); r.Status != 403 {
		t.Errorf("viewer evidencia: %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/reputation/allowlist", Token: viewer, Body: map[string]any{"asn": 64500, "reason": "x"}}); r.Status != 403 {
		t.Errorf("viewer allowlist: %d", r.Status)
	}
	// Otro ISP → 404, también por la evidencia y las transiciones.
	for _, p := range []string{"/api/v1/findings/" + fid, "/api/v1/findings/" + fid + "/evidence", "/api/v1/customers/" + ids["customer_id"] + "/findings"} {
		if r := a.do(req{Method: "GET", Path: p, Token: B.token}); r.Status != 404 {
			t.Errorf("B GET %s: %d %s", p, r.Status, r.Raw)
		}
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/findings", Token: B.token}); r.Status != 200 || len(r.Body["data"].([]any)) != 0 {
		t.Errorf("listado de B: %s", r.Raw)
	}

	// El administrador de A (customers.read): ve la IP y los comandos renderizados.
	full := a.must(a.do(req{Method: "GET", Path: "/api/v1/findings/" + fid, Token: A.token}), 200)
	cust, _ := full.Body["customer"].(map[string]any)
	if cust == nil || cust["address"] != "10.10.0.41" {
		t.Fatalf("customer: %s", full.Raw)
	}
	rendered := false
	for _, x := range full.Body["recommended_actions"].([]any) {
		if r, _ := x.(map[string]any)["routeros"].(map[string]any); r != nil && r["rendered_commands"] != nil {
			rendered = true
		}
	}
	if !rendered {
		t.Fatal("sin rendered_commands para quien tiene customers.read")
	}
	// Sin ClickHouse la evidencia responde 503 tras auditar el acceso.
	if r := a.do(req{Method: "GET", Path: "/api/v1/findings/" + fid + "/evidence", Token: A.token}); r.Status != 503 {
		t.Errorf("evidencia sin ClickHouse: %d %s", r.Status, r.Raw)
	}
	if a.auditCount("security.evidence.read") != 1 {
		t.Error("acceso a evidencia no auditado")
	}
	// Ciclo: sin If-Match 428; FP sin comentario 422; ack; ack otra vez 409.
	if r := a.do(req{Method: "POST", Path: "/api/v1/findings/" + fid + "/acknowledge", Token: A.token}); r.Status != 428 {
		t.Errorf("sin If-Match: %d", r.Status)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/findings/" + fid + "/mark-false-positive", Token: A.token,
		Header: map[string]string{"If-Match": `"1"`}, Body: map[string]any{}}); r.Status != 422 {
		t.Errorf("FP sin comentario: %d %s", r.Status, r.Raw)
	}
	ack := a.must(a.do(req{Method: "POST", Path: "/api/v1/findings/" + fid + "/acknowledge", Token: A.token, Header: map[string]string{"If-Match": `"1"`}}), 200)
	if ack.str("state") != "acknowledged" || ack.Header.Get("ETag") != `"2"` {
		t.Fatalf("ack: %s", ack.Raw)
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/findings/" + fid + "/acknowledge", Token: A.token, Header: map[string]string{"If-Match": `"2"`}}); r.Status != 409 || r.code() != "FINDING_STATE_INVALID" {
		t.Errorf("doble ack: %d %s", r.Status, r.Raw)
	}
	res := a.must(a.do(req{Method: "POST", Path: "/api/v1/findings/" + fid + "/resolve", Token: A.token, Header: map[string]string{"If-Match": `"2"`},
		Body: map[string]any{"comment": "cliente avisado", "actions_taken": []string{"contact_customer"}}}), 200)
	if res.str("state") != "resolved" || res.Body["resolution"].(map[string]any)["verdict"] != "resolved" {
		t.Fatalf("resolve: %s", res.Raw)
	}
	sum := a.must(a.do(req{Method: "GET", Path: "/api/v1/security/summary", Token: viewer}), 200)
	if sum.Body["affected_customers"].(float64) != 0 || sum.Body["by_security_state"].(map[string]any)["mitigated"].(float64) != 1 {
		t.Fatalf("summary tras resolver: %s", sum.Raw)
	}
	// Allowlist: B no ve ni borra la de A.
	if r := a.do(req{Method: "DELETE", Path: "/api/v1/reputation/allowlist/" + ids["entry_id"], Token: B.token}); r.Status != 404 {
		t.Errorf("B borra allowlist de A: %d", r.Status)
	}
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/reputation/allowlist/" + ids["entry_id"], Token: A.token}), 204)
	if r := a.post(A.token, "/api/v1/reputation/allowlist", map[string]any{"prefix": "192.0.2.0/24", "asn": 64500, "reason": "x"}); r.Status != 422 {
		t.Errorf("prefijo y ASN a la vez: %d %s", r.Status, r.Raw)
	}
}
