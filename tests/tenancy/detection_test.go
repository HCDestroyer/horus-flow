//go:build integration

package tenancy

import (
	"context"
	"strings"
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
	// Feeds de plataforma (D20): el catálogo embebido, visible con security.findings.read.
	src := a.must(a.do(req{Method: "GET", Path: "/api/v1/reputation/sources", Token: viewer}), 200)
	feeds := src.Body["data"].([]any)
	if len(feeds) == 0 || feeds[0].(map[string]any)["origin"] != "catalog" || feeds[0].(map[string]any)["key"] == "" {
		t.Fatalf("reputation/sources: %s", src.Raw)
	}
	// Umbrales vigentes (settings.read, solo lectura): valores por defecto + ajustes del ISP,
	// que no se filtran a otro ISP.
	cfg := a.must(a.do(req{Method: "GET", Path: "/api/v1/detection/config", Token: viewer}), 200)
	scan, _ := cfg.Body["detectors"].(map[string]any)["scanning"].(map[string]any)
	if scan["min_destinations"] != float64(100) || scan["window"] != "5m0s" || len(cfg.Body["customized"].([]any)) != 0 {
		t.Fatalf("detection/config por defecto: %s", cfg.Raw)
	}
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO detection.detector_config (tenant_id, detector, enabled, params)
		VALUES ($1, 'scanning', true, '{"min_destinations": 250}')`, A.id); err != nil {
		t.Fatal(err)
	}
	cfg = a.must(a.do(req{Method: "GET", Path: "/api/v1/detection/config", Token: viewer}), 200)
	scan, _ = cfg.Body["detectors"].(map[string]any)["scanning"].(map[string]any)
	if scan["min_destinations"] != float64(250) || cfg.Body["customized"].([]any)[0] != "scanning" {
		t.Fatalf("detection/config con ajuste: %s", cfg.Raw)
	}
	other := a.must(a.do(req{Method: "GET", Path: "/api/v1/detection/config", Token: B.token}), 200)
	if s, _ := other.Body["detectors"].(map[string]any)["scanning"].(map[string]any); s["min_destinations"] != float64(100) {
		t.Fatalf("el ajuste de A se ve en B: %s", other.Raw)
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

const securityTemplate = "0192f000-0000-7000-8000-00000000d002"

func (a *app) widget(token, wid string) resp {
	return a.do(req{Method: "GET", Path: "/api/v1/dashboards/" + securityTemplate + "/widgets/" + wid + "/data", Token: token})
}

// Widgets de seguridad (C9) servidos por detection a analytics/dashboards y
// /security/summary con el JWT de kiosco: sin IP ni alias si el kiosco no
// tiene show_personal_data; otro ISP no ve los hallazgos de A.
func TestSecurityWidgetsAndKiosk(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")
	ids := a.fixtureISP(A, "10.10.0.0/24")

	sum := a.must(a.widget(A.token, "w-findings-summary"), 200)
	data := sum.Body["data"].(map[string]any)
	if data["kind"] != "state" || data["values"].(map[string]any)["open_total"].(float64) != 1 || sum.Body["meta"].(map[string]any)["widget_type"] != "findings_summary" {
		t.Fatalf("findings_summary: %s", sum.Raw)
	}
	sig := a.must(a.widget(A.token, "w-botnet-signals"), 200).Body["data"].(map[string]any)["values"].(map[string]any)
	if sig["by_signal"].(map[string]any)["scanning"].(float64) != 1 || sig["affected_customers"].(float64) != 1 {
		t.Fatalf("botnet_signals: %v", sig)
	}
	node := a.must(a.widget(A.token, "w-security-by-node"), 200).Body["data"].(map[string]any)["rows"].([]any)
	if len(node) != 1 || node[0].(map[string]any)["open_findings"].(float64) != 1 {
		t.Fatalf("security_by_node: %v", node)
	}
	trend := a.must(a.widget(A.token, "w-findings-trend"), 200).Body["data"].(map[string]any)["series"].([]any)
	if len(trend) != 1 || trend[0].(map[string]any)["group"] != "outbound_scanning" || len(trend[0].(map[string]any)["points"].([]any)) < 7 {
		t.Fatalf("findings_trend: %v", trend)
	}
	feed := a.must(a.widget(A.token, "w-findings-feed"), 200)
	rows := feed.Body["data"].(map[string]any)["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["customer_ip"] != "10.10.0.41" || rows[0].(map[string]any)["id"] != ids["finding_id"] ||
		feed.Body["meta"].(map[string]any)["masked_personal_data"] != false {
		t.Fatalf("findings_feed (customers.read): %s", feed.Raw)
	}
	// Sin ClickHouse en este binario: watched_ports responde 503, no 500.
	if r := a.widget(A.token, "w-watched-ports"); r.Status != 503 {
		t.Fatalf("watched_ports sin ClickHouse: %d %s", r.Status, r.Raw)
	}
	// Otro ISP: su dashboard de seguridad no ve nada de A.
	if r := a.must(a.widget(B.token, "w-findings-feed"), 200); len(r.Body["data"].(map[string]any)["rows"].([]any)) != 0 {
		t.Fatalf("B ve hallazgos de A: %s", r.Raw)
	}

	// Kiosco sin show_personal_data con el dashboard de seguridad en su playlist.
	pl := a.must(a.post(A.token, "/api/v1/playlists", map[string]any{"name": "Seguridad", "items": []any{
		map[string]any{"dashboard_id": securityTemplate, "duration_seconds": 30}}}), 201)
	_, _, jwt := a.enrolledKiosk(A, map[string]any{"name": "TV Seguridad", "playlist_id": pl.str("id")})
	kfeed := a.must(a.widget(jwt, "w-findings-feed"), 200)
	krow := kfeed.Body["data"].(map[string]any)["rows"].([]any)[0].(map[string]any)
	if krow["customer_ip"] != "10.10.0.•••" || krow["alias"] != nil || kfeed.Body["meta"].(map[string]any)["masked_personal_data"] != true {
		t.Fatalf("feed del kiosco sin datos personales: %s", kfeed.Raw)
	}
	ks := a.must(a.do(req{Method: "GET", Path: "/api/v1/security/summary", Token: jwt}), 200)
	if ks.Body["affected_customers"].(float64) != 1 || strings.Contains(string(ks.Raw), "10.10.0.41") {
		t.Fatalf("summary con JWT de kiosco: %s", ks.Raw)
	}
	// El kiosco sigue sin acceso a rutas que no admiten kioscos.
	if r := a.do(req{Method: "GET", Path: "/api/v1/findings", Token: jwt}); r.Status != 403 {
		t.Fatalf("kiosco en /findings: %d", r.Status)
	}
}

// insertFinding crea un hallazgo (como lo dejaría el motor) con kind, estado y
// objetivo dados; devuelve su ID.
func (a *app) insertFinding(tenant, site, router, kind, state, target string) string {
	a.t.Helper()
	id := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	var res any
	if state == "resolved" || state == "false_positive" {
		res = `{"verdict": "` + state + `", "comment": "previo", "actions_taken": [], "by": null, "at": "` + now.Format(time.RFC3339) + `"}`
	}
	_, err := a.admin.Exec(context.Background(), `INSERT INTO detection.finding (id, tenant_id, state, kind, severity, confidence, customer_id,
			realm_id, site_id, router_id, address, target_type, target_value, signals, summary, reasons, evidence, window_from, window_to,
			first_seen_at, last_seen_at, opened_at, updated_at, rule_version, resolution)
		VALUES ($1, $2, $3, $4, 'high', 0.9, $5, $6, $7, $8, '10.10.0.42', 'remote_ip', $9, '{volume}',
			'{"code": "ddos_participation", "text": "Volumen saliente anómalo"}', '[]', '{}', $10, $11, $10, $11, $11, $11, 'ddos@1', $12)`,
		id, tenant, state, kind, uuid.New(), uuid.New(), site, router, target, now.Add(-5*time.Minute), now, res)
	if err != nil {
		a.t.Fatal(err)
	}
	return id.String()
}

func (a *app) findingState(id string) string {
	a.t.Helper()
	var st string
	if err := a.admin.QueryRow(context.Background(), `SELECT state FROM detection.finding WHERE id = $1`, id).Scan(&st); err != nil {
		a.t.Fatal(err)
	}
	return st
}

// POST /findings/bulk-resolve: solo el kind pedido y solo open/acknowledged,
// dentro del alcance de nodos de quien llama y sin tocar otro ISP; comentario
// obligatorio; cada hallazgo deja veredicto y evento como el resolve individual.
func TestFindingsBulkResolve(t *testing.T) {
	t.Parallel()
	a := startApp(t)
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")
	idsA := a.fixtureISP(A, "10.10.0.0/24") // un outbound_scanning abierto en el nodo 1
	idsB := a.fixtureISP(B, "10.20.0.0/24")
	site1, router1 := idsA["site_id"], idsA["router_id"]
	site2 := a.must(a.post(A.token, "/api/v1/sites", map[string]any{"name": "Nodo 2"}), 201).str("id")
	router2 := uuid.NewString()

	open1 := a.insertFinding(A.id, site1, router1, "ddos_participation", "open", "198.51.100.1")
	ack1 := a.insertFinding(A.id, site1, router1, "ddos_participation", "acknowledged", "198.51.100.2")
	done1 := a.insertFinding(A.id, site1, router1, "ddos_participation", "resolved", "198.51.100.3")
	fp1 := a.insertFinding(A.id, site1, router1, "ddos_participation", "false_positive", "198.51.100.4")
	open2 := a.insertFinding(A.id, site2, router2, "ddos_participation", "open", "198.51.100.5")
	ack2 := a.insertFinding(A.id, site2, router2, "ddos_participation", "acknowledged", "198.51.100.6")
	otherB := a.insertFinding(B.id, idsB["site_id"], idsB["router_id"], "ddos_participation", "open", "198.51.100.7")

	bulk := func(token string, body any) resp {
		return a.do(req{Method: "POST", Path: "/api/v1/findings/bulk-resolve", Token: token, Body: body})
	}
	// Validación: comentario obligatorio, kind obligatorio, estados acotados.
	for _, body := range []map[string]any{
		{"kind": "ddos_participation"},
		{"kind": "ddos_participation", "comment": "  x "},
		{"comment": "detector mejorado"},
		{"kind": "DDoS!", "comment": "detector mejorado"},
		{"kind": "ddos_participation", "comment": "detector mejorado", "states": []string{"resolved"}},
		{"kind": "ddos_participation", "comment": "detector mejorado", "states": []string{}},
	} {
		if r := bulk(A.token, body); r.Status != 422 || r.code() != "VALIDATION_FAILED" {
			t.Errorf("cuerpo %v: %d %s, quiero 422", body, r.Status, r.Raw)
		}
	}
	if r := a.do(req{Method: "POST", Path: "/api/v1/findings/bulk-resolve", Token: A.token}); r.Status != 422 {
		t.Errorf("sin cuerpo: %d %s, quiero 422", r.Status, r.Raw)
	}
	// Un viewer no tiene security.findings.manage.
	viewer := a.userToken(A, "visor@isp-a.test", "viewer")
	if r := bulk(viewer, map[string]any{"kind": "ddos_participation", "comment": "detector mejorado"}); r.Status != 403 {
		t.Errorf("viewer: %d %s, quiero 403", r.Status, r.Raw)
	}

	// Operador con alcance solo en el nodo 2 (rol propio asignado al nodo).
	role := a.must(a.post(A.token, "/api/v1/roles", map[string]any{"key": "seg_nodo", "name": "Seguridad de nodo",
		"permissions": []string{"security.findings.read", "security.findings.manage"}}), 201).str("id")
	opID := a.createUser("seg@isp-a.test")
	var mID uuid.UUID
	ctx := context.Background()
	if err := a.admin.QueryRow(ctx, `INSERT INTO auth.tenant_membership (id, tenant_id, user_id, status) VALUES ($1, $2, $3, 'active') RETURNING id`,
		uuid.Must(uuid.NewV7()), A.id, opID).Scan(&mID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.admin.Exec(ctx, `INSERT INTO auth.role_assignment (id, tenant_id, membership_id, role_id, scope_type, scope_id)
		VALUES ($1, $2, $3, $4, 'site', $5)`, uuid.Must(uuid.NewV7()), A.id, mID, role, site2); err != nil {
		t.Fatal(err)
	}
	op := a.login("seg@isp-a.test", userPassword).tenantToken(A.id)
	r := a.must(bulk(op, map[string]any{"kind": "ddos_participation", "comment": "Detector mejorado", "states": []string{"open"}}), 200)
	if r.Body["resolved"] != float64(1) || a.findingState(open2) != "resolved" || a.findingState(ack2) != "acknowledged" ||
		a.findingState(open1) != "open" {
		t.Fatalf("operador de nodo 2 (solo open): %s", r.Raw)
	}

	// El administrador de A: el resto de ddos activos de A; nada de B ni de otros kinds.
	r = a.must(bulk(A.token, map[string]any{"kind": "ddos_participation", "comment": "Detector mejorado: cierre de los hallazgos anteriores"}), 200)
	if r.Body["resolved"] != float64(3) { // open1, ack1 y ack2
		t.Fatalf("admin A: %s, quiero resolved=3", r.Raw)
	}
	want := map[string]string{open1: "resolved", ack1: "resolved", ack2: "resolved", done1: "resolved", fp1: "false_positive",
		idsA["finding_id"]: "open", otherB: "open"}
	for id, st := range want {
		if got := a.findingState(id); got != st {
			t.Errorf("hallazgo %s: estado %s, quiero %s", id, got, st)
		}
	}
	// Mismo rastro que el resolve individual: veredicto con comentario y evento resolved.
	var verdicts, events int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM detection.verdict_feedback WHERE tenant_id = $1 AND verdict = 'resolved'`, A.id).Scan(&verdicts); err != nil {
		t.Fatal(err)
	}
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM detection.outbox WHERE payload->>'type' = 'horus.detection.finding.resolved'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if verdicts != 4 || events != 4 {
		t.Errorf("veredictos=%d eventos=%d, quiero 4 y 4", verdicts, events)
	}
	// refreshSecurity por cliente afectado: los cuatro quedan "mitigated".
	var mitigated int
	if err := a.admin.QueryRow(ctx, `SELECT count(*) FROM detection.customer_security WHERE tenant_id = $1 AND state = 'mitigated'`, A.id).Scan(&mitigated); err != nil {
		t.Fatal(err)
	}
	if mitigated != 4 {
		t.Errorf("clientes mitigated=%d, quiero 4", mitigated)
	}
	got := a.must(a.do(req{Method: "GET", Path: "/api/v1/findings/" + open1, Token: A.token}), 200)
	if res, _ := got.Body["resolution"].(map[string]any); res == nil || res["verdict"] != "resolved" ||
		res["comment"] != "Detector mejorado: cierre de los hallazgos anteriores" || got.Header.Get("ETag") != `"2"` {
		t.Fatalf("hallazgo resuelto en bloque: %s", got.Raw)
	}
	// Nada más que resolver: 0, y B no se ha tocado.
	if r := a.must(bulk(A.token, map[string]any{"kind": "ddos_participation", "comment": "otra vez"}), 200); r.Body["resolved"] != float64(0) {
		t.Errorf("segunda llamada: %s", r.Raw)
	}
	if r := a.must(bulk(B.token, map[string]any{"kind": "ddos_participation", "comment": "cierre B"}), 200); r.Body["resolved"] != float64(1) {
		t.Errorf("B: %s, quiero resolved=1 (solo el suyo)", r.Raw)
	}
}
