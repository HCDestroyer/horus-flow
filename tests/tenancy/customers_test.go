//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx/natstest"
)

// startAppNATS arranca la app con NATS real (durables con prefijo propio).
func startAppNATS(t *testing.T, extra ...string) (*app, jetstream.JetStream) {
	t.Helper()
	_, js := natstest.New(t)
	env := append([]string{"HORUS_NATS_URL=" + natstest.URL(t), "HORUS_NATS_DURABLE_PREFIX=t" + uuid.NewString()[:8] + "-"}, extra...)
	return startAppEnv(t, env...), js
}

// userToken crea un usuario con un rol de sistema en el ISP y devuelve su token.
func (a *app) userToken(x isp, email, role string) string {
	a.t.Helper()
	id := a.createUser(email)
	a.grant(id, x.id, role)
	return a.login(email, userPassword).tenantToken(x.id)
}

// insertCustomer crea un cliente directamente en la base (fixtures de aislamiento).
func (a *app) insertCustomer(tenant, site, realm, prefix, addr string) string {
	a.t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO devices.customer (id, tenant_id, realm_id, address, site_id, client_prefix_id,
		first_seen, last_seen) VALUES ($1, $2, $3, $4::inet, $5, $6, now(), now())`, id, tenant, realm, addr, site, prefix); err != nil {
		a.t.Fatal(err)
	}
	return id.String()
}

func (a *app) realmOf(token, site string) string {
	a.t.Helper()
	return a.must(a.do(req{Method: "GET", Path: "/api/v1/sites/" + site, Token: token}), 200).str("private_realm_id")
}

func publishFirstSeen(t *testing.T, js jetstream.JetStream, tenant, realm, prefix string, addrs ...string) {
	t.Helper()
	tid := uuid.MustParse(tenant)
	var clients []map[string]any
	for _, a := range addrs {
		clients = append(clients, map[string]any{"address": a, "client_prefix_id": prefix, "first_seen": time.Now().UTC().Format(time.RFC3339)})
	}
	data, _ := json.Marshal(map[string]any{"batch_id": uuid.NewString(), "realm_id": realm, "router_id": uuid.NewString(),
		"window_from": time.Now().UTC().Format(time.RFC3339), "window_to": time.Now().UTC().Format(time.RFC3339), "clients": clients})
	env := natsx.Envelope{Type: "horus.flows.client.first_seen", Source: "horus/flows", Subject: realm, TenantID: &tid,
		AggregateType: "realm", AggregateVersion: 1, Data: data}
	if err := natsx.Publish(context.Background(), js, &env); err != nil {
		t.Fatal(err)
	}
}

func publishActivity(t *testing.T, js jetstream.JetStream, tenant, realm string, addrs ...string) {
	t.Helper()
	var clients []map[string]any
	for _, a := range addrs {
		clients = append(clients, map[string]any{"address": a, "last_seen": time.Now().UTC().Format(time.RFC3339)})
	}
	body, _ := json.Marshal(map[string]any{"batch_id": uuid.NewString(), "realm_id": realm, "clients": clients})
	m := natsTelemetry("horus.telemetry.flows.client_activity."+realm, tenant, body)
	if _, err := js.PublishMsg(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

func (a *app) waitFor(what string, cond func() bool) {
	a.t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	a.t.Fatalf("timeout esperando %s (outbox discovered=%d)", what, a.outboxCount("horus.devices.customer.discovered"))
}

func (a *app) outboxCount(typ string) int {
	a.t.Helper()
	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM devices.outbox WHERE payload->>'type' = $1`, typ).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}

