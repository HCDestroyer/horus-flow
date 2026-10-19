//go:build e2e

package e2eargos

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	code := m.Run()
	s := summary()
	fmt.Print(s)
	if p := os.Getenv("E2E_SUMMARY_FILE"); p != "" {
		_ = os.WriteFile(p, []byte(s), 0o600)
	}
	os.Exit(code)
}

// world es el estado compartido entre escenarios (en orden).
type world struct {
	c  cfg
	h  *horus
	a  *argos
	A  *isp // ISP de Horus conectado con esta instalación de ArgosISP
	B  *isp // otro ISP de Horus (aislamiento)
	at string
	// Datos de ArgosISP (demo:datos): servicio → IP, cliente, empresa.
	svc map[int]svcInfo
	// Token hf_pat_ y atk_ de prueba directos (contrato §3 y §2.1 en el cable).
	hfPat, atk, atkID string
	hfPatB            string
	oldIP2            string
	findingID         string
	channelID         string
	argosSecret       string
}

type svcInfo struct {
	ID, Cliente, Empresa, Router int
	IP, UserPPP                  string
}

func TestE2E(t *testing.T) {
	c := loadCfg(t)
	w := &world{c: c, svc: map[int]svcInfo{}}
	w.h = &horus{t: t, c: c, db: pool(t, c.HorusPG)}
	w.a = &argos{t: t, c: c, db: pool(t, c.ArgosPG)}

	if !scenario(t, "S00", "arranque y siembra de los dos sistemas", w.setup) {
		t.FailNow() // sin siembra, los demás escenarios no tienen sentido
	}
	scenario(t, "S01", "enlace generado en Horus y canjeado en Argos", w.linkFromHorus)
	scenario(t, "S02", "enlace generado en Argos y canjeado en Horus", w.linkFromArgos)
	scenario(t, "S03", "conectores apagados hasta que un administrador los activa", w.enableConnectors)
	scenario(t, "S04", "tokens hf_pat_ y atk_ de alcance mínimo y revocables", w.tokens)
	scenario(t, "S05", "resolver por lotes, respaldo GET /ip/:ip e identidad con vigencia", w.resolver)
	scenario(t, "S06", "webhooks firmados Argos → Horus", w.webhooksToHorus)
	scenario(t, "S07", "Argos lee de Horus: 360, consumo con vigencias y seguridad", w.reads)
	scenario(t, "S08", "webhooks firmados Horus → Argos y eventos internos", w.webhooksToArgos)
	scenario(t, "S09", "autologin Horus → Argos con vinculación y ACL de Argos", w.ssoToArgos)
	scenario(t, "S10", "autologin Argos → Horus con vinculación y ACL de Horus", w.ssoToHorus)
	scenario(t, "S11", "degradación: cada sistema sigue sin el otro", w.degradation)
	scenario(t, "S12", "datos personales y aislamiento entre ISP", w.isolation)
}

