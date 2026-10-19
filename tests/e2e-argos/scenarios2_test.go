//go:build e2e

package e2eargos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
)

const gb = 1 << 30

// flows inserta consumo de un cliente de A en los agregados de ClickHouse
// (lo que dejaría el ingester tras recibir sus flujos).
func (w *world) flows(t *testing.T, ip string, bucket time.Time, down, up uint64) {
	t.Helper()
	for _, tbl := range []string{"flows.customer_5m", "flows.customer_1h"} {
		extra, extraVal := "", ""
		if tbl == "flows.customer_1h" {
			extra, extraVal = ", remote_asn", ", 15169"
		}
		for _, d := range []struct {
			dir   string
			bytes uint64
		}{{"download", down}, {"upload", up}} {
			chExec(t, w.c, fmt.Sprintf(`INSERT INTO %s (tenant_id, site_id, realm_id, client_ip, bucket, service_id%s, direction, bytes, packets, flows, remote_ips)
				SELECT toUUID('%s'), toUUID('%s'), toUUID('%s'), toIPv6('%s'), toDateTime('%s', 'UTC'), toUUID('00000000-0000-0000-0000-000000000000')%s, '%s', %d, 1000, 10, uniqState(toIPv6('8.8.8.8'))`,
				tbl, extra, w.A.id, w.A.site, w.A.realm, ip, bucket.UTC().Format("2006-01-02 15:04:05"), extraVal, d.dir, d.bytes))
		}
	}
}

