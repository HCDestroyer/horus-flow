package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testToken = "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef0123"

func TestMapSNMPEvents(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		data map[string]any
		want string
	}{
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": "online", "state": "offline", "reason": "tunnel_down"}, eventRouterOffline},
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": "offline", "state": "online", "reason": "ok"}, eventRouterRecovered},
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": "offline", "state": "degraded", "reason": "snmp_unreachable"}, eventRouterRecovered},
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": nil, "state": "online", "reason": "ok"}, ""},
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": "online", "state": "warning", "reason": "cpu_high"}, eventRouterCPUHigh},
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": "warning", "state": "critical", "reason": "memory_high"}, eventRouterMemoryHigh},
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": "critical", "state": "warning", "reason": "cpu_high"}, ""},
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": "online", "state": "critical", "reason": "temperature_high"}, eventRouterTemperatureHigh},
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": "online", "state": "critical", "reason": "fan_failed"}, eventRouterHardwareFailed},
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": "online", "state": "warning", "reason": "exporter_silent"}, ""},
		{"horus.snmp.router.state_changed", map[string]any{"previous_state": "online", "state": "degraded", "reason": "snmp_auth_failed"}, ""},
		{"horus.snmp.router.rebooted", map[string]any{}, eventRouterRebooted},
		{"horus.snmp.interface.oper_status_changed", map[string]any{"previous": "up", "current": "down"}, eventRouterInterfaceDown},
		{"horus.snmp.interface.oper_status_changed", map[string]any{"previous": "down", "current": "up"}, eventRouterInterfaceUp},
		{"horus.snmp.interface.oper_status_changed", map[string]any{"previous": "up", "current": "testing"}, ""},
		{"horus.snmp.service.state_changed", map[string]any{"previous_state": "up", "state": "down"}, eventServiceDown},
		{"horus.snmp.service.state_changed", map[string]any{"previous_state": "up", "state": "degraded"}, eventServiceDegraded},
		{"horus.snmp.service.state_changed", map[string]any{"previous_state": "down", "state": "degraded"}, ""},
		{"horus.snmp.service.state_changed", map[string]any{"previous_state": "down", "state": "up"}, eventServiceRecovered},
		{"horus.snmp.service.state_changed", map[string]any{"previous_state": "unknown", "state": "up"}, ""},
	} {
		if got := mapEvent(tc.typ, tc.data); got != tc.want {
			t.Errorf("%s %v = %q, quiero %q", tc.typ, tc.data, got, tc.want)
		}
	}
	for _, e := range EventTypes {
		if EventCategory[e] == "" {
			t.Errorf("%s sin categoría", e)
		}
	}
}

func TestRenderRouterAndServiceMessages(t *testing.T) {
	s := &Service{baseURL: "https://horus.example.net"}
	rid := uuid.NewString()
	data := map[string]any{"router_id": rid, "previous_state": "online", "state": "critical", "reason": "cpu_high",
		"thresholds": []any{map[string]any{"metric": "cpu_percent", "level": "critical", "value": 97.25, "threshold": 95.0}}}
	m, sev := s.render(eventRouterCPUHigh, data, renderCtx{routerName: "rt-core-01", siteName: "Centro", slug: "fibra-norte"})
	if sev != "high" || m.Subject != "CPU alta en el router «rt-core-01»" || !strings.Contains(m.Text, "97.3 % (umbral 95 %)") ||
		!strings.Contains(m.Text, "Nodo: Centro") || m.Link != "https://horus.example.net/t/fibra-norte/routers/"+rid {
		t.Fatalf("cpu = %+v %s", m, sev)
	}
	m, sev = s.render(eventRouterOffline, map[string]any{"router_id": rid, "reason": "tunnel_down"}, renderCtx{})
	if sev != "critical" || !strings.Contains(m.Subject, rid) || !strings.Contains(m.Text, "WireGuard") {
		t.Fatalf("offline = %+v", m)
	}
	m, sev = s.render(eventRouterInterfaceDown, map[string]any{"router_id": rid, "name": "sfp1", "uplink": true, "previous": "up",
		"current": "down"}, renderCtx{routerName: "r"})
	if sev != "high" || !strings.HasPrefix(m.Subject, "Enlace de subida sfp1 caída") {
		t.Fatalf("uplink = %+v %s", m, sev)
	}
	m, sev = s.render(eventServiceDown, map[string]any{"name": "Clientes FTTH", "check_type": "ddos_inbound", "target": "100.64.3.9"}, renderCtx{})
	if sev != "critical" || strings.Contains(m.Subject+m.Text, "100.64.3.9") {
		t.Fatalf("ddos = %+v", m)
	}
	m, _ = s.render(eventServiceDown, map[string]any{"name": "DNS", "check_type": "tcp", "target": "dns1.isp.example:53", "reason": "connect_timeout"}, renderCtx{})
	if !strings.Contains(m.Text, "dns1.isp.example:53") || !strings.Contains(m.Text, "connect timeout") {
		t.Fatalf("tcp = %+v", m)
	}
	m, sev = s.render(eventServiceRecovered, map[string]any{"name": "DNS", "check_type": "icmp"}, renderCtx{})
	if sev != "info" || m.Subject != "Servicio «DNS» recuperado" {
		t.Fatalf("recovered = %+v", m)
	}
}

