//go:build integration

package tenancy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/webhooksig"
)

// H5: canal webhook (alta, secreto write-only, prueba de conexión y envío de
// prueba firmado según el contrato §1.3).
func TestWebhookChannel(t *testing.T) {
	t.Parallel()
	a := startAppEnv(t, "HORUS_ALERTS_WEBHOOK_ALLOW_PRIVATE=true", "HORUS_ALERTS_DISPATCH_INTERVAL=200ms")
	pt := a.superadmin().platformToken()
	A := a.newISP(pt, "hook-a")
	secret := "whsec_secreto_del_receptor_42"
	got := make(chan map[string]any, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if _, err := webhooksig.Verify(r.Header, body, time.Now(), []byte(secret)); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		m["_id_header"] = r.Header.Get(webhooksig.HeaderID)
		got <- m
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	ch := a.must(a.post(A.token, "/api/v1/notification-channels", map[string]any{"name": "ArgosISP", "kind": "webhook",
		"config": map[string]any{"url": srv.URL + "/api/v1/integraciones/horus/webhook"},
		"subscription": map[string]any{"event_types": []string{"finding_resolved", "customer_security_state_changed"},
			"categories": []string{"security", "routers", "infrastructure"}}}), 201)
	id := ch.str("id")
	// Sin secreto la prueba de conexión lo dice.
	r := a.must(a.post(A.token, "/api/v1/notification-channels/"+id+"/connection-test", nil), 200)
	if r.Body["ok"] != false || r.Body["error_code"] != "missing_credentials" {
		t.Fatalf("prueba sin secreto = %s", r.Raw)
	}
	a.must(a.do(req{Method: "PUT", Path: "/api/v1/notification-channels/" + id + "/credentials", Token: A.token,
		Body: map[string]any{"webhook_secret": secret}}), 204)
	got2 := a.must(a.do(req{Method: "GET", Path: "/api/v1/notification-channels/" + id, Token: A.token}), 200)
	if got2.Body["has_credentials"] != true || strings.Contains(string(got2.Raw), secret) {
		t.Fatalf("canal = %s", got2.Raw)
	}
	r = a.must(a.post(A.token, "/api/v1/notification-channels/"+id+"/connection-test", nil), 200)
	if r.Body["ok"] != true {
		t.Fatalf("prueba de conexión = %s", r.Raw)
	}
	<-got // ping de la prueba de conexión
	a.must(a.do(req{Method: "POST", Path: "/api/v1/notification-channels/" + id + "/test", Token: A.token,
		Header: map[string]string{"Idempotency-Key": uuid.NewString()}}), 202)
	select {
	case m := <-got:
		if m["event"] != "test" || m["id"] != m["_id_header"] || m["occurred_at"] == nil {
			t.Fatalf("envío de prueba = %v", m)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("el envío de prueba no llegó")
	}
	// Un webhook con secreto de otro kind se rechaza.
	if r := a.do(req{Method: "PUT", Path: "/api/v1/notification-channels/" + id + "/credentials", Token: A.token,
		Body: map[string]any{"password": "x"}}); r.Status != 422 {
		t.Fatalf("credencial ajena = %d %s", r.Status, r.Raw)
	}
}
