//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
)

type fakeRemote struct {
	mu       sync.Mutex
	messages []string
	auth     []string
}

func (f *fakeRemote) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("X-Auth-Token"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"horus_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"), r.URL.Path == "/api/v0/eventlog":
			f.messages = append(f.messages, r.URL.Path+" "+string(body))
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.URL.Path == "/api/v0/system":
			if r.Header.Get("X-Auth-Token") != "token-librenms-1234567" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"status":"ok","system":[{"local_ver":"24.9.0"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeRemote) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.messages {
		if strings.HasPrefix(m, prefix) {
			n++
		}
	}
	return n
}

func publishFindingFull(t *testing.T, js jetstream.JetStream, tenant, severity string, eventID uuid.UUID) {
	t.Helper()
	tid := uuid.MustParse(tenant)
	fid := uuid.Must(uuid.NewV7())
	data, _ := json.Marshal(map[string]any{"id": fid, "kind": "outbound_scanning", "severity": severity, "state": "open",
		"confidence_level": "high", "site_id": uuid.NewString(), "summary": map[string]any{"text": "Escaneo del puerto 23 a 1 240 destinos en 5 min"}})
	env := natsx.Envelope{ID: eventID, Type: "horus.detection.finding.opened", Source: "horus/detection", Subject: fid.String(), TenantID: &tid,
		AggregateType: "finding", AggregateVersion: 1, Data: data}
	if err := natsx.Publish(context.Background(), js, &env); err != nil {
		t.Fatal(err)
	}
}