func TestSubscriptionCategories(t *testing.T) {
	var f fieldErrs
	sub := Subscription{Categories: []string{CategoryRouters}}
	validateSubscription(&sub, &f)
	if len(f) != 0 || !sub.Subscribes(eventRouterOffline) || sub.Subscribes(eventFindingOpened) || !sub.Recovery() {
		t.Fatalf("categorías: %v %+v", f, sub)
	}
	// Suscripción antigua (solo event_types, sin categorías ni notify_recovery).
	var old Subscription
	if err := json.Unmarshal([]byte(`{"event_types":["finding_opened"],"min_severity":"high","site_ids":[]}`), &old); err != nil {
		t.Fatal(err)
	}
	validateSubscription(&old, &f)
	if len(f) != 0 || !old.Subscribes(eventFindingOpened) || old.Subscribes(eventRouterOffline) {
		t.Fatalf("antigua: %v %+v", f, old)
	}
	empty := Subscription{}
	validateSubscription(&empty, &f)
	bad := Subscription{Categories: []string{"weather"}}
	validateSubscription(&bad, &f)
	if len(f) != 2 {
		t.Fatalf("errores = %v", f)
	}
	off := false
	if (Subscription{NotifyRecovery: &off}).Recovery() {
		t.Fatal("notify_recovery=false")
	}
}

func TestFormatTelegramEscapes(t *testing.T) {
	out := formatTelegram(Message{Subject: "Router «<b>x</b>» & co", Text: "a<b", Link: "https://h.example/t/a?x=1&y=\"2\""}, "critical")
	want := "🔴 <b>Router «&lt;b&gt;x&lt;/b&gt;» &amp; co</b>\na&lt;b\n\n<a href=\"https://h.example/t/a?x=1&amp;y=&quot;2&quot;\">Abrir en Horus</a>"
	if out != want {
		t.Fatalf("html =\n%s\nquiero\n%s", out, want)
	}
	if strings.Contains(formatTelegram(Message{Subject: "s", Link: "javascript:alert(1)"}, "info"), "href") {
		t.Fatal("enlace no http aceptado")
	}
}

// tgStub simula la Bot API.
type tgStub struct {
	mu       sync.Mutex
	member   string
	canPost  bool
	chatType string
	sendResp string
	updates  []string
	offsets  []float64
	sent     []map[string]any
	webhook  bool
}

func (s *tgStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body map[string]any
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &body)
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if !strings.Contains(r.URL.Path, "/bot"+testToken+"/") {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
		return
	}
	switch method {
	case "getMe":
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":777,"is_bot":true,"first_name":"NOC","username":"noc_isp_bot"}}`))
	case "getChat":
		if body["chat_id"] == float64(-100999) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found (bot ` + testToken + `)"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":-1001,"type":"` + s.chatType + `","title":"NOC <ISP>"}}`))
	case "getChatMember":
		_, _ = w.Write([]byte(`{"ok":true,"result":{"status":"` + s.member + `","can_post_messages":` + map[bool]string{true: "true", false: "false"}[s.canPost] + `}}`))
	case "sendMessage":
		s.sent = append(s.sent, body)
		if s.sendResp != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(s.sendResp))
			s.sendResp = ""
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	case "getUpdates":
		if s.webhook {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first"}`))
			return
		}
		off, _ := body["offset"].(float64)
		s.offsets = append(s.offsets, off)
		if off > 0 {
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[` + strings.Join(s.updates, ",") + `]}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestTelegramConnectionTest(t *testing.T) {
	stub := &tgStub{member: "administrator", canPost: true, chatType: "channel"}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	s := &Senders{TelegramAPI: srv.URL}
	cr := Credentials{TelegramBotToken: testToken}
	ctx := context.Background()
	if r := s.testTelegram(ctx, TelegramConfig{ChatID: "-1001"}, cr); !r.OK || *r.BotUsername != "@noc_isp_bot" || *r.ChatTitle != "NOC <ISP>" {
		t.Fatalf("ok = %+v", r)
	}
	stub.canPost = false
	if r := s.testTelegram(ctx, TelegramConfig{ChatID: "-1001"}, cr); r.OK || *r.ErrorCode != "bot_cannot_post" {
		t.Fatalf("sin publicar = %+v", r)
	}
	stub.member, stub.chatType = "left", "supergroup"
	if r := s.testTelegram(ctx, TelegramConfig{ChatID: "-1001"}, cr); r.OK || *r.ErrorCode != "bot_not_member" {
		t.Fatalf("fuera = %+v", r)
	}
	r := s.testTelegram(ctx, TelegramConfig{ChatID: "-100999"}, cr)
	if r.OK || *r.ErrorCode != "chat_not_found" || !strings.Contains(*r.Error, "chat not found") || strings.Contains(*r.Error, testToken) {
		t.Fatalf("chat inexistente = %+v", *r.Error)
	}
	r = s.testTelegram(ctx, TelegramConfig{ChatID: "-1001"}, Credentials{TelegramBotToken: "999:" + strings.Repeat("x", 35)})
	if r.OK || *r.ErrorCode != "auth_failed" || !strings.Contains(*r.Error, "Unauthorized") {
		t.Fatalf("token malo = %+v", r)
	}
}