// reads: §3.1, §3.2 y §3.5 en el cable (hf_pat_) y desde las pantallas de
// Argos. El consumo por servicio suma cada IP solo dentro de su vigencia.
func (w *world) reads(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	s2, s3 := w.svc[2], w.svc[3]
	now := time.Now().UTC().Truncate(time.Hour)
	// Vigencias del servicio 2: la IP anterior de now-10h a now-4h y la actual desde now-4h.
	exec1(t, h.db, `UPDATE devices.customer_external_identity SET valid_from = $3, valid_to = $4
		WHERE tenant_id = $1 AND customer_id = $2 AND external_service_id = $5`, w.A.id, w.A.customersByIP[w.oldIP2], now.Add(-10*time.Hour), now.Add(-4*time.Hour), s2.ID)
	exec1(t, h.db, `UPDATE devices.customer_external_identity SET valid_from = $3
		WHERE tenant_id = $1 AND customer_id = $2 AND valid_to IS NULL`, w.A.id, w.A.customersByIP[s2.IP], now.Add(-4*time.Hour))
	w.flows(t, w.oldIP2, now.Add(-6*time.Hour), 1*gb, 100) // del servicio 2 (IP anterior, en vigencia)
	w.flows(t, s2.IP, now.Add(-2*time.Hour), 2*gb, 200)    // del servicio 2 (IP actual)
	w.flows(t, w.oldIP2, now.Add(-2*time.Hour), gb/2, 50)  // de otro: la IP anterior ya no era del servicio
	w.flows(t, s2.IP, now.Add(-6*time.Hour), 3*gb/10, 30)  // de otro: la IP actual aún no era del servicio
	from, to := now.Add(-24*time.Hour).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)

	// §3.2 por servicio y por IP.
	c := must(t, h.do("POST", "/integrations/consumption", w.hfPat, map[string]any{"from": from, "to": to, "granularity": "total",
		"items": []any{map[string]any{"external_service_id": s2.ID}, map[string]any{"address": w.oldIP2}, map[string]any{"external_service_id": 999999}}}), 200)
	items, _ := c.Body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("consumo = %s", c.Raw)
	}
	eq(t, "bajada del servicio (dos IPs en su vigencia)", gs(items[0], "down_bytes"), fmt.Sprint(uint64(3*gb)))
	eq(t, "subida del servicio", gs(items[0], "up_bytes"), "300")
	if a := gs(items[0], "addresses"); !strings.Contains(a, w.oldIP2) || !strings.Contains(a, s2.IP) {
		t.Fatalf("addresses del servicio = %s", a)
	}
	eq(t, "bajada de la IP anterior (todos sus dueños)", gs(items[1], "down_bytes"), fmt.Sprint(uint64(gb+gb/2)))
	eq(t, "servicio desconocido", gs(items[2], "error"), "not_found")
	day := must(t, h.do("POST", "/integrations/consumption", w.hfPat, map[string]any{"from": from, "to": to, "granularity": "day",
		"items": []any{map[string]any{"external_service_id": s2.ID}}}), 200)
	if series, _ := get(day.Body, "items.0.series").([]any); len(series) == 0 || gs(series[0], "day") == "" {
		t.Fatalf("serie diaria = %s", day.Raw)
	}

	// §3.1 por IP y por servicio; `at` en el pasado da la IP de entonces.
	lk := must(t, h.do("POST", "/integrations/ip-lookup", w.hfPat, map[string]any{"address": s2.IP, "include": []string{"consumption", "health", "security"}}), 200)
	eq(t, "360 external.service_id", gs(lk.Body, "external.service_id"), s2.ID)
	eq(t, "360 external.customer_id", gs(lk.Body, "external.customer_id"), s2.Cliente)
	eq(t, "360 external.company_id", gs(lk.Body, "external.company_id"), s2.Empresa)
	eq(t, "360 external.system", gs(lk.Body, "external.system"), "argosisp")
	eq(t, "360 customer.address", gs(lk.Body, "customer.address"), s2.IP)
	if gs(lk.Body, "consumption.last_24h.down_bytes") == "" || gs(lk.Body, "meta.at") == "" || get(lk.Body, "health") == nil {
		t.Fatalf("360 sin consumo, salud o meta: %s", lk.Raw)
	}
	bySvc := must(t, h.do("POST", "/integrations/ip-lookup", w.hfPat, map[string]any{"external_service_id": s2.ID}), 200)
	eq(t, "360 por servicio: IP actual", gs(bySvc.Body, "customer.address"), s2.IP)
	past := must(t, h.do("POST", "/integrations/ip-lookup", w.hfPat, map[string]any{"external_service_id": s2.ID, "at": now.Add(-6 * time.Hour).Format(time.RFC3339)}), 200)
	eq(t, "360 por servicio en el pasado: IP de entonces", gs(past.Body, "customer.address"), w.oldIP2)
	if r := h.do("POST", "/integrations/ip-lookup", w.hfPat, map[string]any{"address": s2.IP, "external_service_id": s2.ID}); r.Status != 422 {
		t.Fatalf("360 con address y external_service_id: %d", r.Status)
	}
	if r := h.do("POST", "/integrations/ip-lookup", w.hfPat, map[string]any{"address": "100.64.99.99"}); r.Status != 404 {
		t.Fatalf("360 de una IP nunca vista: %d", r.Status)
	}

	// §3.5 estado de seguridad por lotes.
	exec1(t, h.db, `INSERT INTO detection.customer_security (tenant_id, customer_id, site_id, state, open_findings, max_severity, since, updated_at)
		VALUES ($1, $2, $3, 'suspected', 1, 'high', now() - interval '1 hour', now())`, w.A.id, w.A.customersByIP[s3.IP], w.A.site)
	ss := must(t, h.do("POST", "/integrations/security-states", w.hfPat, map[string]any{"items": []any{
		map[string]any{"external_service_id": s3.ID}, map[string]any{"address": s3.IP}, map[string]any{"external_service_id": 999999}}}), 200)
	eq(t, "seguridad por servicio", gs(ss.Body, "items.0.state"), "suspected")
	eq(t, "seguridad: address del servicio", gs(ss.Body, "items.0.address"), s3.IP)
	eq(t, "seguridad: max_severity", gs(ss.Body, "items.0.max_severity"), "high")
	eq(t, "seguridad por IP: external_service_id", gs(ss.Body, "items.1.external_service_id"), s3.ID)
	eq(t, "seguridad de un servicio desconocido", gs(ss.Body, "items.2.error"), "not_found")

	// Desde Argos (lo que ve la ficha del servicio y la columna "Seguridad").
	f := must(t, a.do("GET", fmt.Sprintf("/servicios/%d/horus", s2.ID), w.at, nil), 200)
	eq(t, "ficha: conector", f.str("conector"), "activo")
	eq(t, "ficha: stale", f.Body["stale"], false)
	eq(t, "ficha: consulta", f.str("consulta"), "external_service_id")
	eq(t, "ficha: datos.external.service_id", gs(f.Body, "datos.external.service_id"), s2.ID)
	sec := must(t, a.do("POST", "/integraciones/horus/seguridad", w.at, map[string]any{"servicio_ids": []int{s3.ID, s2.ID}}), 200)
	eq(t, "columna seguridad: modo", sec.str("modo"), "lotes")
	eq(t, "columna seguridad: estado", gs(sec.Body, "items.0.estado"), "suspected")
	eq(t, "columna seguridad: fuente", gs(sec.Body, "items.0.fuente"), "horus")
	gt, _ := time.LoadLocation("America/Guatemala")
	if now.Add(-10*time.Hour).In(gt).Month() == time.Now().In(gt).Month() {
		mes := time.Now().In(gt).Format("2006-01")
		cs := must(t, a.do("GET", fmt.Sprintf("/servicios/%d/horus/consumo?mes=%s", s2.ID, mes), w.at, nil), 200)
		eq(t, "consumo mensual en Argos (bajada)", gs(cs.Body, "items.0.bajada_bytes"), fmt.Sprint(uint64(3*gb)))
		lot := must(t, a.do("POST", "/integraciones/horus/consumo", w.at, map[string]any{"servicio_ids": []int{s2.ID, s3.ID}, "mes": mes}), 200)
		eq(t, "consumo por lotes en Argos", gs(lot.Body, "items.0.bajada_bytes"), fmt.Sprint(uint64(3*gb)))
	}
}

