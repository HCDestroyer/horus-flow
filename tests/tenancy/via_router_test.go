//go:build integration

package tenancy

import (
	"context"
	"strings"
	"testing"
)

// Switches vía router del sitio por la API completa (gateway + devices + wireguard; ADR-0032): dos ISP con el MISMO
// switch privado (192.168.88.2) detrás de sus routers reciben direcciones Horus distintas, cada uno ve solo lo suyo;
// el router de otro ISP no sirve de camino (422), el switch de A no existe para B (404), la misma IP por el mismo router
// se rechaza (409), el router con switches no se da de baja (409) y la IP privada nunca llega a la IPAM ni al hub.
func TestSwitchesViaRouterAPI(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	ctx := context.Background()
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-va"), a.newISP(pt, "isp-vb")
	type fx struct{ site, router, sw string }
	setup := func(x isp) fx {
		site := a.must(a.post(x.token, "/api/v1/sites", map[string]any{"name": "Nodo"}), 201).str("id")
		router := a.must(a.post(x.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "rt-1", "routeros_version": "7.16"}), 201).str("id")
		sw := a.must(a.post(x.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "sw-1", "device_type": "switch",
			"switch_platform": "vrp"}), 201).str("id")
		return fx{site, router, sw}
	}
	fa, fb := setup(A), setup(B)
	onboard := func(token, sw string, body map[string]any) resp {
		return a.do(req{Method: "POST", Path: "/api/v1/routers/" + sw + "/switch-onboarding", Token: token, Body: body})
	}
	body := func(router string) map[string]any {
		return map[string]any{"connection_mode": "via_router", "via_router_id": router, "management_address": "192.168.88.2", "sflow": true}
	}
	// El router de otro ISP no es camino; el switch de A no existe para B.
	if r := onboard(A.token, fa.sw, body(fb.router)); r.Status != 422 || !strings.Contains(string(r.Raw), "VIA_ROUTER_INVALID") {
		t.Fatalf("router de otro ISP: %d %s", r.Status, r.Raw)
	}
	a.must(onboard(B.token, fa.sw, body(fb.router)), 404)
	// Un switch no es camino.
	if r := onboard(A.token, fa.sw, body(fa.sw)); r.Status != 422 {
		t.Fatalf("el propio switch como camino: %d %s", r.Status, r.Raw)
	}
	ra := a.must(onboard(A.token, fa.sw, body(fa.router)), 201)
	rb := a.must(onboard(B.token, fb.sw, body(fb.router)), 201)
	pa, _ := ra.Body["path"].(map[string]any)
	pb, _ := rb.Body["path"].(map[string]any)
	if ra.str("connection_mode") != "via_router" || pa == nil || pb == nil || pa["horus_address"] == pb["horus_address"] ||
		pa["transport"] != "wireguard" || pa["protection_level"] != "encrypted" || ra.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("caminos: A=%s B=%s", ra.Raw, rb.Raw)
	}
	cfg, _ := ra.Body["router_config"].(map[string]any)
	if cfg == nil || !strings.Contains(cfg["config"].(string), "action=netmap to-addresses=192.168.88.2") ||
		!strings.Contains(ra.str("config"), "rule 5 permit source 10.255.0.1 0") {
		t.Fatalf("configuraciones: %s", ra.Raw)
	}
	// Ficha del switch: camino y protección heredada; la IP privada sigue siendo la de gestión.
	got := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/" + fa.sw, Token: A.token}), 200)
	via, _ := got.Body["via_router"].(map[string]any)
	if got.str("connection_mode") != "via_router" || got.str("via_router_id") != fa.router || got.str("via_address") != pa["horus_address"] ||
		got.str("management_address") != "192.168.88.2" || via == nil || via["name"] != "rt-1" || via["connection_mode"] != "wireguard" {
		t.Fatalf("ficha del switch: %s", got.Raw)
	}
	// Lista de switches vía el router: A ve el suyo; B con el router de A no ve nada.
	list := func(token, router string) int {
		r := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers?device_type=switch&via_router_id=" + router, Token: token}), 200)
		return len(r.Body["data"].([]any))
	}
	if list(A.token, fa.router) != 1 || list(B.token, fa.router) != 0 {
		t.Fatalf("switches vía rt-1: A=%d B=%d", list(A.token, fa.router), list(B.token, fa.router))
	}
	// La misma IP privada por el mismo router en el mismo ISP → 409.
	sw2 := a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": fa.site, "name": "sw-2", "device_type": "switch",
		"switch_platform": "routeros"}), 201).str("id")
	b2 := body(fa.router)
	delete(b2, "sflow")
	if r := onboard(A.token, sw2, b2); r.Status != 409 || !strings.Contains(string(r.Raw), "MANAGEMENT_ADDRESS_IN_USE") {
		t.Fatalf("misma IP por el mismo router: %d %s", r.Status, r.Raw)
	}
	// La IP de gestión de un switch vía router solo cambia con su alta.
	if r := a.do(req{Method: "PATCH", Path: "/api/v1/routers/" + fa.sw, Token: A.token, Body: map[string]any{"management_address": "192.168.88.3"},
		Header: map[string]string{"If-Match": got.Header.Get("ETag")}}); r.Status != 422 {
		t.Fatalf("PATCH de la IP de gestión: %d %s", r.Status, r.Raw)
	}
	// El router con switches vía él no se da de baja.
	rt := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/" + fa.router, Token: A.token}), 200)
	if r := a.do(req{Method: "DELETE", Path: "/api/v1/routers/" + fa.router, Token: A.token, Header: map[string]string{"If-Match": rt.Header.Get("ETag")}}); r.Status != 409 ||
		!strings.Contains(string(r.Raw), "ROUTER_HAS_VIA_SWITCHES") {
		t.Fatalf("baja del router con switches: %d %s", r.Status, r.Raw)
	}
	// IPAM: direcciones Horus distintas, cada una de su ISP; nunca la IP privada.
	rows, err := a.admin.Query(ctx, `SELECT tenant_id::text, host(mapped), host(target) FROM wireguard.via_mapping WHERE released_at IS NULL
		AND tenant_id IN ($1, $2)`, A.id, B.id)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for rows.Next() {
		var tenant, mapped, target string
		if err := rows.Scan(&tenant, &mapped, &target); err != nil {
			t.Fatal(err)
		}
		if target != "192.168.88.2" || strings.HasPrefix(mapped, "192.168.") {
			t.Fatalf("asignación: %s %s %s", tenant, mapped, target)
		}
		seen[tenant] = mapped
	}
	rows.Close()
	if len(seen) != 2 || seen[A.id] != pa["horus_address"] || seen[B.id] != pb["horus_address"] {
		t.Fatalf("asignaciones por ISP: %v", seen)
	}
	var private int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM wireguard.ip_allocation WHERE address <<= '192.168.0.0/16'`).Scan(&private); err != nil || private != 0 {
		t.Fatalf("IP privadas en la IPAM: %d %v", private, err)
	}
	// RLS de devices.management_subnet (tabla nueva).
	var forced bool
	if err := a.admin.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE oid = 'devices.management_subnet'::regclass`).Scan(&forced); err != nil || !forced {
		t.Fatalf("devices.management_subnet sin RLS forzada: %v", err)
	}
	if err := a.admin.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE oid = 'wireguard.via_mapping'::regclass`).Scan(&forced); err != nil || !forced {
		t.Fatalf("wireguard.via_mapping sin RLS forzada: %v", err)
	}
	// Vuelta a modo directo: libera el camino.
	a.must(onboard(A.token, fa.sw, map[string]any{"connection_mode": "direct"}), 201)
	got = a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/" + fa.sw, Token: A.token}), 200)
	if got.str("connection_mode") != "direct" || got.Body["via_router_id"] != nil || got.Body["via_address"] != nil {
		t.Fatalf("vuelta a directo: %s", got.Raw)
	}
}