// D13/D17: canales por ISP (Telegram y LibreNMS por API), credenciales
// write-only, prueba de conexión, envío de prueba, notificación de hallazgos
// abiertos sin IP de cliente, idempotencia por evento y registro de entregas.
func TestAlertsMinimalChannel(t *testing.T) {
	t.Parallel()
	remote := &fakeRemote{}
	srv := remote.server(t)
	a, js := startAppNATS(t, "HORUS_TELEGRAM_API_URL="+srv.URL)
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")

	tg := a.must(a.post(A.token, "/api/v1/notification-channels", map[string]any{"name": "NOC Telegram", "kind": "telegram",
		"config": map[string]any{"chat_id": "-100123"}, "subscription": map[string]any{"event_types": []string{"finding_opened"}, "min_severity": "high"}}), 201)
	if tg.str("status") != "unverified" || tg.Body["has_credentials"] != false {
		t.Fatalf("canal = %s", tg.Raw)
	}
	tgID := tg.str("id")
	if r := a.post(A.token, "/api/v1/notification-channels", map[string]any{"name": "x", "kind": "whatsapp", "config": map[string]any{},
		"subscription": map[string]any{"event_types": []string{"finding_opened"}}}); r.Status != 422 || r.code() != "NOTIFICATION_CHANNEL_KIND_NOT_AVAILABLE" {
		t.Fatalf("kind no disponible: %d %s", r.Status, r.Raw)
	}
	// Bot propio: write-only.
	a.must(a.do(req{Method: "PUT", Path: "/api/v1/notification-channels/" + tgID + "/credentials", Token: A.token,
		Body: map[string]any{"telegram_bot_token": "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef0123"}}), 204)
	got := a.must(a.do(req{Method: "GET", Path: "/api/v1/notification-channels/" + tgID, Token: A.token}), 200)
	if got.Body["has_credentials"] != true || strings.Contains(string(got.Raw), "ABCDEFGHIJ") {
		t.Fatalf("secreto expuesto o sin marcar: %s", got.Raw)
	}
	if a.auditCount("alerts.channel.credentials.updated") != 1 {
		t.Fatal("credenciales no auditadas")
	}
	ct := a.must(a.post(A.token, "/api/v1/notification-channels/"+tgID+"/connection-test", nil), 200)
	if ct.Body["ok"] != true {
		t.Fatalf("connection-test = %s", ct.Raw)
	}
	if s := a.must(a.do(req{Method: "GET", Path: "/api/v1/notification-channels/" + tgID, Token: A.token}), 200).str("status"); s != "ok" {
		t.Fatalf("estado tras la prueba = %s", s)
	}

	// LibreNMS por API (D17): token write-only y prueba de conexión.
	ln := a.must(a.post(A.token, "/api/v1/notification-channels", map[string]any{"name": "LibreNMS", "kind": "librenms",
		"config": map[string]any{"base_url": srv.URL, "username": "horus"}, "subscription": map[string]any{"event_types": []string{"finding_opened"}}}), 201)
	lnID := ln.str("id")
	if r := a.post(A.token, "/api/v1/notification-channels/"+lnID+"/connection-test", nil); r.Body["error_code"] != "missing_credentials" {
		t.Fatalf("sin credenciales: %s", r.Raw)
	}
	a.must(a.do(req{Method: "PUT", Path: "/api/v1/notification-channels/" + lnID + "/credentials", Token: A.token,
		Body: map[string]any{"api_token": "token-librenms-1234567"}}), 204)
	lt := a.must(a.post(A.token, "/api/v1/notification-channels/"+lnID+"/connection-test", nil), 200)
	if lt.Body["ok"] != true || lt.Body["remote_version"] != "24.9.0" {
		t.Fatalf("librenms connection-test = %s", lt.Raw)
	}
	// Un ISP B con la misma instancia no comparte nada.
	if r := a.do(req{Method: "GET", Path: "/api/v1/notification-channels/" + lnID, Token: B.token}); r.Status != 404 {
		t.Fatalf("canal de A visto por B: %d", r.Status)
	}

	// Envío de prueba (202, asíncrono).
	tr := a.must(a.do(req{Method: "POST", Path: "/api/v1/notification-channels/" + tgID + "/test", Token: A.token,
		Header: map[string]string{"Idempotency-Key": uuid.NewString()}}), 202)
	if tr.Body["is_test"] != true || tr.str("status") != "queued" {
		t.Fatalf("test = %s", tr.Raw)
	}
	a.waitFor("prueba enviada", func() bool { return remote.count("/bot") >= 1 })

	// Hallazgo abierto (alta → ambos canales; media → solo LibreNMS); evento repetido → sin duplicar.
	time.Sleep(time.Second) // durables deliver-new creados
	ev := uuid.Must(uuid.NewV7())
	publishFindingFull(t, js, A.id, "high", ev)
	publishFindingFull(t, js, A.id, "high", ev) // reentrega (mismo id)
	publishFindingFull(t, js, A.id, "medium", uuid.Must(uuid.NewV7()))
	publishFindingFull(t, js, B.id, "critical", uuid.Must(uuid.NewV7()))
	a.waitFor("notificaciones", func() bool { return remote.count("/bot") == 2 && remote.count("/api/v0/eventlog") == 2 })
	time.Sleep(500 * time.Millisecond)
	if remote.count("/bot") != 2 || remote.count("/api/v0/eventlog") != 2 {
		t.Fatalf("envíos telegram=%d librenms=%d", remote.count("/bot"), remote.count("/api/v0/eventlog"))
	}
	remote.mu.Lock()
	for _, m := range remote.messages {
		if strings.Contains(m, "10.") {
			t.Errorf("mensaje con IP: %s", m)
		}
	}
	remote.mu.Unlock()
	dl := a.must(a.do(req{Method: "GET", Path: "/api/v1/notification-deliveries?status=sent", Token: A.token}), 200)
	if n := len(dl.Body["data"].([]any)); n != 4 {
		t.Fatalf("entregas = %d: %s", n, dl.Raw)
	}
	byKind := a.must(a.do(req{Method: "GET", Path: "/api/v1/notification-deliveries?channel_kind=librenms", Token: A.token}), 200)
	if len(byKind.Body["data"].([]any)) != 2 {
		t.Fatalf("entregas librenms = %s", byKind.Raw)
	}
	if b := a.must(a.do(req{Method: "GET", Path: "/api/v1/notification-deliveries", Token: B.token}), 200); len(b.Body["data"].([]any)) != 0 {
		t.Fatalf("B ve entregas: %s", b.Raw)
	}
	var sent int
	_ = a.admin.QueryRow(context.Background(), `SELECT count(*) FROM alerts.outbox WHERE payload->>'type' = 'horus.alerts.notification.sent'`).Scan(&sent)
	if sent != 4 { // prueba + alta (telegram) + alta y media (librenms)
		t.Fatalf("notification.sent = %d", sent)
	}
}
