//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeArgosPeer es una ArgosISP de prueba para el enlace de conexión (§1.4)
// y el autologin (§1.5): canjea un código y emite tickets.
type fakeArgosPeer struct {
	mu       sync.Mutex
	srv      *httptest.Server
	code     string
	status   int // respuesta al canje (0 = 200)
	received map[string]any
	tickets  []map[string]any
}

func (f *fakeArgosPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch r.URL.Path {
	case "/api/v1/integraciones/horus/conexion/canjear":
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		if body["codigo"] != f.code || body["sistema"] != "horus" {
			w.WriteHeader(http.StatusGone)
			return
		}
		f.received = body
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "atk_prueba_1234",
			"webhook":     map[string]any{"url": f.srv.URL + "/api/v1/integraciones/horus/webhook", "secreto": "secreto-de-prueba-0003"},
			"conector_id": 7})
	case "/api/v1/integraciones/horus/sso/tickets":
		if r.Header.Get("Authorization") != "Bearer atk_prueba_1234" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.tickets = append(f.tickets, body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"url": "https://panel.argos.test/sso/entrar#" + strings.Repeat("t", 43),
			"expira_en": time.Now().Add(time.Minute).UTC().Format(time.RFC3339)})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// redeemSSO canjea un ticket como lo hace la página /sso/entrar (CSRF: cabecera y Origin).
func (a *app) redeemSSO(ticket string, headers bool) resp {
	a.t.Helper()
	h := map[string]string{}
	if headers {
		h["X-Requested-With"], h["Origin"] = "horus", "https://horus.test"
	}
	return a.do(req{Method: "POST", Path: "/api/v1/integrations/argosisp/sso/redeem", Body: map[string]string{"ticket": ticket}, Header: h})
}

func fragment(t *testing.T, u, prefix string) string {
	t.Helper()
	v, ok := strings.CutPrefix(u, prefix)
	if !ok || v == "" {
		t.Fatalf("url = %q", u)
	}
	return v
}