func (w *world) setup(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	must(t, do(t, rq{Method: "GET", URL: w.c.HorusURL + "/api/v1/auth/login"}), 405)
	// Superadministrador semilla: cambio de contraseña y 2FA en el primer login.
	sa := h.login("root@horus.test", "semilla-inicial-123")
	must(t, h.do("POST", "/me/password", sa.session, map[string]string{"current_password": "semilla-inicial-123", "new_password": horusUserPassword}), 204)
	sa.password = horusUserPassword
	sa.enrollTOTP()
	pt := must(t, h.do("POST", "/auth/token", sa.session, map[string]string{"scope": "platform"}), 200).str("access_token")
	w.A = h.newISP(pt, "isp-a", "100.64.0.0/16")
	w.B = h.newISP(pt, "isp-b", "100.64.0.0/16")

	// ArgosISP: datos de demostración cargados por su API (pnpm demo:datos).
	w.at = a.login(w.c.ArgosAdmin, w.c.ArgosAdminPass)
	rows, err := a.db.Query(t.Context(), `SELECT s.id, s.cliente_id, s.empresa_id, s.router_id, host(i.ip), coalesce(s.user_ppp, '')
		FROM core.servicios s JOIN network.ipv4_asignaciones i ON i.servicio_id = s.id AND i.fin IS NULL
		WHERE s.estado <> 'retirado' ORDER BY s.id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s svcInfo
		if err := rows.Scan(&s.ID, &s.Cliente, &s.Empresa, &s.Router, &s.IP, &s.UserPPP); err != nil {
			t.Fatal(err)
		}
		w.svc[s.ID] = s
	}
	rows.Close()
	if len(w.svc) < 4 {
		t.Fatalf("demo:datos: %d servicios con IP", len(w.svc))
	}
	// Horus ve a los clientes de esas IPs (el ingester los descubriría con los flujos).
	// (El último servicio no: Horus lo descubrirá por el bus en S05, §2.2.1.)
	for id, s := range w.svc {
		if id != w.lastSvc() {
			h.customer(w.A, s.IP)
		}
	}
	// Una IP que ArgosISP no conoce y la que tendrá el servicio 2 tras el cambio de IP.
	h.customer(w.A, "100.64.10.200")
	h.customer(w.A, "100.64.10.50")
	// El ISP B tiene un cliente con la MISMA IP que un servicio de A (otro realm).
	h.customer(w.B, w.svc[2].IP)
}

// linkFromHorus: §1.4 con Horus = A. Un uso, 15 min, enlace nuevo anula el
// anterior, conectores apagados, canal webhook apagado, repetición → 410.
func (w *world) linkFromHorus(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	tok := w.A.admin.tok()
	gen := func() string {
		r := must(t, h.do("POST", "/integrations/argosisp/connection-links", tok, nil), 201)
		u := r.str("url")
		if !strings.HasPrefix(u, w.c.HorusURL+"/conectar#") {
			t.Fatalf("enlace de Horus = %s", r.Raw)
		}
		return u
	}
	conectar := func(link string) resp {
		return a.do("POST", "/integraciones/horus/conexion/conectar", w.at, map[string]string{"enlace": link})
	}
	// Un enlace nuevo anula el anterior sin usar (1.5.0).
	old := gen()
	_ = gen()
	if r := conectar(old); r.Status != 422 || !strings.Contains(r.str("type"), "enlace_vencido") {
		t.Fatalf("enlace anulado canjeado en Argos: %d %s", r.Status, r.Raw)
	}
	// Vencido (15 min): se adelanta el reloj en la base de Horus.
	expired := gen()
	exec1(t, h.db, `UPDATE devices.argosisp_connection_link SET created_at = now() - interval '16 minutes', expires_at = now() - interval '1 minute' WHERE used_at IS NULL`)
	if r := conectar(expired); r.Status != 422 {
		t.Fatalf("enlace vencido: %d %s", r.Status, r.Raw)
	}
	link := gen()
	v := must(t, conectar(link), 200)
	if v.Body["activo"] != false || !strings.HasPrefix(v.str("url_base"), w.c.HorusURL) || v.str("token") != "***" ||
		!strings.Contains(v.str("webhook_salida_url"), "/api/v1/integrations/argosisp/webhook/") || v.str("conectado_por_enlace_en") == "" {
		t.Fatalf("conector de Argos tras el canje = %s", v.Raw)
	}
	// Un solo uso: Argos vuelve a pegar el mismo enlace (otra petición) → Horus 410 → 422 y nada cambia.
	before := q1(t, a.db, `SELECT token_argos_id FROM integraciones.horus_conector WHERE id = 1`)
	if r := conectar(link); r.Status != 422 {
		t.Fatalf("segundo canje del mismo enlace: %d %s", r.Status, r.Raw)
	}
	eq(t, "token atk_ vigente tras el canje fallido", q1(t, a.db, `SELECT token_argos_id FROM integraciones.horus_conector WHERE id = 1`), before)
	eq(t, "atk_ del canje vigente", q1(t, a.db, `SELECT count(*) FROM core.tokens_api WHERE id = $1 AND revocado_en IS NULL`, before), 1)
	// Horus: conector apagado, preparado por enlace, canal webhook apagado.
	cn := must(t, h.do("GET", "/integrations/argosisp", tok, nil), 200)
	eq(t, "conector Horus enabled", cn.Body["enabled"], false)
	eq(t, "conector Horus base_url", cn.Body["base_url"], w.c.ArgosURL+"/api/v1")
	eq(t, "método del enlace", gs(cn.Body, "connection.method"), "link_generated")
	ch := must(t, h.do("GET", "/notification-channels/"+gs(cn.Body, "connection.notification_channel_id"), tok, nil), 200)
	eq(t, "canal webhook kind", ch.Body["kind"], "webhook")
	eq(t, "canal webhook enabled", ch.Body["enabled"], false)
	eq(t, "URL del canal", gs(ch.Body, "config.url"), w.c.ArgosURL+"/api/v1/integraciones/horus/webhook")
	// Auditoría en los dos lados.
	if n := q1(t, h.db, `SELECT count(*) FROM auth.audit_log WHERE action = 'integrations.argosisp.connection_link.redeemed' AND outcome = 'success'`); fmt.Sprint(n) != "1" {
		t.Fatalf("canjes auditados en Horus = %v", n)
	}
	if n := q1(t, a.db, `SELECT count(*) FROM audit.acciones WHERE accion = 'horus.conectar_enlace' AND resultado = 'ok'`); fmt.Sprint(n) == "0" {
		t.Fatal("canje no auditado en Argos")
	}
}

// linkFromArgos: §1.4 con Argos = A. Primero un "Horus" de prueba (este
// test) comprueba el canje en el cable: idempotencia, 410, enlace anulado y
// vencido; al final el Horus real canjea un enlace y queda conectado.
func (w *world) linkFromArgos(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	gen := func() string {
		r := must(t, a.do("POST", "/integraciones/horus/enlace", w.at, nil), 201)
		if !strings.HasPrefix(r.str("enlace"), w.c.ArgosURL+"/conectar#") {
			t.Fatalf("enlace de Argos = %s", r.Raw)
		}
		return r.str("enlace")
	}
	code := func(link string) string { return link[strings.Index(link, "#")+1:] }
	body := func(link string) map[string]any {
		return map[string]any{"codigo": code(link), "sistema": "horus", "base_url": w.c.HorusURL,
			"token": "hf_pat_" + strings.Repeat("e2eFalso0", 5),
			"webhook": map[string]any{"url": w.c.HorusURL + "/api/v1/integrations/argosisp/webhook/00000000-0000-7000-8000-000000000001",
				"secreto": "whsec_secreto-de-prueba-e2e-0001"}}
	}
	canjear := func(b map[string]any) resp { return a.do("POST", "/integraciones/horus/conexion/canjear", "", b) }

	// Idempotencia (1.5.0): la MISMA petición repetida antes de que caduque → la misma respuesta.
	l1 := gen()
	b1 := body(l1)
	r1 := must(t, canjear(b1), 200)
	if !strings.HasPrefix(r1.str("token"), "atk_") || r1.str("conector_id") == "" || gs(r1.Body, "webhook.url") != w.c.ArgosURL+"/api/v1/integraciones/horus/webhook" ||
		len(gs(r1.Body, "webhook.secreto")) < 16 {
		t.Fatalf("canje en Argos = %s", r1.Raw)
	}
	r2 := canjear(b1)
	if r2.Status != 200 || string(r2.Raw) != string(r1.Raw) {
		t.Errorf("la misma petición repetida devuelve la misma respuesta (1.5.0): %d %s", r2.Status, r2.Raw)
	}
	// Otra petición con el código usado → 410.
	b1b := body(l1)
	b1b["token"] = "hf_pat_" + strings.Repeat("otroFalso", 5)
	if r := canjear(b1b); r.Status != 410 {
		t.Errorf("otra petición con un código usado: %d %s", r.Status, r.Raw)
	}
	// Un enlace nuevo anula el anterior sin usar (1.5.0).
	old := gen()
	_ = gen()
	if r := canjear(body(old)); r.Status != 410 {
		t.Errorf("enlace anulado por uno nuevo: %d %s", r.Status, r.Raw)
	}
	// Vencido.
	exp := gen()
	exec1(t, a.db, `UPDATE integraciones.horus_enlaces SET vence_en = now() - interval '1 second' WHERE usado_en IS NULL`)
	if r := canjear(body(exp)); r.Status != 410 {
		t.Errorf("enlace vencido: %d %s", r.Status, r.Raw)
	}
	// Datos inválidos → 422 (y el código no se consume).
	bad := gen()
	bb := body(bad)
	bb["token"] = "no-es-un-token"
	if r := canjear(bb); r.Status != 422 {
		t.Errorf("canje con datos inválidos: %d %s", r.Status, r.Raw)
	}
	// El Horus real canjea un enlace de Argos (Conectar con enlace).
	link := gen()
	tok := w.A.admin.tok()
	prevAtk := q1(t, a.db, `SELECT token_argos_id FROM integraciones.horus_conector WHERE id = 1`)
	v := must(t, h.do("POST", "/integrations/argosisp/connection-links/redeem", tok, map[string]string{"link": link}), 200)
	if v.Body["enabled"] != false || gs(v.Body, "connection.method") != "link_redeemed" || v.Body["base_url"] != w.c.ArgosURL+"/api/v1" {
		t.Fatalf("conector Horus = %s", v.Raw)
	}
	eq(t, "peer_connector_id", gs(v.Body, "connection.peer_connector_id"), q1(t, a.db, `SELECT conector_id FROM integraciones.horus_conector WHERE id = 1`))
	ac := must(t, a.do("GET", "/integraciones/horus", w.at, nil), 200)
	if ac.Body["activo"] != false || ac.str("url_base") != w.c.HorusURL ||
		ac.str("webhook_salida_url") != gs(v.Body, "webhook_url") {
		t.Fatalf("conector Argos = %s", ac.Raw)
	}
	// Un canje nuevo revoca el atk_ del anterior.
	eq(t, "atk_ anterior revocado", q1(t, a.db, `SELECT count(*) FROM core.tokens_api WHERE id = $1 AND revocado_en IS NOT NULL`, prevAtk), 1)
}

// enableConnectors: tras el canje nada fluye hasta activar; luego se
// activan los dos conectores y el canal webhook de Horus.
func (w *world) enableConnectors(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	tok := w.A.admin.tok()
	cust := w.A.customersByIP[w.svc[2].IP]
	// Apagado: Horus no resuelve y Argos no consulta.
	if r := h.do("POST", "/customers/"+cust+"/external-identity/refresh", tok, nil); r.Status != 409 {
		t.Fatalf("refresh con el conector apagado: %d %s", r.Status, r.Raw)
	}
	if r := a.do("GET", fmt.Sprintf("/servicios/%d/horus", w.svc[2].ID), w.at, nil); r.Status == 200 && r.Body["activo"] == true {
		t.Fatalf("Argos consulta a Horus con el conector apagado: %s", r.Raw)
	}
	// Webhook de Argos al receptor de Horus con el conector apagado → 404.
	cn := must(t, h.do("GET", "/integrations/argosisp", tok, nil), 200)
	body := []byte(`{"id":"e2e-apagado","evento":"cliente.actualizado","ocurrido_en":"2026-10-18T00:00:00Z","payload":{"cliente_id":1}}`)
	if r := do(t, rq{Method: "POST", URL: cn.str("webhook_url"), RawBody: body, Header: signedHeaders("whsec_cualquiera-e2e-0001", "e2e-apagado", time.Now(), body)}); r.Status != 404 {
		t.Fatalf("receptor de Horus con el conector apagado: %d %s", r.Status, r.Raw)
	}
	// Activar Horus (conector + canal webhook); Argos sigue apagado.
	must(t, h.do("PUT", "/integrations/argosisp", tok, map[string]any{"base_url": w.c.ArgosURL + "/api/v1", "enabled": true}), 200)
	s3 := w.svc[3]
	w.refresh(t, s3.IP)
	// Autologin hacia Argos con su conector apagado: Argos no emite tickets (1.7.0) y Argos no abre Horus.
	if r := h.do("POST", "/integrations/argosisp/sso/open", w.A.admin.tok(), map[string]any{"customer_id": w.A.customersByIP[s3.IP]}); r.Status != 502 ||
		!strings.Contains(r.str("detail"), "apagado el conector") {
		t.Fatalf("Ver en ArgosISP con el conector de Argos apagado: %d %s", r.Status, r.Raw)
	}
	if r := a.do("POST", "/integraciones/horus/abrir", w.at, map[string]any{"destino": map[string]any{"tipo": "servicio", "id_externo": fmt.Sprint(s3.ID)}}); r.Status != 422 {
		t.Fatalf("Ver en Horus con el conector de Argos apagado: %d %s", r.Status, r.Raw)
	}
	chID := gs(cn.Body, "connection.notification_channel_id")
	ch := must(t, h.do("GET", "/notification-channels/"+chID, tok, nil), 200)
	must(t, h.do("PATCH", "/notification-channels/"+chID, tok, map[string]any{"enabled": true},
		map[string]string{"If-Match": ch.Header.Get("ETag")}), 200)
	ver := a.version(w.at, "/integraciones/horus")
	must(t, a.do("PUT", "/integraciones/horus", w.at, map[string]any{"version": ver, "activo": true}), 200)
	waitFor(t, "estado de Argos activo", 10*time.Second, func() (bool, string) {
		r := a.do("GET", "/integraciones/horus/estado", w.at, nil)
		return r.Body["activo"] == true, string(r.Raw)
	})
	pr := must(t, a.do("POST", "/integraciones/horus/probar", w.at, nil), 200)
	eq(t, "prueba de Argos a Horus", pr.Body["ok"], true)
	ht := must(t, h.do("POST", "/integrations/argosisp/connection-test", tok, nil), 200)
	eq(t, "prueba de Horus a Argos", ht.Body["ok"], true)
	eq(t, "resolver usado por Horus", ht.Body["resolver"], "batch")
}

// identity es GET /customers/{id}/external-identity del ISP A.
func (w *world) identity(t *testing.T, ip string) resp {
	t.Helper()
	cust := w.A.customersByIP[ip]
	if cust == "" {
		t.Fatalf("Horus no tiene cliente %s", ip)
	}
	return must(t, w.h.do("GET", "/customers/"+cust+"/external-identity", w.A.admin.tok(), nil), 200)
}

func (w *world) refresh(t *testing.T, ip string) resp {
	t.Helper()
	return must(t, w.h.do("POST", "/customers/"+w.A.customersByIP[ip]+"/external-identity/refresh", w.A.admin.tok(), nil), 200)
}

// tokens: §1.1 y §1.2. Un hf_pat_ con la plantilla ArgosISP lee lo de §3 y
// nada más; un atk_ resuelve IPs y nada más; revocar corta el acceso.
func (w *world) tokens(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	tok := w.A.admin.tok()
	mk := func(x *isp, name string) (string, string) {
		r := must(t, h.do("POST", "/api-tokens", x.admin.tok(), map[string]any{"name": name, "template": "argosisp"}), 201)
		return r.str("token"), gs(r.Body, "api_token.id") + gs(r.Body, "id")
	}
	w.hfPat, _ = mk(w.A, "E2E Argos A")
	w.hfPatB, _ = mk(w.B, "E2E Argos B")
	if !strings.HasPrefix(w.hfPat, "hf_pat_") {
		t.Fatalf("hf_pat_ = %q", w.hfPat)
	}
	// §3.3: salud de la red con el hf_pat_.
	must(t, h.do("GET", "/routers/status", w.hfPat, nil), 200)
	must(t, h.do("GET", "/infra-services", w.hfPat, nil), 200)
	// Mínimo privilegio: no administra nada.
	if r := h.do("GET", "/api-tokens", w.hfPat, nil); r.Status != 403 {
		t.Fatalf("hf_pat_ lista tokens: %d", r.Status)
	}
	if r := h.do("PUT", "/integrations/argosisp", w.hfPat, map[string]any{"base_url": "https://x.example/api/v1", "enabled": false}); r.Status != 403 {
		t.Fatalf("hf_pat_ configura el conector: %d", r.Status)
	}
	// atk_ de ArgosISP: solo ip.resolver, clientes.ver, servicios.ver y sso.emitir.
	ar := must(t, a.do("POST", "/integraciones/horus/tokens", w.at, map[string]any{"nombre": "E2E directo"}), 201)
	w.atk, w.atkID = ar.str("token"), gs(ar.Body, "datos.id")
	if !strings.HasPrefix(w.atk, "atk_") {
		t.Fatalf("atk_ = %s", ar.Raw)
	}
	perms := gs(ar.Body, "datos.alcances.permisos")
	for _, p := range []string{"ip.resolver", "clientes.ver", "servicios.ver", "sso.emitir"} {
		if !strings.Contains(perms, p) {
			t.Fatalf("permisos del atk_ = %s", perms)
		}
	}
	must(t, a.do("POST", "/integraciones/horus/resolver", w.atk, map[string]any{"ips": []string{w.svc[2].IP}}), 200)
	if r := a.do("GET", "/facturas", w.atk, nil); r.Status != 403 {
		t.Fatalf("atk_ lee facturas: %d", r.Status)
	}
	if r := a.do("POST", fmt.Sprintf("/servicios/%d/suspender", w.svc[2].ID), w.atk, map[string]any{"motivo": "no autorizado"}); r.Status != 403 {
		t.Fatalf("atk_ suspende: %d", r.Status)
	}
	// Revocar corta el acceso (tokens desechables, no los del conector).
	tmp := must(t, a.do("POST", "/integraciones/horus/tokens", w.at, map[string]any{"nombre": "E2E revocable"}), 201)
	must(t, a.do("POST", "/integraciones/horus/tokens/"+gs(tmp.Body, "datos.id")+"/revocar", w.at, nil), 200)
	if r := a.do("POST", "/integraciones/horus/resolver", tmp.str("token"), map[string]any{"ips": []string{w.svc[2].IP}}); r.Status != 401 {
		t.Fatalf("atk_ revocado: %d %s", r.Status, r.Raw)
	}
	hp := must(t, h.do("POST", "/api-tokens", tok, map[string]any{"name": "E2E revocable", "template": "argosisp"}), 201)
	id := gs(hp.Body, "id")
	if id == "" {
		id = gs(hp.Body, "api_token.id")
	}
	must(t, h.do("DELETE", "/api-tokens/"+id, tok, nil), 204)
	if r := h.do("POST", "/integrations/security-states", hp.str("token"), map[string]any{"items": []any{map[string]any{"address": w.svc[2].IP}}}); r.Status != 401 {
		t.Fatalf("hf_pat_ revocado: %d %s", r.Status, r.Raw)
	}
}

// resolver: §2.1 en el cable (atk_ directo) y en Horus (identidad, alias,
// empresa, vigencia), barrido periódico y respaldo GET /ip/:ip.
func (w *world) resolver(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	s2 := w.svc[2]
	r := must(t, a.do("POST", "/integraciones/horus/resolver", w.atk, map[string]any{"ips": []string{s2.IP, "100.64.10.200", "2800:abcd:1:2::1"}}), 200)
	res, _ := r.Body["resultados"].([]any)
	if len(res) != 3 {
		t.Fatalf("resultados = %s", r.Raw)
	}
	first := res[0]
	eq(t, "ip", gs(first, "ip"), s2.IP)
	eq(t, "servicio_id", gs(first, "servicio_id"), s2.ID)
	eq(t, "cliente_id", gs(first, "cliente_id"), s2.Cliente)
	eq(t, "prefijo", gs(first, "prefijo"), s2.IP+"/32")
	eq(t, "servicio.user_ppp", gs(first, "servicio.user_ppp"), s2.UserPPP)
	eq(t, "servicio.estado", gs(first, "servicio.estado"), "activo")
	eq(t, "servicio.router_id", gs(first, "servicio.router_id"), s2.Router)
	eq(t, "empresa.id", gs(first, "empresa.id"), s2.Empresa)
	for _, k := range []string{"cliente.nombre", "cliente.codigo", "cliente.tipo", "servicio.plan", "empresa.nombre", "desde"} {
		if gs(first, k) == "" {
			t.Fatalf("%s vacío en %s", k, r.Raw)
		}
	}
	if _, err := time.Parse(time.RFC3339, gs(first, "desde")); err != nil {
		t.Fatalf("desde no es ISO-8601: %v", err)
	}
	eq(t, "IP desconocida", gs(res[1], "error"), "no_encontrada")
	eq(t, "IPv6 desconocida", gs(res[2], "error"), "no_encontrada")
	many := make([]string, 501)
	for i := range many {
		many[i] = fmt.Sprintf("100.65.%d.%d", i/250, i%250+1)
	}
	if r := a.do("POST", "/integraciones/horus/resolver", w.atk, map[string]any{"ips": many}); r.Status != 422 {
		t.Fatalf("501 IPs: %d", r.Status)
	}
	// Respaldo (§2.1 compatibilidad): GET /ip/:ip, /clientes/:id y /servicios/:id con lo que Horus lee.
	ip := must(t, a.do("GET", "/ip/"+s2.IP, w.atk, nil), 200)
	eq(t, "GET /ip servicio_id", gs(ip.Body, "servicio_id"), s2.ID)
	eq(t, "GET /ip cliente_id", gs(ip.Body, "cliente_id"), s2.Cliente)
	cl := must(t, a.do("GET", fmt.Sprintf("/clientes/%d", s2.Cliente), w.atk, nil), 200)
	eq(t, "GET /clientes nombre", cl.str("nombre"), gs(first, "cliente.nombre"))
	eq(t, "GET /clientes codigo", cl.str("codigo"), gs(first, "cliente.codigo"))
	sv := must(t, a.do("GET", fmt.Sprintf("/servicios/%d", s2.ID), w.atk, nil), 200)
	eq(t, "GET /servicios estado", sv.str("estado"), "activo")
	eq(t, "GET /servicios empresa_id", gs(sv.Body, "empresa_id"), s2.Empresa)

	// Horus: resolución a demanda («Actualizar desde ArgosISP»).
	id := w.refresh(t, s2.IP)
	cur := id.obj("current")
	if cur == nil {
		t.Fatalf("identidad vigente = %s", id.Raw)
	}
	eq(t, "external customer_id", gs(cur, "customer_id"), s2.Cliente)
	eq(t, "external service_id", gs(cur, "service_id"), s2.ID)
	eq(t, "code", gs(cur, "code"), gs(first, "cliente.codigo"))
	eq(t, "display_name", gs(cur, "display_name"), gs(first, "cliente.nombre"))
	eq(t, "service_state", gs(cur, "service_state"), "activo")
	eq(t, "plan", gs(cur, "plan"), gs(first, "servicio.plan"))
	eq(t, "company_id", gs(cur, "company_id"), s2.Empresa)
	eq(t, "company_name", gs(cur, "company_name"), gs(first, "empresa.nombre"))
	eq(t, "stale", gs(cur, "stale"), "false")
	eq(t, "last_check_status", id.str("last_check_status"), "found")
	c := must(t, h.do("GET", "/customers/"+w.A.customersByIP[s2.IP], w.A.admin.tok(), nil), 200)
	eq(t, "alias = display_name", c.str("alias"), gs(first, "cliente.nombre"))
	eq(t, "alias_source", c.str("alias_source"), "argosisp")
	// IP que ArgosISP no conoce: sin identidad, not_found.
	nf := w.refresh(t, "100.64.10.200")
	if nf.Body["current"] != nil || nf.str("last_check_status") != "not_found" {
		t.Fatalf("IP desconocida = %s", nf.Raw)
	}
	// Cliente descubierto (§2.2.1): el ingester lo anuncia en el bus; devices lo crea y lo resuelve.
	last := w.svc[w.lastSvc()]
	w.discover(t, last.IP)
	waitFor(t, "identidad del cliente descubierto", 30*time.Second, func() (bool, string) {
		var cid string
		_ = h.db.QueryRow(t.Context(), `SELECT id::text FROM devices.customer WHERE tenant_id = $1 AND address = $2::inet`, w.A.id, last.IP).Scan(&cid)
		if cid == "" {
			return false, "sin cliente"
		}
		w.A.customersByIP[last.IP] = cid
		id := w.identity(t, last.IP)
		return gs(id.Body, "current.service_id") == fmt.Sprint(last.ID), gs(id.Body, "current")
	})
	// Barrido periódico (§2.2.3): los demás clientes se resuelven solos.
	waitFor(t, "barrido periódico", 60*time.Second, func() (bool, string) {
		n := q1(t, h.db, `SELECT count(*) FROM devices.customer_external_identity WHERE tenant_id = $1 AND valid_to IS NULL`, w.A.id)
		return fmt.Sprint(n) == fmt.Sprint(len(w.svc)), fmt.Sprintf("%v vigentes de %d", n, len(w.svc))
	})
	cn := must(t, h.do("GET", "/integrations/argosisp", w.A.admin.tok(), nil), 200)
	eq(t, "resolver", cn.Body["resolver"], "batch")
	eq(t, "last_sync_status", cn.Body["last_sync_status"], "ok")
	// El ISP B no ve nada de A (su cliente con la misma IP no tiene identidad).
	eq(t, "identidades del ISP B", q1(t, h.db, `SELECT count(*) FROM devices.customer_external_identity WHERE tenant_id = $1`, w.B.id), 0)
	// Auditoría de la lectura de datos personales en Argos (§0), actor = token.
	if n := q1(t, a.db, `SELECT count(*) FROM audit.acciones WHERE accion = 'ip.resolver'`); fmt.Sprint(n) == "0" {
		t.Fatal("lecturas de Horus sin auditar en Argos")
	}

	// Respaldo de extremo a extremo: un proxy delante de Argos responde 404 al resolver por lotes.
	px := newFallbackProxy(t, w.c.ArgosURL)
	tok := w.A.admin.tok()
	must(t, h.do("PUT", "/integrations/argosisp", tok, map[string]any{"base_url": px.URL + "/api/v1", "enabled": true}), 200)
	s3 := w.svc[3]
	fb := w.refresh(t, s3.IP)
	eq(t, "respaldo: service_id", gs(fb.Body, "current.service_id"), s3.ID)
	eq(t, "respaldo: customer_id", gs(fb.Body, "current.customer_id"), s3.Cliente)
	eq(t, "respaldo: service_state", gs(fb.Body, "current.service_state"), "activo")
	eq(t, "respaldo: company_id", gs(fb.Body, "current.company_id"), s3.Empresa)
	cn = must(t, h.do("GET", "/integrations/argosisp", tok, nil), 200)
	eq(t, "resolver con respaldo", cn.Body["resolver"], "fallback")
	eq(t, "estado con respaldo", cn.Body["last_sync_status"], "degraded")
	if px.hits("/api/v1/ip/") == 0 {
		t.Fatal("Horus no usó GET /ip/:ip")
	}
	must(t, h.do("PUT", "/integrations/argosisp", tok, map[string]any{"base_url": w.c.ArgosURL + "/api/v1", "enabled": true}), 200)
	w.refresh(t, s3.IP)
}

// inbox cuenta los webhooks de Argos recibidos por Horus de un evento.
func (w *world) inbox(t *testing.T, event string) int {
	t.Helper()
	n, _ := q1(t, w.h.db, `SELECT count(*) FROM devices.argosisp_webhook_inbox WHERE tenant_id = $1 AND event = $2`, w.A.id, event).(int64)
	return int(n)
}

// sent espera a que Argos entregue (estado enviado) un webhook de un evento.
func (w *world) sent(t *testing.T, event string, after int) {
	t.Helper()
	waitFor(t, "envío "+event+" a Horus", 30*time.Second, func() (bool, string) {
		n, _ := q1(t, w.a.db, `SELECT count(*) FROM integraciones.horus_envios WHERE evento = $1 AND estado = 'enviado'`, event).(int64)
		var last string
		_ = w.a.db.QueryRow(t.Context(), `SELECT coalesce(estado || ' ' || coalesce(ultimo_error, ''), '') FROM integraciones.horus_envios WHERE evento = $1 ORDER BY id DESC LIMIT 1`, event).Scan(&last)
		return int(n) > after, fmt.Sprintf("%d enviados; último: %s", n, last)
	})
}

func (w *world) sentCount(t *testing.T, event string) int {
	n, _ := q1(t, w.a.db, `SELECT count(*) FROM integraciones.horus_envios WHERE evento = $1 AND estado = 'enviado'`, event).(int64)
	return int(n)
}

// webhooksToHorus: §2.3. Cada evento de Argos llega firmado al receptor de
// Horus (URL con connector_id) y Horus re-resuelve: nombre, estado, IP,
// sesiones y empresa. Firma, desfase y deduplicación.
func (w *world) webhooksToHorus(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	// cliente.actualizado → nombre y alias en Horus.
	s3 := w.svc[3]
	n0 := w.sentCount(t, "cliente.actualizado")
	ver := a.version(w.at, fmt.Sprintf("/clientes/%d", s3.Cliente))
	must(t, a.do("PUT", fmt.Sprintf("/clientes/%d", s3.Cliente), w.at, map[string]any{"version": ver, "nombre": "Distribuidora E2E Renombrada"}), 200)
	w.sent(t, "cliente.actualizado", n0)
	waitFor(t, "nombre nuevo en Horus", 30*time.Second, func() (bool, string) {
		id := w.identity(t, s3.IP)
		return gs(id.Body, "current.display_name") == "Distribuidora E2E Renombrada", gs(id.Body, "current")
	})
	c := must(t, h.do("GET", "/customers/"+w.A.customersByIP[s3.IP], w.A.admin.tok(), nil), 200)
	eq(t, "alias tras cliente.actualizado", c.str("alias"), "Distribuidora E2E Renombrada")

	// servicio.estado_cambiado → estado en Horus (Horus no decide cortes: solo lo muestra).
	s4 := w.svc[4]
	n0 = w.sentCount(t, "servicio.estado_cambiado")
	must(t, a.do("POST", fmt.Sprintf("/servicios/%d/suspender", s4.ID), w.at, map[string]any{"motivo": "prueba e2e"}), 200)
	w.sent(t, "servicio.estado_cambiado", n0)
	waitFor(t, "estado suspendido en Horus", 30*time.Second, func() (bool, string) {
		id := w.identity(t, s4.IP)
		return gs(id.Body, "current.service_state") == "suspendido", gs(id.Body, "current")
	})
	must(t, a.do("POST", fmt.Sprintf("/servicios/%d/reactivar", s4.ID), w.at, map[string]any{"motivo": "prueba e2e"}), 200)
	waitFor(t, "estado activo en Horus", 30*time.Second, func() (bool, string) {
		id := w.identity(t, s4.IP)
		return gs(id.Body, "current.service_state") == "activo", gs(id.Body, "current")
	})

	// asignacion.cambiada → cierra la vigencia de la IP anterior y abre la nueva.
	s2 := w.svc[2]
	n0 = w.sentCount(t, "asignacion.cambiada")
	ver = a.version(w.at, fmt.Sprintf("/servicios/%d", s2.ID))
	must(t, a.do("PUT", fmt.Sprintf("/servicios/%d", s2.ID), w.at, map[string]any{"version": ver, "ipv4": "100.64.10.50"}), 200)
	w.sent(t, "asignacion.cambiada", n0)
	waitFor(t, "IP nueva del servicio en Horus", 30*time.Second, func() (bool, string) {
		nw := w.identity(t, "100.64.10.50")
		old := w.identity(t, s2.IP)
		return gs(nw.Body, "current.service_id") == fmt.Sprint(s2.ID) && old.Body["current"] == nil,
			"nueva=" + gs(nw.Body, "current") + " anterior=" + gs(old.Body, "current")
	})
	old := w.identity(t, s2.IP)
	if hist, _ := old.Body["history"].([]any); len(hist) == 0 || gs(hist[0], "valid_to") == "" || gs(hist[0], "external_service_id") != fmt.Sprint(s2.ID) {
		t.Fatalf("vigencia anterior sin cerrar: %s", old.Raw)
	}
	w.svc[2] = svcInfo{ID: s2.ID, Cliente: s2.Cliente, Empresa: s2.Empresa, Router: s2.Router, IP: "100.64.10.50", UserPPP: s2.UserPPP}
	w.oldIP2 = s2.IP

	// sesion.iniciada / sesion.terminada: accounting RADIUS real (aaa → sesiones:worker → outbox).
	s5 := w.svc[5]
	acct := func(kind, session string) {
		body := map[string]any{"Acct-Status-Type": map[string]any{"value": []string{kind}}, "User-Name": map[string]any{"value": []string{s5.UserPPP}},
			"NAS-IP-Address": map[string]any{"value": []string{"10.20.0.1"}}, "Acct-Session-Id": map[string]any{"value": []string{session}},
			"Framed-IP-Address": map[string]any{"value": []string{s5.IP}}, "Event-Timestamp": map[string]any{"value": []string{fmt.Sprint(time.Now().Unix())}}}
		r := do(t, rq{Method: "POST", URL: w.c.AAAURL + "/aaa/accounting", Body: body})
		if r.Status >= 300 {
			t.Fatalf("accounting %s: %d %s", kind, r.Status, r.Raw)
		}
	}
	n0 = w.sentCount(t, "sesion.iniciada")
	i0 := w.inbox(t, "sesion.iniciada")
	acct("Start", "e2e-sesion-1")
	w.sent(t, "sesion.iniciada", n0)
	waitFor(t, "sesion.iniciada en Horus", 20*time.Second, func() (bool, string) { return w.inbox(t, "sesion.iniciada") > i0, "" })
	n0 = w.sentCount(t, "sesion.terminada")
	acct("Stop", "e2e-sesion-1")
	w.sent(t, "sesion.terminada", n0)
	waitFor(t, "sesion.terminada en Horus", 20*time.Second, func() (bool, string) { return w.inbox(t, "sesion.terminada") > 0, "" })
	var payload map[string]any
	_ = h.db.QueryRow(t.Context(), `SELECT payload FROM devices.argosisp_webhook_inbox WHERE tenant_id = $1 AND event = 'sesion.iniciada' ORDER BY received_at DESC LIMIT 1`, w.A.id).Scan(&payload)
	if gs(payload, "payload.ipv4") != s5.IP || gs(payload, "payload.servicio_id") != fmt.Sprint(s5.ID) || gs(payload, "payload.empresa_id") != fmt.Sprint(s5.Empresa) ||
		gs(payload, "evento") != "sesion.iniciada" || gs(payload, "ocurrido_en") == "" || !strings.HasPrefix(gs(payload, "id"), "argos-evt-") {
		t.Fatalf("webhook sesion.iniciada = %v", payload)
	}

	// Firma (§1.3): el receptor de Horus rechaza firmas malas, desfase > 5 min y conectores desconocidos.
	tok := w.A.admin.tok()
	sec := must(t, h.do("POST", "/integrations/argosisp/webhook-secret", tok, nil), 200)
	url, secret := sec.str("webhook_url"), sec.str("webhook_secret")
	if url == "" || secret == "" {
		t.Fatalf("webhook-secret = %s", sec.Raw)
	}
	body := []byte(fmt.Sprintf(`{"id":"e2e-firma-1","evento":"cliente.actualizado","ocurrido_en":"%s","payload":{"cliente_id":%d}}`, time.Now().UTC().Format(time.RFC3339), s3.Cliente))
	post := func(u string, hdr map[string]string) int {
		return do(t, rq{Method: "POST", URL: u, RawBody: body, Header: hdr}).Status
	}
	eq(t, "firma inválida", post(url, signedHeaders("whsec_otro-secreto-e2e-00001", "e2e-firma-1", time.Now(), body)), 401)
	eq(t, "desfase de 6 min", post(url, signedHeaders(secret, "e2e-firma-1", time.Now().Add(-6*time.Minute), body)), 401)
	unknown := url[:strings.LastIndex(url, "/")+1] + "00000000-0000-7000-8000-00000000dead"
	eq(t, "conector desconocido", post(unknown, signedHeaders(secret, "e2e-firma-1", time.Now(), body)), 404)
	eq(t, "firma válida", post(url, signedHeaders(secret, "e2e-firma-1", time.Now(), body)), 202)
	eq(t, "duplicado", post(url, signedHeaders(secret, "e2e-firma-1", time.Now(), body)), 202)
	eq(t, "deduplicado por X-Webhook-Id", q1(t, h.db, `SELECT count(*) FROM devices.argosisp_webhook_inbox WHERE webhook_id = 'e2e-firma-1'`), 1)
	// El secreto anterior sigue valiendo 24 h (Argos aún firma con él); se le pasa el nuevo a Argos.
	ver = a.version(w.at, "/integraciones/horus")
	must(t, a.do("PUT", "/integraciones/horus", w.at, map[string]any{"version": ver, "webhook_salida_secreto": secret}), 200)
	n0 = w.sentCount(t, "cliente.actualizado")
	ver = a.version(w.at, fmt.Sprintf("/clientes/%d", s3.Cliente))
	must(t, a.do("PUT", fmt.Sprintf("/clientes/%d", s3.Cliente), w.at, map[string]any{"version": ver, "nombre": "Distribuidora López"}), 200)
	w.sent(t, "cliente.actualizado", n0)
	waitFor(t, "nombre con el secreto rotado", 30*time.Second, func() (bool, string) {
		id := w.identity(t, s3.IP)
		return gs(id.Body, "current.display_name") == "Distribuidora López", gs(id.Body, "current")
	})

	// Multiempresa (§2.3, empresa_id): el servicio pasa a otra empresa y Horus lo refleja.
	emp := must(t, a.do("POST", "/empresas", w.at, map[string]any{"nit": "1234567-9", "razon_social": "Fibra Sur, S.A.", "nombre_comercial": "Fibra Sur"}), 201)
	empID := gs(emp.Body, "id")
	s6 := w.svc[6]
	ver = a.version(w.at, fmt.Sprintf("/servicios/%d", s6.ID))
	must(t, a.do("POST", fmt.Sprintf("/servicios/%d/cambiar-empresa", s6.ID), w.at, map[string]any{"empresa_id": emp.Body["id"], "version": ver, "motivo": "prueba e2e"}), 200)
	waitFor(t, "empresa nueva en Horus", 30*time.Second, func() (bool, string) {
		id := w.identity(t, s6.IP)
		return gs(id.Body, "current.company_id") == empID && gs(id.Body, "current.company_name") == "Fibra Sur", gs(id.Body, "current")
	})
	w.svc[6] = svcInfo{ID: s6.ID, Cliente: s6.Cliente, Empresa: int(emp.Body["id"].(float64)), Router: s6.Router, IP: s6.IP, UserPPP: s6.UserPPP}
}

func (w *world) lastSvc() int {
	last := 0
	for id := range w.svc {
		last = max(last, id)
	}
	return last
}
