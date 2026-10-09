//go:build integration

package tenancy

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb/pgtest"
	"github.com/hcdestroyer/horus-flow/packages/go/tenanttest"
	"github.com/hcdestroyer/horus-flow/packages/go/testkit"
	wgmig "github.com/hcdestroyer/horus-flow/services/wireguard/migrations"
)

// Esquema wireguard: tablas con tenant_id con RLS forzada (salvo la outbox),
// fail-closed sin tenant y migración reversible.
func TestWireguardSchemaRLS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := pgdb.Open(ctx, pgdb.Config{DSN: pgtest.New(t), AppRole: "wireguard_app", PlatformRole: "wireguard_platform"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := pgdb.Migrate(ctx, db, wgmig.Schema, wgmig.Postgres(), nil); err != nil {
		t.Fatal(err)
	}
	exempt := []string{"wireguard.outbox"}
	if tables := tenanttest.AssertRLS(t, db.Pool, []string{"wireguard"}, exempt, exempt); len(tables) != 4 {
		t.Fatalf("tablas con tenant_id = %v", tables)
	}
	mg, err := pgdb.NewMigrator(ctx, db, wgmig.Schema, wgmig.Postgres(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mg.Close() }()
	if _, err := mg.DownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := pgdb.Migrate(ctx, db, wgmig.Schema, wgmig.Postgres(), nil); err != nil {
		t.Fatalf("reaplicar: %v", err)
	}
	var n int
	if err := db.AppTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM wireguard.peer`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("fail-closed: %d, %v", n, err)
	}
}

func wgKey(n byte) string {
	b := make([]byte, 32)
	b[0], b[31] = n, 0x40
	return base64.StdEncoding.EncodeToString(b)
}

// I1-01 y I1-02 por la API completa (gateway + auth + devices + wireguard +
// wg-agent en memoria): script, enrolamiento público, peer en el hub en
// < 10 s, router.updated con la IP de túnel, permisos y rate limit.
func TestWireguardOnboardingE2E(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A := a.newISP(pt, "isp-a")
	ctx := context.Background()

	site := a.must(a.post(A.token, "/api/v1/sites", map[string]any{"name": "Nodo"}), 201).str("id")
	router := a.must(a.post(A.token, "/api/v1/routers", map[string]any{"site_id": site, "name": "rt-1", "routeros_version": "7.16"}), 201).str("id")
	path := "/api/v1/routers/" + router + "/provisioning-script"

	// Sin Idempotency-Key → 428; isp_viewer → 403; RouterOS < 7.12 → 422.
	a.must(a.do(req{Method: "POST", Path: path, Token: A.token}), 428)
	viewer := a.createUser("viewer@isp-a.test")
	a.grant(viewer, A.id, "viewer")
	vt := a.login("viewer@isp-a.test", userPassword).tenantToken(A.id)
	idem := func() map[string]string { return map[string]string{"Idempotency-Key": uuid.NewString()} }
	if r := a.do(req{Method: "POST", Path: path, Token: vt, Header: idem()}); r.Status != 403 {
		t.Fatalf("viewer: %d %s", r.Status, r.Raw)
	}
	if r := a.do(req{Method: "POST", Path: path, Token: A.token, Header: idem(), Body: map[string]any{"routeros_version": "7.11"}}); r.Status != 422 ||
		r.code() != "ROUTEROS_VERSION_UNSUPPORTED" {
		t.Fatalf("7.11: %d %s", r.Status, r.Raw)
	}

	r := a.must(a.do(req{Method: "POST", Path: path, Token: A.token, Header: idem()}), 201)
	script := string(r.Raw)
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") || r.Header.Get("Cache-Control") != "no-store" ||
		r.Header.Get("X-Horus-Enrollment-Token-Id") == "" || r.Header.Get("X-Horus-Enrollment-Token-Expires-At") == "" {
		t.Fatalf("cabeceras = %v", r.Header)
	}
	if strings.Contains(script, "<") || !strings.Contains(script, "check-certificate=yes") {
		t.Fatal("script con placeholders o sin verificación TLS")
	}
	i := strings.Index(script, `\"token\":\"`) + len(`\"token\":\"`)
	token := script[i : i+strings.Index(script[i:], `\"`)]
	if a.auditCount("wireguard.provisioning_script.created") != 1 {
		t.Fatal("script no auditado")
	}

	// Enrolamiento público.
	enroll := func(tok, key string) resp {
		return a.do(req{Method: "POST", Path: "/api/v1/enroll/wireguard", Body: map[string]string{"token": tok, "public_key": key}})
	}
	if e := enroll(strings.Repeat("z", 43), wgKey(1)); e.Status != 422 || e.code() != "ENROLLMENT_TOKEN_INVALID" {
		t.Fatalf("token desconocido: %d %s", e.Status, e.Raw)
	}
	e := a.must(enroll(token, wgKey(1)), 202)
	if e.str("peer_status") != "pending_handshake" {
		t.Fatalf("enroll = %s", e.Raw)
	}
	if again := enroll(token, wgKey(1)); again.code() != "ENROLLMENT_TOKEN_INVALID" {
		t.Fatalf("token reutilizado: %s", again.Raw)
	}
	if a.auditCount("wireguard.peer.enrolled") != 1 {
		t.Fatal("enrolamiento no auditado")
	}
	// El agente (en proceso) aplica el estado deseado en < 10 s.
	testkit.Eventually(t, 10*time.Second, func() bool {
		var applied, desired int64
		_ = a.admin.QueryRow(ctx, `SELECT applied_version, desired_version FROM wireguard.server`).Scan(&applied, &desired)
		return desired >= 2 && applied == desired
	}, "peer aplicado en el hub")

	peers := a.must(a.do(req{Method: "GET", Path: "/api/v1/wireguard/peers?router_id=" + router, Token: A.token}), 200)
	p := peers.Body["data"].([]any)[0].(map[string]any)
	if p["public_key"] != wgKey(1) || p["status"] != "pending_handshake" || p["persistent_keepalive_seconds"].(float64) != 25 ||
		p["enrollment"].(map[string]any)["token_state"] != "used" {
		t.Fatalf("peer = %v", p)
	}
	rt := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/" + router, Token: A.token}), 200)
	if rt.str("onboarding_state") != "key_received" || rt.str("tunnel_address") == "" || rt.str("wireguard_peer_id") != p["id"] {
		t.Fatalf("router = %s", rt.Raw)
	}
	if !strings.HasPrefix(p["address"].(string), rt.str("tunnel_address")+"/32") {
		t.Fatalf("dirección del peer %v ≠ router %s", p["address"], rt.str("tunnel_address"))
	}
	// flows recibe la identidad del exportador en router.updated (tunnel_address /32).
	var withTunnel int
	_ = a.admin.QueryRow(ctx, `SELECT count(*) FROM devices.outbox WHERE subject = 'horus.devices.router.updated.' || $1
		AND payload->'data'->>'tunnel_address' = $2`, router, rt.str("tunnel_address")+"/32").Scan(&withTunnel)
	if withTunnel == 0 {
		t.Fatal("router.updated sin tunnel_address /32")
	}

	// Script inverso.
	inv := a.must(a.do(req{Method: "POST", Path: "/api/v1/routers/" + router + "/deprovisioning-script", Token: A.token}), 200)
	if !strings.Contains(string(inv.Raw), `comment="horus"`) || !strings.Contains(string(inv.Raw), "port=4739") {
		t.Fatalf("script inverso = %s", inv.Raw)
	}

	// Hubs (plataforma) con ocupación.
	hubs := a.must(a.do(req{Method: "GET", Path: "/api/v1/platform/wireguard/hubs", Token: pt}), 200)
	h := hubs.Body["data"].([]any)[0].(map[string]any)
	if h["addresses_used"].(float64) != 1 || h["tunnel_cidr"] != "10.255.0.0/16" || h["public_key"] == "" {
		t.Fatalf("hub = %v", h)
	}

	// Rate limit del endpoint público: 10/min por IP (ya van 3).
	var last resp
	for range 8 {
		last = enroll(strings.Repeat("q", 43), wgKey(2))
	}
	if last.Status != 429 {
		t.Fatalf("rate limit: %d %s", last.Status, last.Raw)
	}
}
