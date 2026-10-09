package notify

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRenderWithoutCustomerIP(t *testing.T) {
	s := &Service{baseURL: "https://horus.example.net"}
	data := map[string]any{"id": "f-1", "kind": "outbound_scanning", "severity": "high", "confidence_level": "high",
		"summary": map[string]any{"text": "Escaneo del puerto 23 a 1 240 destinos en 5 min"}, "address": "10.20.0.41"}
	m, sev := s.render(mapEvent("horus.detection.finding.opened", data), data)
	if sev != "high" || !strings.Contains(m.Subject, "señales compatibles") || m.Link != "https://horus.example.net/security/findings/f-1" {
		t.Fatalf("mensaje = %+v", m)
	}
	if strings.Contains(m.Subject+m.Text, "10.20.0.41") {
		t.Fatal("el mensaje lleva la IP del cliente")
	}
	if mapEvent("horus.detection.finding.opened", map[string]any{"previous_finding_id": "x"}) != eventFindingReopened ||
		mapEvent("horus.wireguard.peer.handshake_stale", nil) != eventTunnelDown || mapEvent("horus.devices.site.created", nil) != "" {
		t.Fatal("mapEvent")
	}
}

func TestMatches(t *testing.T) {
	med := "medium"
	site := uuid.New()
	c := &Channel{Enabled: true, Subscription: Subscription{EventTypes: []string{eventFindingOpened}, MinSeverity: &med, SiteIDs: []uuid.UUID{site}}}
	other := uuid.New()
	for _, tc := range []struct {
		ev, sev string
		site    *uuid.UUID
		want    bool
	}{
		{eventFindingOpened, "high", &site, true},
		{eventFindingOpened, "low", &site, false},
		{eventFindingOpened, "high", &other, false},
		{eventExporterSilent, "high", &site, false},
	} {
		if got := matches(c, tc.ev, tc.sev, tc.site); got != tc.want {
			t.Errorf("%s %s: %v", tc.ev, tc.sev, got)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	var f fieldErrs
	normalizeConfig(KindLibreNMS, json.RawMessage(`{"base_url":"https://user:pw@nms.example","username":"horus"}`), &f)
	normalizeConfig(KindTelegram, json.RawMessage(`{"chat_id":"no válido"}`), &f)
	normalizeConfig(KindEmail, json.RawMessage(`{"recipients":["noc@isp.example","x"]}`), &f)
	if len(f) != 3 {
		t.Fatalf("errores = %v", f)
	}
	f = nil
	out := normalizeConfig(KindLibreNMS, json.RawMessage(`{"base_url":"https://nms.example/","username":"horus"}`), &f)
	if len(f) != 0 || !strings.Contains(string(out), `"tls_verify":true`) || !strings.Contains(string(out), `"https://nms.example"`) {
		t.Fatalf("config = %s %v", out, f)
	}
}

func TestLibreNMSAndTelegramSenders(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("X-Auth-Token"))
		switch {
		case r.URL.Path == "/api/v0/system" && r.Header.Get("X-Auth-Token") == "token-de-api-largo-1234":
			_, _ = w.Write([]byte(`{"status":"ok","system":[{"local_ver":"24.9.0"}]}`))
		case r.URL.Path == "/api/v0/system":
			w.WriteHeader(http.StatusUnauthorized)
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	s := &Senders{TelegramAPI: srv.URL}
	cfg := LibreNMSConfig{BaseURL: srv.URL, Username: "horus", TimeoutSeconds: 5}
	if r := s.testLibreNMS(context.Background(), cfg, Credentials{APIToken: "token-de-api-largo-1234"}); !r.OK || *r.RemoteVersion != "24.9.0" {
		t.Fatalf("librenms ok = %+v", r)
	}
	if r := s.testLibreNMS(context.Background(), cfg, Credentials{APIToken: "otro-token-incorrecto-x"}); r.OK || *r.ErrorCode != "auth_failed" {
		t.Fatalf("librenms auth = %+v", r)
	}
	if r := s.testLibreNMS(context.Background(), cfg, Credentials{}); *r.ErrorCode != "missing_credentials" {
		t.Fatalf("librenms sin credenciales = %+v", r)
	}
	if err := s.sendLibreNMS(context.Background(), cfg, Credentials{APIToken: "token-de-api-largo-1234"}, Message{Subject: "x"}, "finding_opened", "high"); err != nil {
		t.Fatal(err)
	}
	if r := s.testTelegram(context.Background(), Credentials{TelegramBotToken: "123:abc"}); !r.OK {
		t.Fatalf("telegram = %+v", r)
	}
	if r := s.testTelegram(context.Background(), Credentials{}); *r.ErrorCode != "missing_credentials" {
		t.Fatalf("telegram sin token = %+v", r)
	}
	// Destino caído: connect_failed (sin cuerpo remoto ni secretos).
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_ = l.Close()
	if r := s.testLibreNMS(context.Background(), LibreNMSConfig{BaseURL: "http://" + addr, Username: "h", TimeoutSeconds: 2}, Credentials{Password: "x"}); *r.ErrorCode != "connect_failed" {
		t.Fatalf("caído = %+v", r)
	}
	s.SMTP = SMTPConfig{Host: "127.0.0.1", Port: 1}
	if r := s.testEmail(context.Background()); r.OK {
		t.Fatal("SMTP inexistente aceptado")
	}
}
