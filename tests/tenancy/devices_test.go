//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// isp es un ISP con su administrador (tenant_admin con TOTP) y un token.
type isp struct {
	id    string
	admin *session
	token string
}

func (a *app) newISP(pt, slug string) isp {
	a.t.Helper()
	email := "admin@" + slug + ".test"
	a.createUser(email)
	id := a.createTenant(pt, slug, email)
	s := a.login(email, userPassword)
	s.enrollTOTP() // tenant_admin exige 2FA (D14)
	return isp{id: id, admin: s, token: s.tenantToken(id)}
}

func (a *app) post(token, path string, body any) resp {
	return a.do(req{Method: "POST", Path: path, Token: token, Body: body})
}

// I0-09 criterio 1: el superadmin crea ISP con slug único y asigna su
// administrador; un isp_admin no puede crear ISP.
func TestPlatformCreatesISP(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	a.createUser("jefa@isp-uno.test")
	id := a.createTenant(pt, "isp-uno", "jefa@isp-uno.test")
	dup := a.do(req{Method: "POST", Path: "/api/v1/platform/tenants", Token: pt, Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body: map[string]any{"slug": "isp-uno", "name": "Otro", "country": "MX", "timezone": "America/Mexico_City", "initial_admin_email": "x@y.test"}})
	if dup.Status != 409 || dup.code() != "ALREADY_EXISTS" {
		t.Fatalf("slug duplicado: %d %s", dup.Status, dup.Raw)
	}
	bad := a.do(req{Method: "POST", Path: "/api/v1/platform/tenants", Token: pt, Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body: map[string]any{"slug": "UPPER", "name": "", "country": "mx", "timezone": "Mars/Base", "initial_admin_email": "nope"}})
	if bad.Status != 422 || len(bad.Body["errors"].([]any)) != 5 {
		t.Fatalf("validación: %d %s", bad.Status, bad.Raw)
	}
	got := a.must(a.do(req{Method: "GET", Path: "/api/v1/platform/tenants/" + id, Token: pt}), 200)
	if got.str("slug") != "isp-uno" || got.Header.Get("ETag") != `"1"` {
		t.Fatalf("tenant = %s", got.Raw)
	}
	upd := a.must(a.do(req{Method: "PATCH", Path: "/api/v1/platform/tenants/" + id, Token: pt, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"name": "ISP Uno SA", "quotas": map[string]any{"max_routers": 10}}}), 200)
	if upd.str("name") != "ISP Uno SA" || upd.Body["version"].(float64) != 2 {
		t.Fatalf("patch = %s", upd.Raw)
	}
	stale := a.do(req{Method: "PATCH", Path: "/api/v1/platform/tenants/" + id, Token: pt, Header: map[string]string{"If-Match": `"1"`}, Body: map[string]any{"name": "x"}})
	if stale.Status != 412 || stale.Body["current"] == nil {
		t.Fatalf("If-Match viejo: %d %s", stale.Status, stale.Raw)
	}
	a.must(a.do(req{Method: "PATCH", Path: "/api/v1/platform/tenants/" + id, Token: pt, Body: map[string]any{"name": "x"}}), 428)
	list := a.must(a.do(req{Method: "GET", Path: "/api/v1/platform/tenants?include_total=true&limit=1", Token: pt}), 200)
	if list.Body["page"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("listado = %s", list.Raw)
	}

	// La jefa (isp_admin = tenant_admin) entra a su ISP, pero no crea ISP.
	s := a.login("jefa@isp-uno.test", userPassword)
	if r := a.post(s.token, "/api/v1/auth/token", map[string]string{"tenant_id": id}); r.code() != "MFA_ENROLLMENT_REQUIRED" {
		t.Fatalf("tenant_admin sin TOTP: %s", r.Raw)
	}
	s.enrollTOTP()
	tok := s.tenantToken(id)
	if r := a.do(req{Method: "POST", Path: "/api/v1/platform/tenants", Token: tok, Header: map[string]string{"Idempotency-Key": uuid.NewString()},
		Body: map[string]any{"slug": "isp-dos"}}); r.Status != 403 {
		t.Fatalf("isp_admin crea ISP: %d %s", r.Status, r.Raw)
	}
	// Su token de sesión tampoco sirve en plataforma (no tiene rol de plataforma).
	if r := a.post(s.token, "/api/v1/auth/token", map[string]string{"scope": "platform"}); r.Status != 403 {
		t.Fatalf("token de plataforma para isp_admin: %d", r.Status)
	}
	var n int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM auth.outbox WHERE subject LIKE 'horus.auth.tenant.created.%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("eventos tenant.created = %d", n)
	}
}