func (w *world) js(t *testing.T) jetstream.JetStream {
	t.Helper()
	nc, err := nats.Connect(w.c.NATSURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

// publish publica un evento de dominio de Horus (lo que publicarían
// detection o snmp por su outbox) para que alerts lo convierta en webhook.
func (w *world) publish(t *testing.T, js jetstream.JetStream, typ, subject string, data map[string]any) {
	t.Helper()
	b, _ := json.Marshal(data)
	tid := uuid.MustParse(w.A.id)
	src := "horus/" + strings.Split(typ, ".")[1]
	if err := natsx.Publish(context.Background(), js, &natsx.Envelope{Type: typ, Source: src, Subject: subject, TenantID: &tid,
		AggregateType: "finding", AggregateVersion: 1, Data: b}); err != nil {
		t.Fatal(err)
	}
}

// received espera el webhook de Horus en Argos y devuelve su fila.
func (w *world) received(t *testing.T, event, contains string) map[string]any {
	t.Helper()
	var out map[string]any
	waitFor(t, "webhook "+event+" en Argos", 30*time.Second, func() (bool, string) {
		var id int64
		var body map[string]any
		var svc *int64
		var interno *string
		err := w.a.db.QueryRow(t.Context(), `SELECT id, cuerpo, servicio_id, evento_interno FROM integraciones.horus_recibidos
			WHERE evento = $1 AND cuerpo::text LIKE '%' || $2 || '%' ORDER BY id DESC LIMIT 1`, event, contains).Scan(&id, &body, &svc, &interno)
		if err != nil {
			return false, err.Error()
		}
		out = map[string]any{"cuerpo": body, "servicio_id": svc, "evento_interno": interno}
		if svc != nil {
			out["servicio_id"] = *svc
		}
		if interno != nil {
			out["evento_interno"] = *interno
		}
		return true, ""
	})
	return out
}

// outbox comprueba el evento interno de Argos (outbox) de un webhook de Horus.
func (w *world) outbox(t *testing.T, event, webhookID string) map[string]any {
	t.Helper()
	var payload map[string]any
	waitFor(t, "evento interno "+event, 10*time.Second, func() (bool, string) {
		err := w.a.db.QueryRow(t.Context(), `SELECT payload FROM audit.outbox WHERE evento = $1 AND payload->>'webhook_id' = $2 ORDER BY id DESC LIMIT 1`,
			event, webhookID).Scan(&payload)
		return err == nil, fmt.Sprint(err)
	})
	return payload
}

// webhooksToArgos: §3.4. El canal webhook de Horus entrega firmado al
// receptor de Argos, que lo traduce a eventos internos horus.* (nunca cambia
// el estado de un servicio).
func (w *world) webhooksToArgos(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	tok := w.A.admin.tok()
	cn := must(t, h.do("GET", "/integrations/argosisp", tok, nil), 200)
	w.channelID = gs(cn.Body, "connection.notification_channel_id")
	// Prueba de conexión del canal: event = "ping".
	pt := must(t, h.do("POST", "/notification-channels/"+w.channelID+"/connection-test", tok, nil), 200)
	eq(t, "prueba del canal webhook", gs(pt.Body, "ok"), "true")
	w.received(t, "ping", "")

	js := w.js(t)
	s3 := w.svc[3]
	cust := w.A.customersByIP[s3.IP]
	// Hallazgo real en detection (para resolverlo por la API) y su apertura en el bus.
	fid := uuid.Must(uuid.NewV7())
	w.findingID = fid.String()
	nowT := time.Now().UTC()
	exec1(t, h.db, `INSERT INTO detection.finding (id, tenant_id, state, kind, severity, confidence, customer_id, realm_id, site_id, router_id,
			address, target_type, target_value, signals, summary, reasons, evidence, window_from, window_to, first_seen_at, last_seen_at, opened_at, updated_at, rule_version)
		VALUES ($1, $2, 'open', 'outbound_scanning', 'high', 0.9, $3, $4, $5, $6, $7, 'remote_port', '23', '{scanning}',
			'{"code": "outbound_scanning_port", "text": "Escaneo del puerto 23 a 1240 destinos en 5 min"}',
			'[{"code": "syn_only_ratio_high", "detail": "96 %", "weight": 0.45, "data": {"syn_ratio": 0.96}}]',
			'{"destination_ports": [23]}', $8, $9, $8, $9, $9, $9, 'outbound-scan@1')`,
		fid, w.A.id, cust, w.A.realm, w.A.site, w.A.router, s3.IP, nowT.Add(-5*time.Minute), nowT)
	w.publish(t, js, "horus.detection.finding.opened", fid.String(), map[string]any{"id": fid.String(), "kind": "outbound_scanning", "severity": "high",
		"state": "open", "summary": map[string]any{"text": "Escaneo del puerto 23"}, "opened_at": nowT.Format(time.RFC3339),
		"window_from": nowT.Add(-5 * time.Minute).Format(time.RFC3339), "window_to": nowT.Format(time.RFC3339),
		"customer_id": cust, "site_id": w.A.site, "router_id": w.A.router, "realm_id": w.A.realm})
	op := w.received(t, "finding.opened", fid.String())
	eq(t, "finding.opened: servicio de Argos", op["servicio_id"], s3.ID)
	eq(t, "finding.opened: evento interno", op["evento_interno"], "horus.hallazgo_abierto")
	body := op["cuerpo"]
	eq(t, "finding.opened: external.service_id", gs(body, "data.external.service_id"), s3.ID)
	eq(t, "finding.opened: external.customer_id", gs(body, "data.external.customer_id"), s3.Cliente)
	eq(t, "finding.opened: external.company_id", gs(body, "data.external.company_id"), s3.Empresa)
	eq(t, "finding.opened: tenant", gs(body, "tenant"), w.A.slug)
	eq(t, "finding.opened: finding.kind", gs(body, "data.finding.kind"), "outbound_scanning")
	if gs(body, "data.customer.address") != "" || gs(body, "data.customer.alias") != "" {
		t.Fatalf("sin include_personal_data no viajan IP ni alias: %v", body)
	}
	ev := w.outbox(t, "horus.hallazgo_abierto", gs(body, "id"))
	eq(t, "horus.hallazgo_abierto.servicio_id", gs(ev, "servicio_id"), s3.ID)
	eq(t, "horus.hallazgo_abierto.hallazgo.severidad", gs(ev, "hallazgo.severidad"), "high")
	// Horus no decide cortes: el servicio sigue activo.
	eq(t, "estado del servicio tras el hallazgo", q1(t, a.db, `SELECT estado FROM core.servicios WHERE id = $1`, s3.ID), "activo")

	// finding.resolved por el flujo real: resolver en la API de Horus → outbox → bus → alerts → Argos.
	fr := must(t, h.do("GET", "/findings/"+fid.String(), tok, nil), 200)
	must(t, h.do("POST", "/findings/"+fid.String()+"/resolve", tok, map[string]any{"comment": "atendido en e2e"},
		map[string]string{"If-Match": fr.Header.Get("ETag")}), 200)
	rs := w.received(t, "finding.resolved", fid.String())
	eq(t, "finding.resolved: evento interno", rs["evento_interno"], "horus.hallazgo_resuelto")
	eq(t, "finding.resolved: servicio", rs["servicio_id"], s3.ID)

	// customer.security_state_changed → evento interno y último dato conocido de la columna.
	w.publish(t, js, "horus.detection.customer.security_state_changed", cust, map[string]any{"customer_id": cust, "state": "infected",
		"previous_state": "suspected", "open_findings": 2, "site_id": w.A.site})
	sc := w.received(t, "customer.security_state_changed", "infected")
	eq(t, "estado de seguridad: servicio", sc["servicio_id"], s3.ID)
	eq(t, "estado de seguridad guardado", q1(t, a.db, `SELECT datos->>'estado' FROM integraciones.horus_datos WHERE tipo = 'seguridad' AND clave = $1`,
		fmt.Sprintf("servicio:%d", s3.ID)), "infected")

	// router.status_changed e infra_service.state_changed.
	w.publish(t, js, "horus.snmp.router.state_changed", w.A.router, map[string]any{"router_id": w.A.router, "site_id": w.A.site,
		"state": "offline", "previous_state": "online", "reason": "tunnel_down"})
	rt := w.received(t, "router.status_changed", w.A.router)
	eq(t, "router.status_changed: nombre", gs(rt["cuerpo"], "data.router.name"), "rt-centro")
	eq(t, "router.status_changed: status", gs(rt["cuerpo"], "data.router.status"), "offline")
	rev := w.outbox(t, "horus.router_estado_cambiado", gs(rt["cuerpo"], "id"))
	eq(t, "horus.router_estado_cambiado.router.estado", gs(rev, "router.estado"), "offline")
	svcID := uuid.NewString()
	w.publish(t, js, "horus.snmp.service.state_changed", svcID, map[string]any{"service_id": svcID, "name": "IPTV", "site_id": w.A.site,
		"state": "down", "previous_state": "up", "reason": "icmp"})
	is := w.received(t, "infra_service.state_changed", svcID)
	eq(t, "infra_service.state_changed: name", gs(is["cuerpo"], "data.service.name"), "IPTV")
	eq(t, "infra_service.state_changed: evento interno", is["evento_interno"], "horus.servicio_infra_cambiado")

	// Receptor de Argos: firma, desfase y deduplicación con un secreto rotado (se copia a Horus).
	ver := a.version(w.at, "/integraciones/horus")
	cfgR := must(t, a.do("PUT", "/integraciones/horus", w.at, map[string]any{"version": ver, "regenerar_secreto_entrada": true}), 200)
	w.argosSecret = cfgR.str("webhook_entrada_secreto_nuevo")
	if len(w.argosSecret) < 16 {
		t.Fatalf("secreto nuevo de Argos = %s", cfgR.Raw)
	}
	must(t, h.do("PUT", "/notification-channels/"+w.channelID+"/credentials", w.A.admin.tok(), map[string]any{"webhook_secret": w.argosSecret}), 204)
	url := w.c.ArgosURL + "/api/v1/integraciones/horus/webhook"
	raw := []byte(fmt.Sprintf(`{"id":"e2e-hook-1","event":"router.status_changed","occurred_at":"%s","tenant":"%s","data":{"router":{"id":"%s","name":"rt-centro","status":"online","previous_status":"offline","reason":null}}}`,
		time.Now().UTC().Format(time.RFC3339), w.A.slug, w.A.router))
	post := func(hdr map[string]string) int {
		return do(t, rq{Method: "POST", URL: url, RawBody: raw, Header: hdr}).Status
	}
	eq(t, "Argos: firma inválida", post(signedHeaders("whsec_otro-secreto-e2e-00002", "e2e-hook-1", time.Now(), raw)), 401)
	eq(t, "Argos: desfase de 6 min", post(signedHeaders(w.argosSecret, "e2e-hook-1", time.Now().Add(6*time.Minute), raw)), 401)
	if st := post(signedHeaders(w.argosSecret, "e2e-hook-1", time.Now(), raw)); st/100 != 2 {
		t.Fatalf("Argos: firma válida → %d", st)
	}
	if st := post(signedHeaders(w.argosSecret, "e2e-hook-1", time.Now(), raw)); st/100 != 2 {
		t.Fatalf("Argos: duplicado → %d", st)
	}
	eq(t, "Argos deduplica por id", q1(t, a.db, `SELECT count(*) FROM integraciones.horus_recibidos WHERE webhook_id = 'e2e-hook-1'`), 1)
	// Con el secreto nuevo, Horus sigue entregando.
	w.publish(t, js, "horus.snmp.router.state_changed", w.A.router, map[string]any{"router_id": w.A.router, "site_id": w.A.site,
		"state": "online", "previous_state": "offline", "reason": ""})
	w.received(t, "router.status_changed", `"status": "online", "previous_status": "offline"`)
}

// cookieOf devuelve "nombre=valor" de una cookie de la respuesta.
func cookieOf(r resp, name string) string {
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == name {
			return c.Name + "=" + c.Value
		}
	}
	return ""
}

// ssoToArgos: §1.5 con Horus como origen ("Ver en ArgosISP").
func (w *world) ssoToArgos(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	s3, s4 := w.svc[3], w.svc[4]
	// Usuario de Argos con el MISMO correo que el de Horus: no se vincula solo por el correo.
	roles := must(t, a.do("GET", "/roles", w.at, nil), 200)
	var lectura float64
	var list []any
	_ = json.Unmarshal(roles.Raw, &list)
	for _, r := range list {
		if gs(r, "nombre") == "lectura" {
			lectura, _ = get(r, "id").(float64)
		}
	}
	if lectura == 0 {
		t.Fatalf("rol lectura: %s", roles.Raw)
	}
	email := "admin@" + w.A.slug + ".test"
	must(t, a.do("POST", "/usuarios", w.at, map[string]any{"nombre": "Ana Lectura", "email": email, "password": "clave-lectura-e2e-1",
		"roles": []any{map[string]any{"role_id": lectura, "alcance_tipo": "router", "alcance_id": s4.Router}}}), 201)

	open := func() string {
		r := must(t, h.do("POST", "/integrations/argosisp/sso/open", w.A.admin.tok(), map[string]any{"customer_id": w.A.customersByIP[s3.IP]}), 200)
		u := r.str("url")
		if !strings.HasPrefix(u, "http://127.0.0.1:13000/sso/entrar#") {
			t.Fatalf("URL de autologin de Argos = %s", r.Raw)
		}
		return u[strings.Index(u, "#")+1:]
	}
	origin := map[string]string{"Origin": "http://127.0.0.1:13000"}
	canjear := func(ticket string) resp {
		return a.do("POST", "/auth/sso/canjear", "", map[string]string{"ticket": ticket}, origin)
	}
	tk := open()
	var ext, correo, tipo, idExt string
	_ = a.db.QueryRow(t.Context(), `SELECT usuario_id_externo, coalesce(usuario_correo, ''), destino_tipo, destino_id_externo FROM core.sso_tickets ORDER BY id DESC LIMIT 1`).
		Scan(&ext, &correo, &tipo, &idExt)
	eq(t, "ticket: usuario externo", ext, w.A.adminID)
	eq(t, "ticket: correo", correo, email)
	eq(t, "ticket: destino", tipo+":"+idExt, fmt.Sprintf("servicio:%d", s3.ID))
	first := must(t, canjear(tk), 200)
	eq(t, "primer canje", first.str("resultado"), "vinculacion_requerida")
	eq(t, "primer canje: ruta", first.str("ruta"), fmt.Sprintf("/servicios/%d", s3.ID))
	if cookieOf(first, "argos_sesion") != "" {
		t.Fatal("sin vinculación no se abre sesión")
	}
	if r := canjear(tk); r.Status != 410 {
		t.Fatalf("ticket reutilizado: %d", r.Status)
	}
	// Credenciales malas no vinculan; las buenas sí.
	if r := a.do("POST", "/auth/sso/vincular", "", map[string]string{"token_vinculacion": gs(first.Body, "vinculacion.token"), "email": email, "password": "mala-clave-123"}, origin); r.Status != 401 {
		t.Fatalf("vincular con clave mala: %d", r.Status)
	}
	ln := must(t, a.do("POST", "/auth/sso/vincular", "", map[string]string{"token_vinculacion": gs(first.Body, "vinculacion.token"), "email": email, "password": "clave-lectura-e2e-1"}, origin), 200)
	eq(t, "vincular", ln.str("resultado"), "sesion")
	// Con vinculación: sesión del usuario local, con SUS permisos (lectura, solo el router de s4).
	in := must(t, canjear(open()), 200)
	eq(t, "canje vinculado", in.str("resultado"), "sesion")
	ck := cookieOf(in, "argos_sesion")
	if ck == "" {
		t.Fatalf("sin cookie de sesión: %v", in.Header)
	}
	asCookie := func(method, path string, body any) resp {
		hdr := map[string]string{"Cookie": ck, "Origin": "http://127.0.0.1:13000"}
		return a.do(method, path, "", body, hdr)
	}
	if r := asCookie("GET", fmt.Sprintf("/servicios/%d", s3.ID), nil); r.Status != 403 && r.Status != 404 {
		t.Fatalf("servicio fuera de su alcance: %d", r.Status)
	}
	must(t, asCookie("GET", fmt.Sprintf("/servicios/%d", s4.ID), nil), 200)
	if r := asCookie("POST", fmt.Sprintf("/servicios/%d/suspender", s4.ID), map[string]any{"motivo": "no autorizado"}); r.Status != 403 {
		t.Fatalf("lectura suspende un servicio: %d", r.Status)
	}
	// Tickets: solo el token del canje, nunca otro atk_; destino router/hallazgo (ids de Horus) no aplica en Argos.
	tb := map[string]any{"usuario": map[string]any{"id_externo": w.A.adminID, "correo": email, "nombre": "Ana"},
		"destino": map[string]any{"tipo": "servicio", "id_externo": fmt.Sprint(s3.ID)}}
	if r := a.do("POST", "/integraciones/horus/sso/tickets", w.atk, tb); r.Status != 403 {
		t.Errorf("ticket con un atk_ que no es el del canje: %d %s", r.Status, r.Raw)
	}
	if r := a.do("POST", "/integraciones/horus/sso/tickets", w.at, tb); r.Status != 403 {
		t.Errorf("ticket con la sesión de una persona: %d", r.Status)
	}
	// Auditoría en Argos: emisión, canje y vinculación.
	for _, acc := range []string{"sso.emitir", "auth.sso_canjear", "auth.sso_vincular"} {
		if n := q1(t, a.db, `SELECT count(*) FROM audit.acciones WHERE accion = $1`, acc); fmt.Sprint(n) == "0" {
			t.Errorf("%s sin auditar", acc)
		}
	}
	if n := q1(t, h.db, `SELECT count(*) FROM auth.audit_log WHERE action = 'integrations.argosisp.sso.opened'`); fmt.Sprint(n) == "0" {
		t.Error("apertura sin auditar en Horus")
	}
}

// ssoToHorus: §1.5 con Argos como origen ("Ver en Horus").
func (w *world) ssoToHorus(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	s3 := w.svc[3]
	analyst := h.createUser("analista@" + w.A.slug + ".test")
	h.grant(analyst, w.A.id, "analyst")
	// Un usuario de Horus con el correo del admin de Argos tampoco se vincula solo.
	same := h.createUser(w.c.ArgosAdmin)
	h.grant(same, w.A.id, "analyst")
	abrir := func(tipo, id string) string {
		r := must(t, a.do("POST", "/integraciones/horus/abrir", w.at, map[string]any{"destino": map[string]any{"tipo": tipo, "id_externo": id}}), 200)
		u := r.str("url")
		if !strings.HasPrefix(u, w.c.HorusURL+"/sso/entrar#") {
			t.Fatalf("URL de Horus = %s", r.Raw)
		}
		return u[strings.Index(u, "#")+1:]
	}
	redeem := func(ticket string) resp {
		return h.do("POST", "/integrations/argosisp/sso/redeem", "", map[string]string{"ticket": ticket},
			map[string]string{"X-Requested-With": "horus", "Origin": w.c.HorusURL})
	}
	tk := abrir("servicio", fmt.Sprint(s3.ID))
	first := must(t, redeem(tk), 200)
	eq(t, "primer canje en Horus", first.str("status"), "link_required")
	eq(t, "destino", gs(first.Body, "target.type")+":"+gs(first.Body, "target.external_id"), fmt.Sprintf("servicio:%d", s3.ID))
	eq(t, "usuario externo", gs(first.Body, "external_user.id"), "u-1")
	if r := redeem(tk); r.Status != 410 {
		t.Fatalf("ticket reutilizado: %d", r.Status)
	}
	// La vinculación exige iniciar sesión DESPUÉS del canje (resolución de segundos).
	time.Sleep(1100 * time.Millisecond)
	an := h.login("analista@"+w.A.slug+".test", horusUserPassword)
	must(t, h.do("POST", "/me/sso-links", an.session, map[string]string{"link_token": first.str("link_token")}), 201)
	in := must(t, redeem(abrir("servicio", fmt.Sprint(s3.ID))), 200)
	eq(t, "canje vinculado en Horus", in.str("status"), "authenticated")
	s := &hsession{h: h, session: in.str("access_token")}
	s.useTenant(w.A.id)
	res := must(t, h.do("POST", "/integrations/argosisp/sso/resolve", s.ttoken, map[string]any{"type": "servicio", "external_id": fmt.Sprint(s3.ID)}), 200)
	eq(t, "destino resuelto", res.str("kind")+":"+res.str("id"), "customer:"+w.A.customersByIP[s3.IP])
	cres := must(t, h.do("POST", "/integrations/argosisp/sso/resolve", s.ttoken, map[string]any{"type": "cliente", "external_id": fmt.Sprint(s3.Cliente)}), 200)
	eq(t, "destino cliente", cres.str("kind"), "customer")
	rr := must(t, h.do("POST", "/integrations/argosisp/sso/resolve", s.ttoken, map[string]any{"type": "router", "external_id": w.A.router}), 200)
	eq(t, "destino router", rr.str("kind"), "router")
	// El analista no tiene security.findings.read: el ticket no da permisos.
	if r := h.do("POST", "/integrations/argosisp/sso/resolve", s.ttoken, map[string]any{"type": "hallazgo", "external_id": w.findingID}); r.Status != 403 {
		t.Fatalf("hallazgo sin permiso: %d %s", r.Status, r.Raw)
	}
	// "Ver en Horus" de un hallazgo desde Argos (id de Horus recibido por webhook) para el admin.
	abrir("hallazgo", w.findingID)
	if n := q1(t, h.db, `SELECT count(*) FROM auth.audit_log WHERE action IN ('auth.sso.ticket_issued', 'auth.sso.redeemed', 'auth.sso.linked')`); fmt.Sprint(n) == "0" {
		t.Fatal("autologin sin auditar en Horus")
	}
	if n := q1(t, a.db, `SELECT count(*) FROM audit.acciones WHERE accion = 'horus.abrir'`); fmt.Sprint(n) == "0" {
		t.Fatal("Ver en Horus sin auditar en Argos")
	}
}

// degradation: §0. Argos caído → Horus sigue con la identidad stale; Horus
// caído → la ficha de Argos responde con el último dato "desactualizado" y
// los webhooks se reintentan.
func (w *world) degradation(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	s3 := w.svc[3]
	// Si el escenario falla a medias, los dos sistemas quedan levantados para los siguientes.
	down := map[string]bool{}
	stop := func(what string) { ctl(t, w.c, "stop", what); down[what] = true }
	start := func(what string) { ctl(t, w.c, "start", what); delete(down, what) }
	t.Cleanup(func() {
		for what := range down {
			ctl(t, w.c, "start", what)
		}
	})
	stop("argos")
	id := w.refresh(t, s3.IP)
	eq(t, "identidad conservada sin Argos", gs(id.Body, "current.service_id"), s3.ID)
	eq(t, "identidad marcada stale", gs(id.Body, "current.stale"), "true")
	lk := must(t, h.do("POST", "/integrations/ip-lookup", w.hfPat, map[string]any{"address": s3.IP}), 200)
	eq(t, "360 de Horus sin Argos: external.stale", gs(lk.Body, "external.stale"), "true")
	cn := must(t, h.do("GET", "/integrations/argosisp", w.A.admin.tok(), nil), 200)
	eq(t, "estado del conector", cn.Body["last_sync_status"], "failing")
	must(t, do(t, rq{Method: "GET", URL: strings.Replace(w.c.HorusURL, "18080", "18081", 1) + "/readyz"}), 200)
	start("argos")
	id = w.refresh(t, s3.IP)
	eq(t, "identidad fresca al volver Argos", gs(id.Body, "current.stale"), "false")

	// Horus caído: la ficha ya tenía un dato de Horus.
	f0 := must(t, a.do("GET", fmt.Sprintf("/servicios/%d/horus", s3.ID), w.at, nil), 200)
	if f0.Body["stale"] != false || f0.Body["datos"] == nil {
		t.Fatalf("ficha con Horus = %s", f0.Raw)
	}
	stop("horus")
	f := must(t, a.do("GET", fmt.Sprintf("/servicios/%d/horus", s3.ID), w.at, nil), 200)
	eq(t, "ficha sin Horus: stale (desactualizado)", f.Body["stale"], true)
	if f.str("error") == "" || f.str("actualizado_en") == "" || f.Body["datos"] == nil {
		t.Fatalf("ficha sin Horus = %s", f.Raw)
	}
	must(t, a.do("GET", fmt.Sprintf("/servicios/%d", s3.ID), w.at, nil), 200)
	n0 := w.sentCount(t, "cliente.actualizado")
	ver := a.version(w.at, fmt.Sprintf("/clientes/%d", s3.Cliente))
	must(t, a.do("PUT", fmt.Sprintf("/clientes/%d", s3.Cliente), w.at, map[string]any{"version": ver, "nombre": "Distribuidora sin Horus"}), 200)
	waitFor(t, "envío pendiente con error", 20*time.Second, func() (bool, string) {
		n, _ := q1(t, a.db, `SELECT count(*) FROM integraciones.horus_envios WHERE evento = 'cliente.actualizado' AND estado = 'pendiente' AND intentos >= 1`).(int64)
		return n > 0, ""
	})
	start("horus")
	// Reintento a 1 min (§1.3): al volver Horus el envío llega y Horus re-resuelve.
	waitFor(t, "reintento del webhook", 100*time.Second, func() (bool, string) {
		return w.sentCount(t, "cliente.actualizado") > n0, ""
	})
	waitFor(t, "nombre nuevo tras el reintento", 30*time.Second, func() (bool, string) {
		id := w.identity(t, s3.IP)
		return gs(id.Body, "current.display_name") == "Distribuidora sin Horus", gs(id.Body, "current")
	})
	// La ficha vuelve a estar al día cuando vence la espera exponencial de Argos.
	waitFor(t, "ficha al día", 60*time.Second, func() (bool, string) {
		f := a.do("GET", fmt.Sprintf("/servicios/%d/horus", s3.ID), w.at, nil)
		return f.Body["stale"] == false && f.Body["datos"] != nil, string(f.Raw)[:min(200, len(f.Raw))]
	})
}

// isolation: datos personales solo con include_personal_data; secretos
// enmascarados; un token del ISP B no lee nada de A.
func (w *world) isolation(t *testing.T) {
	h, a := w.h, w.a
	h.t, a.t = t, t
	s3 := w.svc[3]
	tok := w.A.admin.tok()
	ch := must(t, h.do("GET", "/notification-channels/"+w.channelID, tok, nil), 200)
	must(t, h.do("PATCH", "/notification-channels/"+w.channelID, tok, map[string]any{"include_personal_data": true},
		map[string]string{"If-Match": ch.Header.Get("ETag")}), 200)
	js := w.js(t)
	fid := uuid.NewString()
	cust := w.A.customersByIP[s3.IP]
	w.publish(t, js, "horus.detection.finding.opened", fid, map[string]any{"id": fid, "kind": "spam_smtp", "severity": "medium", "state": "open",
		"summary": map[string]any{"text": "Envío SMTP masivo"}, "opened_at": time.Now().UTC().Format(time.RFC3339), "customer_id": cust, "site_id": w.A.site})
	op := w.received(t, "finding.opened", fid)
	eq(t, "con include_personal_data: address", gs(op["cuerpo"], "data.customer.address"), s3.IP)
	if gs(op["cuerpo"], "data.customer.alias") == "" {
		t.Fatalf("con include_personal_data: alias vacío: %v", op["cuerpo"])
	}
	// Secretos: nunca en claro en las bitácoras ni en las vistas.
	// (Horus guarda solo el prefijo de 4 caracteres de un token para identificarlo.)
	secretRe := `(hf_pat_|atk_)[A-Za-z0-9_-]{12,}|whsec_[A-Za-z0-9_-]{12,}`
	if n := q1(t, a.db, `SELECT count(*) FROM audit.cambios WHERE coalesce(antes::text, '') || coalesce(despues::text, '') ~ $1`, secretRe); fmt.Sprint(n) != "0" {
		t.Fatalf("secretos en claro en audit.cambios de Argos: %v", n)
	}
	if n := q1(t, a.db, `SELECT count(*) FROM audit.acciones WHERE coalesce(entrada::text, '') || coalesce(resultado::text, '') ~ $1`, secretRe); fmt.Sprint(n) != "0" {
		t.Fatalf("secretos en claro en audit.acciones de Argos: %v", n)
	}
	if n := q1(t, h.db, `SELECT count(*) FROM auth.audit_log WHERE changes::text ~ $1`, secretRe); fmt.Sprint(n) != "0" {
		t.Fatalf("secretos en claro en la bitácora de Horus: %v", n)
	}
	v := must(t, a.do("GET", "/integraciones/horus", w.at, nil), 200)
	if strings.Contains(string(v.Raw), "hf_pat_") || strings.Contains(string(v.Raw), "whsec_") {
		t.Fatalf("vista del conector con secretos: %s", v.Raw)
	}
	hv := must(t, h.do("GET", "/integrations/argosisp", tok, nil), 200)
	if strings.Contains(string(hv.Raw), "atk_") {
		t.Fatalf("vista del conector de Horus con el atk_: %s", hv.Raw)
	}

	// Aislamiento: el hf_pat_ del ISP B no ve clientes, servicios ni hallazgos de A.
	if r := h.do("POST", "/integrations/ip-lookup", w.hfPatB, map[string]any{"address": s3.IP}); r.Status != 404 {
		t.Fatalf("B lee la IP de un cliente de A: %d %s", r.Status, r.Raw)
	}
	if r := h.do("POST", "/integrations/ip-lookup", w.hfPatB, map[string]any{"address": w.oldIP2}); r.Status != 200 || get(r.Body, "external") != nil {
		t.Fatalf("B ve la identidad de A en su cliente con la misma IP: %d %s", r.Status, r.Raw)
	}
	if r := h.do("POST", "/integrations/ip-lookup", w.hfPatB, map[string]any{"external_service_id": s3.ID}); r.Status != 404 && r.Status != 422 {
		t.Fatalf("B lee un servicio de A: %d %s", r.Status, r.Raw)
	}
	if r := h.do("POST", "/integrations/security-states", w.hfPatB, map[string]any{"items": []any{map[string]any{"address": s3.IP}}}); r.Status != 200 || gs(r.Body, "items.0.error") != "not_found" {
		t.Fatalf("B lee el estado de seguridad de A: %d %s", r.Status, r.Raw)
	}
	if r := h.do("GET", "/findings/"+w.findingID, w.hfPatB, nil); r.Status != 404 {
		t.Fatalf("B lee un hallazgo de A: %d", r.Status)
	}
	if r := h.do("POST", "/integrations/argosisp/sso/tickets", w.hfPatB, map[string]any{"usuario": map[string]any{"id_externo": "u-1"},
		"destino": map[string]any{"tipo": "servicio", "id_externo": fmt.Sprint(s3.ID)}}); r.Status != 409 && r.Status != 403 {
		t.Fatalf("B emite tickets: %d", r.Status)
	}
	// En Argos: un atk_ limitado a un router no resuelve IPs de otro router.
	sc := must(t, a.do("POST", "/integraciones/horus/tokens", w.at, map[string]any{"nombre": "E2E router 2", "routers": []int{w.svc[4].Router}}), 201)
	r := must(t, a.do("POST", "/integraciones/horus/resolver", sc.str("token"), map[string]any{"ips": []string{s3.IP, w.svc[4].IP}}), 200)
	eq(t, "atk_ con alcance: IP de otro router", gs(r.Body, "resultados.0.error"), "no_encontrada")
	eq(t, "atk_ con alcance: IP de su router", gs(r.Body, "resultados.1.servicio_id"), w.svc[4].ID)
}

// discover publica `horus.flows.client.first_seen` como el ingester al ver
// por primera vez una IP de un prefijo de clientes.
func (w *world) discover(t *testing.T, ip string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	data, _ := json.Marshal(map[string]any{"batch_id": uuid.NewString(), "realm_id": w.A.realm, "router_id": w.A.router,
		"window_from": now, "window_to": now,
		"clients": []any{map[string]any{"address": ip, "client_prefix_id": w.A.clientPrefix, "first_seen": now}}})
	tid := uuid.MustParse(w.A.id)
	if err := natsx.Publish(context.Background(), w.js(t), &natsx.Envelope{Type: "horus.flows.client.first_seen", Source: "horus/flows",
		Subject: w.A.realm, TenantID: &tid, AggregateType: "realm", AggregateVersion: 1, Data: data}); err != nil {
		t.Fatal(err)
	}
}