// I1-06: descubrimiento idempotente por first_seen, tipo manual con motivo e
// If-Match, historial, reset, filtros y stats, permisos y aislamiento.
func TestCustomersLifecycle(t *testing.T) {
	t.Parallel()
	a, js := startAppNATS(t, "HORUS_DEVICES_CUSTOMER_LIFECYCLE_EVERY=300ms")
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")
	fa := a.fixtureISP(A, "10.20.0.0/24")
	fb := a.fixtureISP(B, "10.20.0.0/24")
	realmA, realmB := a.realmOf(A.token, fa["site_id"]), a.realmOf(B.token, fb["site_id"])
	if _, err := a.admin.Exec(context.Background(), `DELETE FROM devices.customer`); err != nil { // clientes de la fixture
		t.Fatal(err)
	}

	// Criterio 1: lote first_seen (con duplicado y una IP fuera de prefijo).
	publishFirstSeen(t, js, A.id, realmA, fa["client_prefix_id"], "10.20.0.41", "10.20.0.42", "192.0.2.9")
	publishFirstSeen(t, js, A.id, realmA, fa["client_prefix_id"], "10.20.0.41")
	publishFirstSeen(t, js, B.id, realmB, fb["client_prefix_id"], "10.20.0.41")
	var list resp
	a.waitFor("clientes de A", func() bool {
		list = a.do(req{Method: "GET", Path: "/api/v1/customers?sort=first_seen", Token: A.token})
		return list.Status == 200 && len(list.Body["data"].([]any)) == 2
	})
	time.Sleep(500 * time.Millisecond) // el duplicado ya se procesó
	if n := len(a.must(a.do(req{Method: "GET", Path: "/api/v1/customers", Token: A.token}), 200).Body["data"].([]any)); n != 2 {
		t.Fatalf("el lote duplicado creó clientes: %d", n)
	}
	first := list.Body["data"].([]any)[0].(map[string]any)
	if first["kind"] != "residential" || first["kind_source"] != "default" || first["tenant_id"] != A.id {
		t.Fatalf("cliente = %v", first)
	}
	if n := a.outboxCount("horus.devices.customer.discovered"); n < 3 {
		t.Fatalf("customer.discovered = %d", n)
	}
	cid := ""
	for _, c := range list.Body["data"].([]any) {
		if c.(map[string]any)["address"] == "10.20.0.41" {
			cid = c.(map[string]any)["id"].(string)
		}
	}
	// Lookup por IP en el cuerpo.
	lk := a.must(a.post(A.token, "/api/v1/customers/lookup", map[string]any{"address": "10.20.0.41"}), 200)
	if d := lk.Body["data"].([]any); len(d) != 1 || d[0].(map[string]any)["id"] != cid {
		t.Fatalf("lookup = %s", lk.Raw)
	}
	var bCustomer string
	a.waitFor("cliente de B", func() bool {
		r := a.do(req{Method: "GET", Path: "/api/v1/customers", Token: B.token})
		if d, _ := r.Body["data"].([]any); len(d) == 1 {
			bCustomer = d[0].(map[string]any)["id"].(string)
			return true
		}
		return false
	})

	// Criterio 2: set-kind por un operador (analyst).
	op := a.userToken(A, "op@isp-a.test", "analyst")
	path := "/api/v1/customers/" + cid
	if r := a.do(req{Method: "POST", Path: path + "/set-kind", Token: op, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"kind": "commercial"}}); r.Status != 422 {
		t.Fatalf("sin motivo: %d %s", r.Status, r.Raw)
	}
	sk := a.must(a.do(req{Method: "POST", Path: path + "/set-kind", Token: op, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"kind": "commercial", "reason": "Contrato empresarial verificado"}}), 200)
	if sk.Body["kind_source"] != "manual" || sk.Body["kind_locked"] != true || sk.Header.Get("ETag") != `"2"` {
		t.Fatalf("set-kind = %s", sk.Raw)
	}
	stale := a.do(req{Method: "POST", Path: path + "/set-kind", Token: op, Header: map[string]string{"If-Match": `"1"`},
		Body: map[string]any{"kind": "residential", "reason": "otra vez"}})
	if stale.Status != 412 || stale.Body["current"].(map[string]any)["kind"] != "commercial" {
		t.Fatalf("If-Match desfasado: %d %s", stale.Status, stale.Raw)
	}
	if a.outboxCount("horus.devices.customer.kind_changed") != 1 {
		t.Fatal("sin customer.kind_changed")
	}
	// Alias y notas.
	a.must(a.do(req{Method: "PATCH", Path: path, Token: op, Header: map[string]string{"If-Match": `"2"`},
		Body: map[string]any{"alias": "Ferretería López", "notes": "cliente VIP"}}), 200)
	byAlias := a.must(a.do(req{Method: "GET", Path: "/api/v1/customers?q=ferreter", Token: A.token}), 200)
	if len(byAlias.Body["data"].([]any)) != 1 {
		t.Fatalf("q por alias = %s", byAlias.Raw)
	}

	// Criterio 6: viewer → 403; cliente de otro ISP → 404.
	viewer := a.userToken(A, "ver@isp-a.test", "viewer")
	if r := a.do(req{Method: "POST", Path: path + "/set-kind", Token: viewer, Header: map[string]string{"If-Match": `"3"`},
		Body: map[string]any{"kind": "unknown", "reason": "no deberia"}}); r.Status != 403 {
		t.Fatalf("viewer set-kind: %d", r.Status)
	}
	if r := a.do(req{Method: "GET", Path: "/api/v1/customers/" + bCustomer, Token: A.token}); r.Status != 404 || r.code() != "CUSTOMER_NOT_FOUND" {
		t.Fatalf("cliente de B: %d %s", r.Status, r.Raw)
	}

	// Criterio 7: filtros y stats cuadran.
	com := a.must(a.do(req{Method: "GET", Path: "/api/v1/customers?kind=commercial&kind_source=manual&status=active", Token: A.token}), 200)
	if len(com.Body["data"].([]any)) != 1 {
		t.Fatalf("filtro = %s", com.Raw)
	}
	page1 := a.must(a.do(req{Method: "GET", Path: "/api/v1/customers?limit=1", Token: A.token}), 200)
	next := page1.Body["page"].(map[string]any)["next_cursor"].(string)
	page2 := a.must(a.do(req{Method: "GET", Path: "/api/v1/customers?limit=1&cursor=" + next, Token: A.token}), 200)
	if page2.Body["data"].([]any)[0].(map[string]any)["id"] == page1.Body["data"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("el cursor repite el cliente")
	}
	st := a.must(a.do(req{Method: "GET", Path: "/api/v1/customers/stats", Token: A.token}), 200)
	if st.Body["total"].(float64) != 2 || st.Body["by_kind"].(map[string]any)["commercial"].(float64) != 1 || st.Body["new_today"].(float64) != 2 {
		t.Fatalf("stats = %s", st.Raw)
	}

	// Criterio 5: reset (🔒 re-autenticación reciente: el token es nuevo).
	rs := a.must(a.do(req{Method: "POST", Path: path + "/reset", Token: A.token, Header: map[string]string{"If-Match": `"3"`},
		Body: map[string]any{"reason": "La IP pasó a otro abonado"}}), 200)
	if rs.Body["kind"] != "residential" || rs.Body["alias"] != nil || rs.Body["notes"] != nil || rs.Body["reset_at"] == nil {
		t.Fatalf("reset = %s", rs.Raw)
	}
	hist := a.must(a.do(req{Method: "GET", Path: path + "/kind-history", Token: A.token}), 200)
	h := hist.Body["data"].([]any)
	if len(h) != 2 || h[0].(map[string]any)["source"] != "reset" || h[1].(map[string]any)["manual_reason"] != "Contrato empresarial verificado" {
		t.Fatalf("historial = %s", hist.Raw)
	}

	// Criterio 3: inactividad (reloj simulado moviendo last_seen) y reactivación.
	if _, err := a.admin.Exec(context.Background(), `UPDATE devices.customer SET last_seen = now() - interval '31 days' WHERE id = $1`, cid); err != nil {
		t.Fatal(err)
	}
	a.waitFor("inactive", func() bool {
		return a.do(req{Method: "GET", Path: path, Token: A.token}).str("status") == "inactive"
	})
	publishActivity(t, js, A.id, realmA, "10.20.0.41")
	a.waitFor("reactivado", func() bool {
		return a.do(req{Method: "GET", Path: path, Token: A.token}).str("status") == "active"
	})
	if a.outboxCount("horus.devices.customer.inactivated") != 1 || a.outboxCount("horus.devices.customer.reactivated") != 1 {
		t.Fatal("eventos de ciclo de vida")
	}

	// Criterio 4: purga a los 25 meses; si reaparece es un cliente nuevo.
	if _, err := a.admin.Exec(context.Background(), `UPDATE devices.customer SET last_seen = now() - interval '26 months' WHERE id = $1`, cid); err != nil {
		t.Fatal(err)
	}
	a.waitFor("purga", func() bool { return a.do(req{Method: "GET", Path: path, Token: A.token}).Status == 404 })
	var hrows int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM devices.customer_kind_change WHERE customer_id = $1`, cid).Scan(&hrows)
	if hrows != 0 || a.outboxCount("horus.devices.customer.purged") != 1 {
		t.Fatalf("purga: historial %d", hrows)
	}
	publishFirstSeen(t, js, A.id, realmA, fa["client_prefix_id"], "10.20.0.41")
	a.waitFor("redescubierto", func() bool {
		r := a.post(A.token, "/api/v1/customers/lookup", map[string]any{"address": "10.20.0.41"})
		d, _ := r.Body["data"].([]any)
		return len(d) == 1 && d[0].(map[string]any)["id"] != cid
	})
	if a.auditCount("customers.read") == 0 {
		t.Fatal("acceso al detalle no auditado")
	}
}

// natsTelemetry construye un mensaje de telemetría JSON (sobre en cabeceras).
func natsTelemetry(subject, tenant string, body []byte) *nats.Msg {
	m := nats.NewMsg(subject)
	m.Data = body
	m.Header.Set(natsx.HeaderMsgID, uuid.NewString())
	m.Header.Set(natsx.HeaderTenant, tenant)
	m.Header.Set(natsx.HeaderContentType, "application/json")
	m.Header.Set(natsx.HeaderType, "horus.flows.client.activity_summary")
	return m
}
