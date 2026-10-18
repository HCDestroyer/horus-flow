//go:build integration

package tenancy

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/tenanttest"
	"github.com/hcdestroyer/horus-flow/services/snmp"
)

// Capacidad de enlaces por la API (rol snmp con PostgreSQL, sin ClickHouse):
// ajustes por ISP (B no ve ni cambia los de A; validación de destinos
// internos), la configuración de un enlace de A no se ve, ni se edita ni se
// borra desde B (404), el visor (snmp.read) lee pero no escribe, y el script
// netwatch de un router de A no se sirve a B. RLS en las tablas nuevas.
func TestCapacityAPI(t *testing.T) {
	t.Parallel()
	a := startAppWith(t, []roleFactory{{snmp.Role, snmp.Register}}, "HORUS_SNMP_ENABLED=false")
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-ca"), a.newISP(pt, "isp-cb")
	if tables := tenanttest.AssertRLS(t, a.admin, []string{"snmp"}, []string{"snmp.outbox"}, []string{"snmp.outbox"}); len(tables) != 11 {
		t.Fatalf("tablas con tenant_id en snmp: %v", tables)
	}

	ov := a.must(a.do(req{Method: "GET", Path: "/api/v1/capacity/overview", Token: B.token}), 200)
	if rows := ov.Body["data"].([]any); len(rows) != 0 || ov.Body["meta"].(map[string]any)["store"] != true ||
		ov.Body["settings"].(map[string]any)["planning_threshold"] != 0.8 {
		t.Fatalf("overview: %s", ov.Raw)
	}
	// Enlace configurado de A (router aún sin sondeo): B no lo ve ni lo toca.
	router, iface := uuid.New(), uuid.New()
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO snmp.uplink_config (tenant_id, interface_id, router_id, label, kind,
		contracted_bps, international_egress) VALUES ($1, $2, $3, 'Proveedor A', 'international_transit', 500000000, true)`, A.id, iface, router); err != nil {
		t.Fatal(err)
	}
	set := a.must(a.do(req{Method: "GET", Path: "/api/v1/capacity/settings", Token: A.token}), 200)
	if set.Body["customized"] != false || set.Header.Get("ETag") != `"0"` {
		t.Fatalf("ajustes por defecto: %s", set.Raw)
	}
	body := map[string]any{"country": "gt", "planning_threshold": 0.75, "urgent_threshold": 0.9, "lead_days": 120, "saturation_warning": 0.7,
		"saturation_critical": 0.85, "sustained_minutes": 15, "loss_warning_pct": 1, "loss_critical_pct": 5, "latency_warning_ms": 80,
		"latency_critical_ms": 200, "local_asns": []int{15169, 2906}, "probe_targets": []map[string]any{{"label": "Borde ISP", "address": "203.0.113.1"}}}
	put := a.must(a.do(req{Method: "PUT", Path: "/api/v1/capacity/settings", Token: A.token, Body: body}), 200)
	if put.Body["customized"] != true || put.Body["country"] != "GT" || put.Body["effective_country"] != "GT" || put.Header.Get("ETag") != `"1"` {
		t.Fatalf("guardar ajustes: %s", put.Raw)
	}
	internal := map[string]any{}
	for k, v := range body {
		internal[k] = v
	}
	internal["probe_targets"] = []map[string]any{{"label": "Colector", "address": "10.255.0.10"}}
	if r := a.do(req{Method: "PUT", Path: "/api/v1/capacity/settings", Token: A.token, Body: internal}); r.Status != 422 {
		t.Fatalf("destino interno de Horus: %d %s", r.Status, r.Raw)
	}
	if b := a.must(a.do(req{Method: "GET", Path: "/api/v1/capacity/settings", Token: B.token}), 200); b.Body["customized"] != false {
		t.Fatalf("B ve los ajustes de A: %s", b.Raw)
	}
	if a.auditCount("snmp.capacity_settings.updated") != 1 {
		t.Fatal("ajustes no auditados")
	}

	ovA := a.must(a.do(req{Method: "GET", Path: "/api/v1/capacity/overview", Token: A.token}), 200)
	if rows := ovA.Body["data"].([]any); len(rows) != 1 || ovA.Body["egress"] == nil {
		t.Fatalf("A no ve su enlace: %s", ovA.Raw)
	}
	if ovB := a.must(a.do(req{Method: "GET", Path: "/api/v1/capacity/overview", Token: B.token}), 200); len(ovB.Body["data"].([]any)) != 0 ||
		ovB.Body["egress"] != nil {
		t.Fatalf("B ve el enlace de A: %s", ovB.Raw)
	}
	up := map[string]any{"router_id": router, "label": "secuestro", "kind": "other"}
	for _, r := range []resp{
		a.do(req{Method: "GET", Path: "/api/v1/capacity/uplinks/" + iface.String(), Token: B.token}),
		a.do(req{Method: "PUT", Path: "/api/v1/capacity/uplinks/" + iface.String(), Token: B.token, Body: up}),
		a.do(req{Method: "DELETE", Path: "/api/v1/capacity/uplinks/" + iface.String(), Token: B.token}),
		a.do(req{Method: "GET", Path: "/api/v1/capacity/egress", Token: B.token}),
		a.do(req{Method: "GET", Path: "/api/v1/routers/" + router.String() + "/netwatch-snippet", Token: B.token}),
	} {
		if r.Status != 404 {
			t.Fatalf("B sobre recursos de A: %d %s", r.Status, r.Raw)
		}
	}
	viewerID := a.createUser("visor@isp-ca.test")
	a.grant(viewerID, A.id, "viewer")
	viewer := a.login("visor@isp-ca.test", userPassword).tenantToken(A.id)
	a.must(a.do(req{Method: "GET", Path: "/api/v1/capacity/overview", Token: viewer}), 200)
	if r := a.do(req{Method: "PUT", Path: "/api/v1/capacity/uplinks/" + iface.String(), Token: viewer, Body: up}); r.Status != 403 {
		t.Fatalf("visor edita un enlace: %d", r.Status)
	}
	if r := a.do(req{Method: "PUT", Path: "/api/v1/capacity/settings", Token: viewer, Body: body}); r.Status != 403 {
		t.Fatalf("visor edita ajustes: %d", r.Status)
	}
	// A edita (el router no se sondea: solo vale el router ya configurado) y borra su enlace.
	up["label"] = "Proveedor A 1G"
	upd := a.must(a.do(req{Method: "PUT", Path: "/api/v1/capacity/uplinks/" + iface.String(), Token: A.token, Body: up,
		Header: map[string]string{"If-Match": `"1"`}}), 200)
	if upd.Body["label"] != "Proveedor A 1G" || upd.Header.Get("ETag") != `"2"` || upd.Body["international_egress"] != false {
		t.Fatalf("edición: %s", upd.Raw)
	}
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/capacity/uplinks/" + iface.String(), Token: A.token}), 204)
	if a.auditCount("snmp.uplink_config.updated") != 1 || a.auditCount("snmp.uplink_config.deleted") != 1 {
		t.Fatal("enlace no auditado")
	}
	if r := a.must(a.do(req{Method: "GET", Path: "/api/v1/capacity/traffic-split", Token: A.token}), 200); r.Body["country_source"] != "settings" {
		t.Fatalf("reparto: %s", r.Raw)
	}
}