// H10 (Horus = A) y H11 de extremo a extremo: Argos canjea un enlace de
// Horus (un uso, idempotente, conector apagado, canal webhook apagado),
// emite tickets con el hf_pat_ recibido y la primera vez el usuario vincula
// su cuenta iniciando sesión. Nunca se vincula por correo ni entre ISP.
func TestArgosConnectionLinkAndSSO(t *testing.T) {
	t.Parallel()
	a := startAppEnv(t, "HORUS_INTEGRATIONS_ALLOW_PRIVATE=true", "HORUS_ALERTS_WEBHOOK_ALLOW_PRIVATE=true",
		"HORUS_PUBLIC_BASE_URL=https://horus.test", "HORUS_INTEGRATIONS_SWEEP_EVERY=1h")
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "enlace-a"), a.newISP(pt, "enlace-b")
	params := a.fixtureISP(A, "10.10.0.0/24")
	peer := &fakeArgosPeer{}
	peer.srv = httptest.NewServer(peer)
	defer peer.srv.Close()

	// Generar: un uso, 15 min, mostrado una vez; solo el hash en la base.
	link := a.must(a.post(A.token, "/api/v1/integrations/argosisp/connection-links", nil), 201)
	code := fragment(t, link.str("url"), "https://horus.test/conectar#")
	if link.Body["ttl_seconds"] != float64(900) || link.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("enlace = %s", link.Raw)
	}
	var stored int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM devices.argosisp_connection_link WHERE position($1::bytea in code_hash) > 0`,
		[]byte(code)).Scan(&stored)
	if stored != 0 {
		t.Fatal("el código no se guarda en claro")
	}
	// Un miembro sin api_tokens.manage no genera enlaces.
	analyst := a.createUser("analista@enlace-a.test")
	a.grant(analyst, A.id, "analyst")
	an := a.login("analista@enlace-a.test", userPassword)
	if r := a.post(an.tenantToken(A.id), "/api/v1/integrations/argosisp/connection-links", nil); r.Status != 403 {
		t.Fatalf("analista genera enlace: %d %s", r.Status, r.Raw)
	}

	// Canje público por Argos.
	body := map[string]any{"codigo": code, "sistema": "argosisp", "base_url": peer.srv.URL, "token": "atk_prueba_1234",
		"webhook": map[string]any{"url": peer.srv.URL + "/api/v1/integraciones/horus/webhook", "secreto": "secreto-de-prueba-0004"}}
	red := a.must(a.do(req{Method: "POST", Path: "/api/v1/integrations/argosisp/conexion/canjear", Body: body}), 200)
	hfpat := red.str("token")
	if !strings.HasPrefix(hfpat, "hf_pat_") || red.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("canje = %s", red.Raw)
	}
	again := a.must(a.do(req{Method: "POST", Path: "/api/v1/integrations/argosisp/conexion/canjear", Body: body}), 200)
	if string(again.Raw) != string(red.Raw) || again.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatal("la misma petición repetida devuelve la misma respuesta")
	}
	body["token"] = "atk_de_un_tercero_9"
	if r := a.do(req{Method: "POST", Path: "/api/v1/integrations/argosisp/conexion/canjear", Body: body}); r.Status != 410 ||
		r.code() != "CONNECTION_LINK_INVALID" {
		t.Fatalf("replay con otra petición: %d %s", r.Status, r.Raw)
	}
	conn := a.must(a.do(req{Method: "GET", Path: "/api/v1/integrations/argosisp", Token: A.token}), 200)
	cx, _ := conn.Body["connection"].(map[string]any)
	if conn.Body["enabled"] != false || conn.Body["base_url"] != peer.srv.URL+"/api/v1" || conn.Body["has_token"] != true ||
		cx["method"] != "link_generated" || strings.Contains(string(conn.Raw), "atk_prueba") {
		t.Fatalf("conector = %s", conn.Raw)
	}
	ch := a.must(a.do(req{Method: "GET", Path: "/api/v1/notification-channels/" + cx["notification_channel_id"].(string), Token: A.token}), 200)
	if ch.Body["kind"] != "webhook" || ch.Body["enabled"] != false {
		t.Fatalf("canal webhook = %s", ch.Raw)
	}
	if a.auditCount("integrations.argosisp.connection_link.created") != 1 || a.auditCount("integrations.argosisp.connection_link.redeemed") != 1 {
		t.Fatal("generación y canje auditados")
	}
	// El hf_pat_ recibido lee de Horus (plantilla ArgosISP) …
	a.must(a.post(hfpat, "/api/v1/integrations/security-states", map[string]any{"items": []any{map[string]any{"address": "10.10.0.41"}}}), 200)
	// … pero no emite tickets con el conector apagado.
	ticketBody := map[string]any{"usuario": map[string]any{"id_externo": "u-17", "correo": "admin@enlace-a.test", "nombre": "Ana"},
		"destino": map[string]any{"tipo": "router", "id_externo": params["router_id"]}}
	if r := a.post(hfpat, "/api/v1/integrations/argosisp/sso/tickets", ticketBody); r.Status != 409 {
		t.Fatalf("ticket con el conector apagado: %d %s", r.Status, r.Raw)
	}
	a.must(a.do(req{Method: "PUT", Path: "/api/v1/integrations/argosisp", Token: A.token,
		Body: map[string]any{"base_url": peer.srv.URL + "/api/v1", "enabled": true}}), 200)
	// Un token de persona no emite tickets; otro hf_pat_ del ISP tampoco (solo el del canje).
	if r := a.post(A.token, "/api/v1/integrations/argosisp/sso/tickets", ticketBody); r.Status != 403 {
		t.Fatalf("ticket con token de persona: %d %s", r.Status, r.Raw)
	}
	other := a.must(a.post(A.token, "/api/v1/api-tokens", map[string]any{"name": "Otro", "template": "argosisp"}), 201).str("token")
	if r := a.post(other, "/api/v1/integrations/argosisp/sso/tickets", ticketBody); r.Status != 403 {
		t.Fatalf("ticket con otro hf_pat_: %d %s", r.Status, r.Raw)
	}
	// Un hf_pat_ de B emite, si acaso, para B (que no tiene conector).
	bTok := a.must(a.post(B.token, "/api/v1/api-tokens", map[string]any{"name": "Argos B", "template": "argosisp"}), 201).str("token")
	if r := a.post(bTok, "/api/v1/integrations/argosisp/sso/tickets", ticketBody); r.Status != 409 {
		t.Fatalf("ticket de B: %d %s", r.Status, r.Raw)
	}
	issue := func() string {
		t.Helper()
		r := a.must(a.post(hfpat, "/api/v1/integrations/argosisp/sso/tickets", ticketBody), 201)
		return fragment(t, r.str("url"), "https://horus.test/sso/entrar#")
	}

	// Canje sin la defensa CSRF → 403; primer uso → link_required (sin sesión, sin usuarios nuevos).
	tk := issue()
	if r := a.redeemSSO(tk, false); r.Status != 403 {
		t.Fatalf("canje sin X-Requested-With: %d %s", r.Status, r.Raw)
	}
	var users int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM auth."user"`).Scan(&users)
	first := a.must(a.redeemSSO(tk, true), 200)
	if first.str("status") != "link_required" || refreshCookie(first) != "" || first.str("link_token") == "" {
		t.Fatalf("primer uso = %s", first.Raw)
	}
	if r := a.redeemSSO(tk, true); r.Status != 410 {
		t.Fatalf("ticket reutilizado: %d %s", r.Status, r.Raw)
	}
	var usersAfter int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM auth."user"`).Scan(&usersAfter)
	if usersAfter != users {
		t.Fatal("el autologin nunca crea usuarios")
	}
	// Un usuario de B (otro ISP; su correo no importa) no puede vincular la cuenta de A.
	a.grant(a.createUser("analista@enlace-b.test"), B.id, "analyst")
	time.Sleep(1100 * time.Millisecond) // login en un segundo posterior al canje
	bFresh := a.login("analista@enlace-b.test", userPassword)
	if r := a.do(req{Method: "POST", Path: "/api/v1/me/sso-links", Token: bFresh.token,
		Body: map[string]string{"link_token": first.str("link_token")}}); r.Status != 403 || r.code() != "SSO_NOT_A_MEMBER" {
		t.Fatalf("vincular desde otro ISP: %d %s", r.Status, r.Raw)
	}
	// Una sesión abierta antes del canje no basta: pide re-autenticarse.
	if r := a.do(req{Method: "POST", Path: "/api/v1/me/sso-links", Token: an.token,
		Body: map[string]string{"link_token": first.str("link_token")}}); r.Status != 403 || r.code() != "REAUTH_REQUIRED" {
		t.Fatalf("vincular con una sesión anterior: %d %s", r.Status, r.Raw)
	}
	// Tras iniciar sesión (analista, sin 2FA), vincula.
	tk2 := issue()
	second := a.must(a.redeemSSO(tk2, true), 200)
	time.Sleep(1100 * time.Millisecond) // auth_time tiene resolución de segundos: login en un segundo posterior
	fresh := a.login("analista@enlace-a.test", userPassword)
	linked := a.must(a.do(req{Method: "POST", Path: "/api/v1/me/sso-links", Token: fresh.token,
		Body: map[string]string{"link_token": second.str("link_token")}}), 201)
	if tg, _ := linked.Body["target"].(map[string]any); tg["tenant_slug"] != "enlace-a" || tg["type"] != "router" {
		t.Fatalf("vinculación = %s", linked.Raw)
	}
	// Con vinculación: sesión normal del analista (cookie de refresh) y destino con su ACL.
	in := a.must(a.redeemSSO(issue(), true), 200)
	if in.str("status") != "authenticated" || refreshCookie(in) == "" {
		t.Fatalf("canje vinculado = %s", in.Raw)
	}
	s := &session{a: a, token: in.str("access_token")}
	tt := s.tenantToken(A.id)
	if r := a.post(tt, "/api/v1/integrations/argosisp/sso/resolve", map[string]any{"type": "router", "external_id": params["router_id"]}); r.Status != 200 ||
		r.str("kind") != "router" {
		t.Fatalf("resolver router: %d %s", r.Status, r.Raw)
	}
	// El analista no tiene security.findings.read: el ticket no le da permisos.
	if r := a.post(tt, "/api/v1/integrations/argosisp/sso/resolve", map[string]any{"type": "hallazgo", "external_id": params["finding_id"]}); r.Status != 403 {
		t.Fatalf("hallazgo sin permiso: %d %s", r.Status, r.Raw)
	}
	mine := a.must(a.do(req{Method: "GET", Path: "/api/v1/me/sso-links", Token: fresh.token}), 200)
	if data, _ := mine.Body["data"].([]any); len(data) != 1 {
		t.Fatalf("mis vinculaciones = %s", mine.Raw)
	}
	if a.auditCount("auth.sso.ticket_issued") < 3 || a.auditCount("auth.sso.redeemed") < 3 || a.auditCount("auth.sso.linked") != 1 {
		t.Fatal("emisión, canje y vinculación auditados")
	}
	// El administrador revoca todas: el siguiente autologin vuelve a pedir vincular.
	all := a.must(a.do(req{Method: "DELETE", Path: "/api/v1/integrations/argosisp/sso/links", Token: A.token}), 200)
	if all.Body["revoked"] != float64(2) { // la del analista y la de la fixture del ISP
		t.Fatalf("revocar todas = %s", all.Raw)
	}
	if r := a.must(a.redeemSSO(issue(), true), 200); r.str("status") != "link_required" {
		t.Fatalf("tras revocar = %s", r.Raw)
	}
}

// H10 (Horus = B) y "Ver en ArgosISP": Horus canjea un enlace de Argos (el
// conector queda apagado y el hf_pat_ enviado sirve); si Argos lo rechaza, el
// token emitido se revoca. Con el conector activo, Horus pide el ticket a
// Argos de servidor a servidor.
func TestArgosRedeemLinkAndOpen(t *testing.T) {
	t.Parallel()
	a := startAppEnv(t, "HORUS_INTEGRATIONS_ALLOW_PRIVATE=true", "HORUS_ALERTS_WEBHOOK_ALLOW_PRIVATE=true",
		"HORUS_PUBLIC_BASE_URL=https://horus.test", "HORUS_INTEGRATIONS_SWEEP_EVERY=1h")
	pt := a.superadmin().platformToken()
	A := a.newISP(pt, "canje-a")
	params := a.fixtureISP(A, "10.10.0.0/24")
	peer := &fakeArgosPeer{code: strings.Repeat("B", 43)}
	peer.srv = httptest.NewServer(peer)
	defer peer.srv.Close()

	// Argos rechaza el código: 422 y el token emitido queda revocado.
	peer.status = http.StatusGone
	r := a.post(A.token, "/api/v1/integrations/argosisp/connection-links/redeem", map[string]any{"link": peer.srv.URL + "/conectar#" + peer.code})
	if r.Status != 422 || r.code() != "CONNECTION_LINK_EXPIRED" {
		t.Fatalf("enlace vencido: %d %s", r.Status, r.Raw)
	}
	var live int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM auth.api_token WHERE tenant_id = $1 AND name LIKE 'ArgosISP (enlace%' AND revoked_at IS NULL`,
		A.id).Scan(&live)
	if live != 0 {
		t.Fatal("el token emitido para un canje fallido se revoca")
	}
	peer.status = 0
	v := a.must(a.post(A.token, "/api/v1/integrations/argosisp/connection-links/redeem", map[string]any{"link": peer.srv.URL + "/conectar#" + peer.code}), 200)
	cx, _ := v.Body["connection"].(map[string]any)
	if v.Body["enabled"] != false || cx["method"] != "link_redeemed" || cx["peer_connector_id"] != "7" || v.Body["base_url"] != peer.srv.URL+"/api/v1" {
		t.Fatalf("conector = %s", v.Raw)
	}
	hook, _ := peer.received["webhook"].(map[string]any)
	if peer.received["base_url"] != "https://horus.test" || !strings.HasPrefix(hook["url"].(string), "https://horus.test/api/v1/integrations/argosisp/webhook/") {
		t.Fatalf("Argos recibió = %v", peer.received)
	}
	hfpat, _ := peer.received["token"].(string)
	a.must(a.post(hfpat, "/api/v1/integrations/security-states", map[string]any{"items": []any{map[string]any{"address": "10.10.0.41"}}}), 200)
	// Un enlace mal formado o a un destino no https (sin modo laboratorio lo rechazaría la guardia) → 422.
	if r := a.post(A.token, "/api/v1/integrations/argosisp/connection-links/redeem", map[string]any{"link": "https://argos.test/otra#x"}); r.Status != 422 {
		t.Fatalf("enlace mal formado: %d %s", r.Status, r.Raw)
	}

	// Ver en ArgosISP: necesita el conector activo y la identidad vigente del cliente.
	cust := params["customer_id"]
	if r := a.post(A.token, "/api/v1/integrations/argosisp/sso/open", map[string]any{"customer_id": cust}); r.Status != 409 {
		t.Fatalf("abrir con el conector apagado: %d %s", r.Status, r.Raw)
	}
	a.must(a.do(req{Method: "PUT", Path: "/api/v1/integrations/argosisp", Token: A.token,
		Body: map[string]any{"base_url": peer.srv.URL + "/api/v1", "enabled": true}}), 200)
	if _, err := a.admin.Exec(context.Background(), `INSERT INTO devices.customer_external_identity (id, tenant_id, customer_id, external_customer_id,
		external_service_id, valid_from, synced_at) VALUES ($1, $2, $3, 455, 812, now(), now())`, uuid.Must(uuid.NewV7()), A.id, cust); err != nil {
		t.Fatal(err)
	}
	open := a.must(a.post(A.token, "/api/v1/integrations/argosisp/sso/open", map[string]any{"customer_id": cust}), 200)
	if !strings.HasPrefix(open.str("url"), "https://panel.argos.test/sso/entrar#") {
		t.Fatalf("abrir = %s", open.Raw)
	}
	usr, _ := peer.tickets[0]["usuario"].(map[string]any)
	dst, _ := peer.tickets[0]["destino"].(map[string]any)
	if usr["correo"] != "admin@canje-a.test" || dst["tipo"] != "servicio" || dst["id_externo"] != "812" {
		t.Fatalf("ticket pedido = %v", peer.tickets[0])
	}
	if a.auditCount("integrations.argosisp.sso.opened") != 1 {
		t.Fatal("apertura auditada")
	}
}
