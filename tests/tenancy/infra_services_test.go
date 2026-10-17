//go:build integration

package tenancy

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/tenanttest"
	"github.com/hcdestroyer/horus-flow/services/snmp"
)

// Servicios de infraestructura por la API (rol snmp con PostgreSQL): el
// administrador del ISP da de alta un servicio con chequeos, lo edita
// (If-Match, los chequeos con id conservan su estado) y lo borra; el visor
// (snmp.read) lo ve pero no lo cambia; otro ISP no lo ve ni lo toca (404).
func TestInfraServicesAPI(t *testing.T) {
	t.Parallel()
	a := startAppWith(t, []roleFactory{{snmp.Role, snmp.Register}}, "HORUS_SNMP_ENABLED=false")
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")
	// Esquema snmp: RLS fail-closed en las tablas de negocio (el outbox solo lo lee el relay).
	if tables := tenanttest.AssertRLS(t, a.admin, []string{"snmp"}, []string{"snmp.outbox"}, []string{"snmp.outbox"}); len(tables) != 9 {
		t.Fatalf("tablas con tenant_id en snmp: %v", tables)
	}

	body := map[string]any{"name": "IPTV cabecera", "target": "192.168.10.20", "kind": "iptv", "failures_before_down": 4,
		"checks": []map[string]any{{"type": "icmp"}, {"type": "http", "port": 8080, "path": "/health"},
			{"type": "ddos_inbound", "pps_threshold": 50000}}}
	created := a.must(a.post(A.token, "/api/v1/infra-services", body), 201)
	id := created.str("id")
	if created.Header.Get("ETag") != `"1"` || created.Header.Get("Location") != "/api/v1/infra-services/"+id ||
		created.str("tenant_id") != A.id || created.Body["target_private"] != true || created.Body["interval_seconds"] != float64(60) ||
		created.Body["failures_before_down"] != float64(4) || created.Body["status"].(map[string]any)["state"] != "unknown" {
		t.Fatalf("alta: %s", created.Raw)
	}
	checks := created.Body["checks"].([]any)
	if len(checks) != 3 || checks[0].(map[string]any)["id"] == nil || len(created.Body["check_status"].([]any)) != 3 {
		t.Fatalf("chequeos: %s", created.Raw)
	}
	if r := a.post(A.token, "/api/v1/infra-services", body); r.Status != 409 {
		t.Fatalf("nombre repetido: %d %s", r.Status, r.Raw)
	}
	bad := a.post(A.token, "/api/v1/infra-services", map[string]any{"name": "x", "target": "no-ip", "kind": "dns", "interval_seconds": 10,
		"checks": []map[string]any{{"type": "tcp"}}})
	if bad.Status != 422 {
		t.Fatalf("validación: %d %s", bad.Status, bad.Raw)
	}
	// La red de servicios de Horus en el túnel (y la de contenedores) no se puede sondear.
	internal := a.post(A.token, "/api/v1/infra-services", map[string]any{"name": "colector", "target": "10.255.0.10", "kind": "other",
		"checks": []map[string]any{{"type": "tcp", "port": 4739}}})
	if internal.Status != 422 {
		t.Fatalf("objetivo interno: %d %s", internal.Status, internal.Raw)
	}

	viewerID := a.createUser("visor@isp-a.test")
	a.grant(viewerID, A.id, "viewer")
	viewer := a.login("visor@isp-a.test", userPassword).tenantToken(A.id)
	list := a.must(a.do(req{Method: "GET", Path: "/api/v1/infra-services", Token: viewer}), 200)
	if rows := list.Body["data"].([]any); len(rows) != 1 || rows[0].(map[string]any)["name"] != "IPTV cabecera" {
		t.Fatalf("listado del visor: %s", list.Raw)
	}
	if sch := list.Body["meta"].(map[string]any)["scheduler"].(map[string]any); sch["enabled"] != false {
		t.Fatalf("meta.scheduler con HORUS_SNMP_ENABLED=false: %v", sch)
	}
	if r := a.post(viewer, "/api/v1/infra-services", body); r.Status != 403 {
		t.Fatalf("visor crea servicio: %d", r.Status)
	}
	detail := a.must(a.do(req{Method: "GET", Path: "/api/v1/infra-services/" + id, Token: viewer}), 200)
	if _, ok := detail.Body["check_status"].([]any)[0].(map[string]any)["recent"]; !ok {
		t.Fatalf("el detalle trae recent: %s", detail.Raw)
	}

	// Otro ISP: no lo ve, no lo edita ni lo borra.
	if r := a.must(a.do(req{Method: "GET", Path: "/api/v1/infra-services", Token: B.token}), 200); len(r.Body["data"].([]any)) != 0 {
		t.Fatalf("B ve servicios de A: %s", r.Raw)
	}
	for _, m := range []string{"GET", "PATCH", "DELETE"} {
		r := a.do(req{Method: m, Path: "/api/v1/infra-services/" + id, Token: B.token, Header: map[string]string{"If-Match": `"1"`},
			Body: map[string]any{"name": "secuestro"}})
		if r.Status != 404 {
			t.Fatalf("B %s servicio de A: %d %s", m, r.Status, r.Raw)
		}
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/infra-services/" + id + "/router-snippet?router_id=" + uuid.NewString(), Token: B.token}); r.Status != 404 {
		t.Fatalf("B pide el script de A: %d", r.Status)
	}

	// Edición: se quita ddos, se conserva el id de icmp y http.
	keep := []any{checks[0], checks[1]}
	a.must(a.do(req{Method: "PATCH", Path: "/api/v1/infra-services/" + id, Token: A.token, Body: map[string]any{"name": "x"}}), 428)
	upd := a.must(a.do(req{Method: "PATCH", Path: "/api/v1/infra-services/" + id, Token: A.token, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"name": "IPTV principal", "checks": keep, "enabled": false}}), 200)
	if upd.str("name") != "IPTV principal" || upd.Header.Get("ETag") != `"2"` || len(upd.Body["checks"].([]any)) != 2 ||
		upd.Body["checks"].([]any)[0].(map[string]any)["id"] != checks[0].(map[string]any)["id"] || upd.Body["enabled"] != false {
		t.Fatalf("edición: %s", upd.Raw)
	}
	var states int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM snmp.infra_check_state WHERE service_id = $1`, id).Scan(&states); err != nil || states != 2 {
		t.Fatalf("estado de chequeos tras quitar uno: %d (%v)", states, err)
	}
	stale := a.do(req{Method: "DELETE", Path: "/api/v1/infra-services/" + id, Token: A.token, Header: map[string]string{"If-Match": `"1"`}})
	if stale.Status != 412 || stale.Body["current"] == nil {
		t.Fatalf("If-Match viejo: %d %s", stale.Status, stale.Raw)
	}
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/infra-services/" + id, Token: A.token, Header: map[string]string{"If-Match": `"2"`}}), 204)
	if r := a.must(a.do(req{Method: "GET", Path: "/api/v1/infra-services", Token: A.token}), 200); len(r.Body["data"].([]any)) != 0 {
		t.Fatalf("A tras borrar: %s", r.Raw)
	}
}
