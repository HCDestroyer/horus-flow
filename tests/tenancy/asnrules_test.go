//go:build integration

package tenancy

import (
	"testing"
)

// ASN propios por la API: el administrador del ISP nombra un ASN, lo edita
// (If-Match) y lo borra; el visor (settings.read) lo ve pero no lo cambia; otro
// ISP no ve la regla ni puede tocarla (404) y puede nombrar el mismo ASN.
func TestAsnRules(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")

	cat := a.must(a.do(req{Method: "GET", Path: "/api/v1/asn-rules/catalog", Token: A.token}), 200)
	if len(cat.Body["categories"].([]any)) == 0 || len(cat.Body["services"].([]any)) == 0 {
		t.Fatalf("catálogo: %s", cat.Raw)
	}
	created := a.must(a.post(A.token, "/api/v1/asn-rules", map[string]any{"asn": 263237, "name": "Fibra Andina", "category": "cdn_infra"}), 201)
	id := created.str("id")
	if created.Header.Get("ETag") != `"1"` || created.Header.Get("Location") != "/api/v1/asn-rules/"+id || created.Body["service"] != nil ||
		created.str("tenant_id") != A.id || created.Body["asn"] != float64(263237) {
		t.Fatalf("alta: %s", created.Raw)
	}
	dup := a.post(A.token, "/api/v1/asn-rules", map[string]any{"asn": 263237, "name": "Otra", "category": "web"})
	if dup.Status != 422 || dup.Body["errors"].([]any)[0].(map[string]any)["field"] != "asn" {
		t.Fatalf("ASN repetido: %d %s", dup.Status, dup.Raw)
	}
	bad := a.post(A.token, "/api/v1/asn-rules", map[string]any{"asn": 0, "name": "", "category": "nope", "service": "nope"})
	if bad.Status != 422 || len(bad.Body["errors"].([]any)) != 4 {
		t.Fatalf("validación: %d %s", bad.Status, bad.Raw)
	}

	viewerID := a.createUser("visor@isp-a.test")
	a.grant(viewerID, A.id, "viewer")
	viewer := a.login("visor@isp-a.test", userPassword).tenantToken(A.id)
	list := a.must(a.do(req{Method: "GET", Path: "/api/v1/asn-rules", Token: viewer}), 200)
	if rows := list.Body["data"].([]any); len(rows) != 1 || rows[0].(map[string]any)["name"] != "Fibra Andina" {
		t.Fatalf("listado del visor: %s", list.Raw)
	}
	if r := a.post(viewer, "/api/v1/asn-rules", map[string]any{"asn": 64512, "name": "X", "category": "web"}); r.Status != 403 {
		t.Fatalf("visor crea regla: %d", r.Status)
	}

	// Otro ISP: no ve la regla, no la edita ni la borra, y nombra el mismo ASN a su manera.
	if r := a.must(a.do(req{Method: "GET", Path: "/api/v1/asn-rules", Token: B.token}), 200); len(r.Body["data"].([]any)) != 0 {
		t.Fatalf("B ve reglas de A: %s", r.Raw)
	}
	for _, m := range []string{"PATCH", "DELETE"} {
		r := a.do(req{Method: m, Path: "/api/v1/asn-rules/" + id, Token: B.token, Header: map[string]string{"If-Match": `"1"`},
			Body: map[string]any{"name": "secuestro"}})
		if r.Status != 404 || r.code() != "ASN_RULE_NOT_FOUND" {
			t.Fatalf("B %s regla de A: %d %s", m, r.Status, r.Raw)
		}
	}
	a.must(a.post(B.token, "/api/v1/asn-rules", map[string]any{"asn": 263237, "name": "Tránsito B", "service": "netflix"}), 201)

	a.must(a.do(req{Method: "PATCH", Path: "/api/v1/asn-rules/" + id, Token: A.token, Body: map[string]any{"name": "x"}}), 428)
	upd := a.must(a.do(req{Method: "PATCH", Path: "/api/v1/asn-rules/" + id, Token: A.token, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"name": "Fibra Andina SA", "service": "netflix"}}), 200)
	if upd.str("name") != "Fibra Andina SA" || upd.str("service") != "netflix" || upd.Header.Get("ETag") != `"2"` {
		t.Fatalf("edición: %s", upd.Raw)
	}
	stale := a.do(req{Method: "DELETE", Path: "/api/v1/asn-rules/" + id, Token: A.token, Header: map[string]string{"If-Match": `"1"`}})
	if stale.Status != 412 || stale.Body["current"] == nil {
		t.Fatalf("If-Match viejo: %d %s", stale.Status, stale.Raw)
	}
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/asn-rules/" + id, Token: A.token, Header: map[string]string{"If-Match": `"2"`}}), 204)
	if r := a.must(a.do(req{Method: "GET", Path: "/api/v1/asn-rules", Token: A.token}), 200); len(r.Body["data"].([]any)) != 0 {
		t.Fatalf("A tras borrar: %s", r.Raw)
	}
	if r := a.must(a.do(req{Method: "GET", Path: "/api/v1/asn-rules", Token: B.token}), 200); len(r.Body["data"].([]any)) != 1 {
		t.Fatalf("B tras el borrado de A: %s", r.Raw)
	}
}
