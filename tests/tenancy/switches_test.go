//go:build integration

package tenancy

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/tenanttest"
	"github.com/hcdestroyer/horus-flow/services/snmp"
)

// Switches por la API (devices + rol snmp con PostgreSQL, sin sondeo ni ClickHouse): alta de un switch en A
// (nunca principal, siempre directo), su alta con credencial SNMP solo para A (B → 404; un router → 422),
// el listado de routers no incluye switches y B no ve los de A con device_type=switch; las rutas /switches/*
// del switch de A responden 404 a B, la elección de puertos de A (RLS en snmp.switch_port*) no se ve desde B
// y el visor no la escribe.
func TestSwitchesAPI(t *testing.T) {
	t.Parallel()
	a := startAppWith(t, []roleFactory{{snmp.Role, snmp.Register}}, "HORUS_SNMP_ENABLED=false")
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-sa"), a.newISP(pt, "isp-sb")
	site := a.must(a.post(A.token, "/api/v1/sites", map[string]any{"name": "Nodo"}), 201).str("id")
	router := a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "rt-1"}), 201).str("id")
	sw := a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "sw-1", "device_type": "switch",
		"switch_platform": "swos"}), 201)
	if sw.Body["device_type"] != "switch" || sw.Body["is_primary"] != false || sw.Body["connection_mode"] != "direct" ||
		sw.Body["vendor"] != "mikrotik" || sw.Body["protection_level"] != "unencrypted_private" {
		t.Fatalf("alta del switch: %s", sw.Raw)
	}
	swID := sw.str("id")
	// Un switch nunca es principal; sFlow solo en la serie S.
	a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "sw-2", "device_type": "switch",
		"switch_platform": "vrp", "is_primary": true}), 422)
	// Listados: el de routers no cambia; device_type=switch solo ve los del ISP.
	if rows := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers", Token: A.token}), 200).Body["data"].([]any); len(rows) != 1 {
		t.Fatalf("routers de A: %d", len(rows))
	}
	if rows := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers?device_type=switch", Token: A.token}), 200).Body["data"].([]any); len(rows) != 1 {
		t.Fatalf("switches de A: %d", len(rows))
	}
	if rows := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers?device_type=all", Token: B.token}), 200).Body["data"].([]any); len(rows) != 0 {
		t.Fatalf("B ve equipos de A: %v", rows)
	}
	// Alta del switch: comunidad v2c solo en la respuesta; B → 404; un router → 422.
	ob := "/api/v1/routers/" + swID + "/switch-onboarding"
	a.must(a.do(req{Method: "POST", Path: ob, Token: B.token, Body: map[string]any{"management_address": "10.20.0.5"}}), 404)
	a.must(a.do(req{Method: "POST", Path: "/api/v1/routers/" + router + "/switch-onboarding", Token: A.token,
		Body: map[string]any{"management_address": "10.20.0.6"}}), 422)
	a.must(a.do(req{Method: "POST", Path: ob, Token: A.token, Body: map[string]any{"management_address": "10.255.0.9"}}), 422) // IP de Horus
	res := a.must(a.do(req{Method: "POST", Path: ob, Token: A.token, Body: map[string]any{"management_address": "10.20.0.5"}}), 201)
	if res.Body["snmp_version"] != "2c" || res.Body["language"] != "swos_web" || !strings.Contains(res.str("config"), "Community = ") ||
		res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("alta SwOS: %s", res.Raw)
	}
	a.must(a.do(req{Method: "POST", Path: ob, Token: A.token, Body: map[string]any{"sflow": true}}), 422) // SwOS sin sFlow
	// La credencial no se devuelve después; el estado dice que está configurada.
	got := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/" + swID, Token: A.token}), 200)
	if strings.Contains(string(got.Raw), "Community") || got.Body["management_address"] != "10.20.0.5" {
		t.Fatalf("detalle: %s", got.Raw)
	}

	// snmp: RLS en las tablas nuevas.
	tables := tenanttest.AssertRLS(t, a.admin, []string{"snmp"}, []string{"snmp.outbox"}, []string{"snmp.outbox"})
	for _, want := range []string{"snmp.switch_port_selection", "snmp.switch_port"} {
		found := false
		for _, x := range tables {
			found = found || (x.Table == want && x.Forced)
		}
		if !found {
			t.Fatalf("%s sin RLS: %v", want, tables)
		}
	}
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO snmp.switch_port_selection (tenant_id, router_id, updated_by) VALUES ($1, $2, 'user:a')`,
		A.id, uuid.MustParse(swID)); err != nil {
		t.Fatal(err)
	}
	// Rutas /switches/* del switch de A desde B → 404; la lista de B no tiene switches de A.
	for _, r := range []req{
		{Method: "GET", Path: "/api/v1/switches/" + swID + "/ports", Token: B.token},
		{Method: "PUT", Path: "/api/v1/switches/" + swID + "/ports", Token: B.token, Body: map[string]any{"ports": []any{}}},
		{Method: "DELETE", Path: "/api/v1/switches/" + swID + "/ports", Token: B.token},
		{Method: "GET", Path: "/api/v1/switches/" + swID + "/neighbors", Token: B.token},
		{Method: "GET", Path: "/api/v1/switches/" + swID + "/flows/top", Token: B.token},
	} {
		a.must(a.do(r), 404)
	}
	if rows := a.must(a.do(req{Method: "GET", Path: "/api/v1/switches/status", Token: B.token}), 200).Body["data"].([]any); len(rows) != 0 {
		t.Fatalf("B ve switches: %v", rows)
	}
	// Visor de A: lee el estado pero no cambia la elección de puertos.
	viewerID := a.createUser("viewer@isp-sa.test")
	a.grant(viewerID, A.id, "viewer")
	viewer := a.login("viewer@isp-sa.test", userPassword).tenantToken(A.id)
	a.must(a.do(req{Method: "GET", Path: "/api/v1/switches/status", Token: viewer}), 200)
	if r := a.do(req{Method: "PUT", Path: "/api/v1/switches/" + swID + "/ports", Token: viewer, Body: map[string]any{"ports": []any{}}}); r.Status != 403 {
		t.Fatalf("visor escribe la elección: %d %s", r.Status, r.Raw)
	}
	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM snmp.switch_port_selection WHERE tenant_id = $1`, A.id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("elección de A: %d %v", n, err)
	}
}