func TestTelegramSendRateLimitAndMigration(t *testing.T) {
	stub := &tgStub{}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	s := &Senders{TelegramAPI: srv.URL}
	cr := Credentials{TelegramBotToken: testToken}
	thread := int64(12)
	if err := s.sendTelegram(context.Background(), TelegramConfig{ChatID: "@noc_isp", MessageThreadID: &thread}, cr,
		Message{Subject: "a<b", Text: "x"}, "high"); err != nil {
		t.Fatal(err)
	}
	got := stub.sent[0]
	if got["parse_mode"] != "HTML" || got["chat_id"] != "@noc_isp" || got["message_thread_id"] != float64(12) ||
		got["text"] != "🟠 <b>a&lt;b</b>\nx" {
		t.Fatalf("sendMessage = %v", got)
	}
	if lp, _ := got["link_preview_options"].(map[string]any); lp["is_disabled"] != true {
		t.Fatalf("vista previa = %v", got)
	}
	stub.sendResp = `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`
	err := s.sendTelegram(context.Background(), TelegramConfig{ChatID: "-1001"}, cr, Message{Subject: "x"}, "info")
	var te *TelegramError
	if !errors.As(err, &te) || te.RetryAfter != 7*time.Second {
		t.Fatalf("429 = %v", err)
	}
	stub.sendResp = `{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1009876}}`
	err = s.sendTelegram(context.Background(), TelegramConfig{ChatID: "-1001"}, cr, Message{Subject: "x"}, "info")
	if !errors.As(err, &te) || te.MigrateTo != -1009876 {
		t.Fatalf("migración = %v", err)
	}
}

func TestTelegramPollUpdates(t *testing.T) {
	stub := &tgStub{updates: []string{
		`{"update_id":10,"message":{"chat":{"id":555,"type":"private","first_name":"Ana"},"text":"/start hfabcdefgh23"}}`,
		`{"update_id":11,"channel_post":{"chat":{"id":-1002,"type":"channel","title":"NOC"},"text":"hola"}}`,
		`{"update_id":12,"my_chat_member":{"chat":{"id":-1003,"type":"supergroup","title":"Guardia"}}}`,
	}}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	s := &Senders{TelegramAPI: srv.URL}
	var chats []ChatCandidate
	err := s.pollUpdates(context.Background(), testToken, func(ups []tgUpdate) error {
		for _, u := range ups {
			c, _, _ := u.chat()
			chats = append(chats, candidate(c))
		}
		return nil
	})
	if err != nil || len(chats) != 3 || chats[0].Title != "Ana" || chats[1].ID != "-1002" {
		t.Fatalf("chats = %+v %v", chats, err)
	}
	// Confirmación con offset = último + 1.
	if len(stub.offsets) != 2 || stub.offsets[1] != 13 {
		t.Fatalf("offsets = %v", stub.offsets)
	}
	stub.webhook = true
	err = s.pollUpdates(context.Background(), testToken, func([]tgUpdate) error { return nil })
	var te *TelegramError
	if !errors.As(err, &te) || !te.webhookActive() {
		t.Fatalf("webhook = %v", err)
	}
	if e := telegramErr(err).Error(); !strings.Contains(e, CodeTelegramWebhookActive) || !strings.Contains(e, "deleteWebhook") {
		t.Fatalf("error = %s", e)
	}
}

func TestSanitizeRemote(t *testing.T) {
	got := sanitizeRemote("Bad\nRequest: "+testToken+" otro 987654:"+strings.Repeat("Z", 35), testToken)
	if strings.Contains(got, "ABCDEF") || strings.Contains(got, "ZZZZ") || strings.Contains(got, "\n") {
		t.Fatalf("saneado = %q", got)
	}
}