// I0-09 criterios 2–5 e I0-06 criterio 3 por la API.
func TestInventory(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A := a.newISP(pt, "isp-a")

	site := a.must(a.post(A.token, "/api/v1/sites", map[string]any{"name": "Nodo Centro", "code": "CEN"}), 201)
	siteID := site.str("id")
	if site.Body["discovery_mode"] != true || site.str("private_realm_id") == "" || site.Header.Get("Location") != "/api/v1/sites/"+siteID ||
		site.str("tenant_id") != A.id || site.Header.Get("ETag") != `"1"` {
		t.Fatalf("site = %s", site.Raw)
	}
	if r := a.post(A.token, "/api/v1/sites", map[string]any{"name": "Otro", "code": "CEN"}); r.Status != 409 {
		t.Fatalf("code duplicado: %d", r.Status)
	}
	if r := a.post(A.token, "/api/v1/sites", map[string]any{"name": "X", "parent_id": uuid.NewString()}); r.Status != 422 {
		t.Fatalf("padre inexistente: %d %s", r.Status, r.Raw)
	}
	site2 := a.must(a.post(A.token, "/api/v1/sites", map[string]any{"name": "Nodo Norte"}), 201).str("id")

	// Criterio 2: router en "Pendiente de configurar" + evento de alta.
	rt := a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": siteID, "name": "rt-centro-01", "model": "CCR2116-12G-4S+", "routeros_version": "7.16.1"}), 201)
	if rt.str("onboarding_state") != "pending_configuration" || rt.Body["is_primary"] != true || rt.Body["routeros_version_supported"] != true ||
		len(rt.Body["warnings"].([]any)) != 0 || rt.Body["tunnel_address"] != nil {
		t.Fatalf("router = %s", rt.Raw)
	}
	var payload []byte
	if err := a.admin.QueryRow(context.Background(), `SELECT payload FROM devices.outbox WHERE subject = $1`,
		"horus.devices.router.created."+rt.str("id")).Scan(&payload); err != nil {
		t.Fatalf("evento router.created: %v", err)
	}
	var env map[string]any
	_ = json.Unmarshal(payload, &env)
	data := env["data"].(map[string]any)
	if env["tenant_id"] != A.id || env["source"] != "horus/devices" || data["onboarding_state"] != "pending_configuration" || data["name"] != "rt-centro-01" {
		t.Fatalf("sobre = %s", payload)
	}
	// Criterio 4: segundo router principal → 409 con explicación; no principal se acepta.
	if r := a.post(A.token, "/api/v1/routers", map[string]any{"site_id": siteID, "name": "rt-centro-02"}); r.Status != 409 || r.code() != "ROUTER_PRIMARY_EXISTS" || r.str("detail") == "" {
		t.Fatalf("segundo principal: %d %s", r.Status, r.Raw)
	}
	a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": siteID, "name": "rt-centro-02", "is_primary": false}), 201)
	// Criterio 5: RouterOS < 7.12 se acepta con aviso; RouterOS 6 se rechaza.
	old := a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": site2, "name": "rt-norte", "routeros_version": "7.10"}), 201)
	if old.Body["routeros_version_supported"] != false || fmt.Sprint(old.Body["warnings"]) != "[routeros_version_unsupported]" {
		t.Fatalf("7.10 = %s", old.Raw)
	}
	if r := a.post(A.token, "/api/v1/routers", map[string]any{"site_id": site2, "name": "rt-v6", "routeros_version": "6.49", "is_primary": false}); r.Status != 422 {
		t.Fatalf("RouterOS 6: %d", r.Status)
	}
	if r := a.post(A.token, "/api/v1/routers", map[string]any{"site_id": uuid.NewString(), "name": "rt-x"}); r.Status != 422 {
		t.Fatalf("site_id inexistente: %d %s", r.Status, r.Raw)
	}
	// Edición con If-Match.
	patched := a.must(a.do(req{Method: "PATCH", Path: "/api/v1/routers/" + old.str("id"), Token: A.token, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"routeros_version": "7.16"}}), 200)
	if patched.Body["routeros_version_supported"] != true || patched.Body["version"].(float64) != 2 {
		t.Fatalf("patch router = %s", patched.Raw)
	}

	// Criterio 3 / I0-06 criterio 3: prefijos IPv4/IPv6 sin solapes por realm.
	cp := a.must(a.post(A.token, "/api/v1/sites/"+siteID+"/client-prefixes", map[string]any{"prefix": "10.20.0.0/24", "role": "customers", "assignment_mode": "dynamic"}), 201)
	if cp.str("realm_kind") != "node_private" || cp.str("realm_id") != site.str("private_realm_id") || cp.Body["ipv6_client_len"] != nil {
		t.Fatalf("prefijo = %s", cp.Raw)
	}
	if r := a.post(A.token, "/api/v1/sites/"+siteID+"/client-prefixes", map[string]any{"prefix": "10.20.0.128/25", "role": "customers"}); r.Status != 409 || r.code() != "CLIENT_PREFIX_OVERLAP" {
		t.Fatalf("solape: %d %s", r.Status, r.Raw)
	}
	a.must(a.post(A.token, "/api/v1/sites/"+site2+"/client-prefixes", map[string]any{"prefix": "10.20.0.128/25", "role": "customers"}), 201)
	pub := a.must(a.post(A.token, "/api/v1/sites/"+siteID+"/client-prefixes", map[string]any{"prefix": "2001:db8:100::/40", "role": "infrastructure", "assignment_mode": "static"}), 201)
	if pub.str("realm_kind") != "public" || pub.Body["ipv6_client_len"].(float64) != 64 {
		t.Fatalf("prefijo público v6 = %s", pub.Raw)
	}
	// Un público solapado en otro nodo choca (realm público del ISP).
	if r := a.post(A.token, "/api/v1/sites/"+site2+"/client-prefixes", map[string]any{"prefix": "2001:db8:100:1::/64", "role": "excluded"}); r.code() != "CLIENT_PREFIX_OVERLAP" {
		t.Fatalf("solape público entre nodos: %s", r.Raw)
	}
	for _, body := range []map[string]any{
		{"prefix": "10.30.0.1/24", "role": "customers"},
		{"prefix": "10.30.0.0/24", "role": "clientes"},
		{"prefix": "10.30.0.0/24", "role": "customers", "ipv6_client_len": 56},
		{"prefix": "10.30.0.0/24"},
	} {
		if r := a.post(A.token, "/api/v1/sites/"+siteID+"/client-prefixes", body); r.Status != 422 {
			t.Errorf("%v: %d %s", body, r.Status, r.Raw)
		}
	}
	var evs int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM devices.outbox WHERE subject LIKE 'horus.devices.client_prefix.created.%'`).Scan(&evs)
	if evs != 3 {
		t.Fatalf("eventos client_prefix.created = %d", evs)
	}
	s1 := a.must(a.do(req{Method: "GET", Path: "/api/v1/sites/" + siteID, Token: A.token}), 200)
	if s1.Body["discovery_mode"] != false || s1.str("primary_router_id") != rt.str("id") {
		t.Fatalf("site tras prefijos = %s", s1.Raw)
	}
	list := a.must(a.do(req{Method: "GET", Path: "/api/v1/sites/" + siteID + "/client-prefixes?limit=1", Token: A.token}), 200)
	page := list.Body["page"].(map[string]any)
	if len(list.Body["data"].([]any)) != 1 || page["has_more"] != true {
		t.Fatalf("página 1 = %s", list.Raw)
	}
	list2 := a.must(a.do(req{Method: "GET", Path: "/api/v1/sites/" + siteID + "/client-prefixes?limit=1&cursor=" + page["next_cursor"].(string), Token: A.token}), 200)
	if len(list2.Body["data"].([]any)) != 1 || list2.Body["data"].([]any)[0].(map[string]any)["id"] == list.Body["data"].([]any)[0].(map[string]any)["id"] {
		t.Fatalf("página 2 = %s", list2.Raw)
	}
	// Edición y borrado de prefijo.
	a.must(a.do(req{Method: "PATCH", Path: "/api/v1/client-prefixes/" + cp.str("id"), Token: A.token, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"role": "excluded", "note": "pool viejo"}}), 200)
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/client-prefixes/" + cp.str("id"), Token: A.token, Header: map[string]string{"If-Match": `"1"`}}), 412)
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/client-prefixes/" + cp.str("id"), Token: A.token, Header: map[string]string{"If-Match": `"2"`}}), 204)
	a.must(a.post(A.token, "/api/v1/sites/"+siteID+"/client-prefixes", map[string]any{"prefix": "10.20.0.128/25", "role": "customers"}), 201)

	// Nodo con routers → 409 SITE_NOT_EMPTY; listado de routers con filtros.
	if r := a.do(req{Method: "DELETE", Path: "/api/v1/sites/" + siteID, Token: A.token, Header: map[string]string{"If-Match": `"1"`}}); r.code() != "SITE_NOT_EMPTY" {
		t.Fatalf("baja de nodo con routers: %s", r.Raw)
	}
	rl := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers?site_id=" + siteID + "&is_primary=true&include_total=true", Token: A.token}), 200)
	if rl.Body["page"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("routers filtrados = %s", rl.Raw)
	}
	a.must(a.do(req{Method: "GET", Path: "/api/v1/routers?sort=-name", Token: A.token}), 200)
	a.must(a.do(req{Method: "GET", Path: "/api/v1/routers?sort=status", Token: A.token}), 400)
	a.must(a.do(req{Method: "GET", Path: "/api/v1/routers?limit=500", Token: A.token}), 400)
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/routers/" + old.str("id"), Token: A.token, Header: map[string]string{"If-Match": `"2"`}}), 204)
	a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/" + old.str("id"), Token: A.token}), 404)
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/sites/" + site2, Token: A.token, Header: map[string]string{"If-Match": `"1"`}}), 204)
	a.must(a.do(req{Method: "GET", Path: "/api/v1/sites/" + site2, Token: A.token}), 404)
}
