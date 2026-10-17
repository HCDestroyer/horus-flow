package notify

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/packages/go/webhooksig"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
)

type fakeDirectory struct{ ref *devapi.CustomerRef }

func (f fakeDirectory) CustomerRef(context.Context, uuid.UUID, uuid.UUID) (*devapi.CustomerRef, error) {
	return f.ref, nil
}

func TestWebhookRetrySchedule(t *testing.T) {
	t.Parallel()
	want := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour}
	if len(WebhookBackoff) != len(want) || WebhookMaxAttempts != 6 {
		t.Fatalf("reintentos = %v (%d intentos)", WebhookBackoff, WebhookMaxAttempts)
	}
	for i, w := range want {
		if WebhookBackoff[i] != w {
			t.Errorf("reintento %d = %v, quiero %v", i+1, WebhookBackoff[i], w)
		}
	}
}

func TestContractEvents(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		eventFindingOpened: "finding.opened", eventFindingReopened: "finding.opened", eventFindingResolved: "finding.resolved",
		eventCustomerSecurity: "customer.security_state_changed", eventRouterOffline: "router.status_changed",
		eventRouterRecovered: "router.status_changed", eventServiceDown: "infra_service.state_changed", eventExporterSilent: "",
		eventTunnelDown: "", eventRouterInterfaceDown: "",
	} {
		if got := contractEvent(in); got != want {
			t.Errorf("%s → %q, quiero %q", in, got, want)
		}
	}
	if mapEvent("horus.detection.finding.resolved", map[string]any{}) != eventFindingResolved {
		t.Error("finding.resolved")
	}
	if mapEvent("horus.detection.customer.security_state_changed", map[string]any{"state": "suspected", "previous_state": "clean"}) != eventCustomerSecurity {
		t.Error("security_state_changed")
	}
}

func TestWebhookDataWithExternalIDs(t *testing.T) {
	t.Parallel()
	svc, sid := int64(812), int64(455)
	alias := "Ferretería López"
	s := &Service{}
	s.SetWebhook(func() (devapi.CustomerDirectory, bool) {
		return fakeDirectory{ref: &devapi.CustomerRef{ID: uuid.New(), Address: "100.64.1.20", Alias: &alias,
			External: &devapi.ExternalIDs{System: "argosisp", CustomerID: &sid, ServiceID: &svc}}}, true
	}, false)
	data := map[string]any{"id": "f1", "kind": "outbound_scanning", "severity": "high", "state": "open", "customer_id": uuid.NewString(),
		"summary": map[string]any{"text": "Escaneo"}, "window_from": "2026-10-17T10:00:00.000Z", "window_to": "2026-10-17T10:05:00.000Z"}
	tenant := uuid.NewString()
	d := s.webhookData(context.Background(), tenant, eventFindingOpened, data, renderCtx{}, false)
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "100.64.1.20") || strings.Contains(string(b), "Ferretería") {
		t.Fatalf("sin include_personal_data no van IP ni alias: %s", b)
	}
	ext := d["external"].(map[string]any)
	if *ext["service_id"].(*int64) != 812 || *ext["customer_id"].(*int64) != 455 {
		t.Fatalf("external = %v", ext)
	}
	d = s.webhookData(context.Background(), tenant, eventFindingOpened, data, renderCtx{}, true)
	if d["customer"].(map[string]any)["address"] != "100.64.1.20" {
		t.Fatalf("con include_personal_data: %v", d["customer"])
	}
	r := s.webhookData(context.Background(), tenant, eventRouterOffline, map[string]any{"router_id": "r1", "state": "offline",
		"previous_state": "online", "reason": "tunnel_down"}, renderCtx{routerName: "rt-centro"}, false)
	if r["router"].(map[string]any)["name"] != "rt-centro" || r["router"].(map[string]any)["status"] != "offline" {
		t.Fatalf("router = %v", r)
	}
}

// El receptor verifica la firma del contrato (§1.3) con el mismo secreto.
func TestSendWebhookSigned(t *testing.T) {
	t.Parallel()
	secret := "whsec_secreto_de_prueba_1234"
	got := make(chan error, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		id, err := webhooksig.Verify(r.Header, body, time.Now(), []byte(secret))
		if err == nil && id != "d-1" {
			err = io.ErrUnexpectedEOF
		}
		got <- err
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	s := &Senders{HTTPClient: func(bool, time.Duration) *http.Client { return srv.Client() },
		WebhookGuard: &datasets.EgressGuard{AllowPrivate: true}}
	body := webhookBody("d-1", "finding.opened", time.Now(), "isp-a", map[string]any{"x": 1})
	if err := s.sendWebhook(context.Background(), WebhookConfig{URL: srv.URL, TimeoutSeconds: 5}, Credentials{WebhookSecret: secret}, "d-1", body, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := <-got; err != nil {
		t.Fatalf("firma: %v", err)
	}
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	for _, k := range []string{"id", "event", "occurred_at", "tenant", "data"} {
		if _, ok := parsed[k]; !ok {
			t.Errorf("falta %s en el cuerpo", k)
		}
	}
	// Sin secreto no se envía.
	if err := s.sendWebhook(context.Background(), WebhookConfig{URL: srv.URL}, Credentials{}, "d-2", body, time.Now()); err == nil {
		t.Fatal("sin secreto debe fallar")
	}
	// 500 del receptor → error reintentable.
	bad := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	s.HTTPClient = func(bool, time.Duration) *http.Client { return bad.Client() }
	if err := s.sendWebhook(context.Background(), WebhookConfig{URL: bad.URL}, Credentials{WebhookSecret: secret}, "d-3", body, time.Now()); err == nil {
		t.Fatal("HTTP 500 debe fallar")
	}
}

// SSRF: sin el modo laboratorio, loopback y redes privadas se rechazan.
func TestWebhookEgressGuard(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	s := &Senders{HTTPClient: func(bool, time.Duration) *http.Client {
		return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // test
	}, WebhookGuard: &datasets.EgressGuard{}}
	err := s.sendWebhook(context.Background(), WebhookConfig{URL: srv.URL}, Credentials{WebhookSecret: "whsec_1234567890abcdef"}, "d", []byte("{}"), time.Now())
	if err == nil || !strings.Contains(err.Error(), "no permitido") {
		t.Fatalf("loopback debe rechazarse: %v", err)
	}
	r := s.testWebhook(context.Background(), WebhookConfig{URL: srv.URL}, Credentials{WebhookSecret: "whsec_1234567890abcdef"}, "isp")
	if r.OK || r.ErrorCode == nil || *r.ErrorCode != "not_allowed" {
		t.Fatalf("prueba de conexión = %+v", r)
	}
	var f fieldErrs
	normalizeWebhook(json.RawMessage(`{"url":"http://argos.isp/x"}`), &f, false)
	if len(f) == 0 {
		t.Fatal("http sin modo laboratorio")
	}
	f = nil
	if out := normalizeWebhook(json.RawMessage(`{"url":"https://argos.isp/api/v1/integraciones/horus/webhook"}`), &f, false); len(f) != 0 ||
		!strings.Contains(string(out), `"timeout_seconds":10`) {
		t.Fatalf("válida: %s %v", out, f)
	}
}
