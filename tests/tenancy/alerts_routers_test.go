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
	"github.com/hcdestroyer/horus-flow/packages/go/tenanttest"
	"github.com/hcdestroyer/horus-flow/services/snmp"
)

// fakeTelegram simula la Bot API para el bot compartido de la instalación:
// getMe, getChat, getChatMember, sendMessage y getUpdates con las
// actualizaciones que el test le cargue.
type fakeTelegram struct {
	mu      sync.Mutex
	sent    []string
	updates []string
	acked   int64
}

func (f *fakeTelegram) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":99,"is_bot":true,"first_name":"Horus","username":"horus_alertas_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getChat"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":-100123,"type":"supergroup","title":"NOC","is_forum":true}}`))
		case strings.HasSuffix(r.URL.Path, "/getChatMember"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"status":"member"}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			f.sent = append(f.sent, string(body))
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			if off, _ := in["offset"].(float64); off > 0 {
				f.acked = int64(off)
				f.updates = nil
				_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":[` + strings.Join(f.updates, ",") + `]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeTelegram) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func publishSNMP(t *testing.T, js jetstream.JetStream, tenant, typ, entity string, data map[string]any) {
	t.Helper()
	tid := uuid.MustParse(tenant)
	b, _ := json.Marshal(data)
	env := natsx.Envelope{ID: uuid.Must(uuid.NewV7()), Type: typ, Source: "horus/snmp", Subject: entity, TenantID: &tid,
		AggregateType: "router", AggregateVersion: 1, Data: b}
	if err := natsx.Publish(context.Background(), js, &env); err != nil {
		t.Fatal(err)
	}
}

// Fase 2/4: avisos de routers (rol snmp) por categoría, recuperación que llega
// aunque min_severity la filtraría, mensajes HTML con el nombre del router y
// aislamiento entre ISP de los códigos de vinculación del bot compartido.
func TestAlertsRouterCategoriesAndTelegramLink(t *testing.T) {
	t.Parallel()
	tg := &fakeTelegram{}
	srv := tg.server(t)
	a, js := startAppNATS(t, "HORUS_TELEGRAM_API_URL="+srv.URL, "HORUS_TELEGRAM_BOT_TOKEN=1:installation-bot-token-xxxxxxxxxxxxxxxx")
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-ra"), a.newISP(pt, "isp-rb")

	ch := a.must(a.post(A.token, "/api/v1/notification-channels", map[string]any{"name": "NOC routers", "kind": "telegram",
		"config": map[string]any{"chat_id": "-100123", "message_thread_id": 7},
		"subscription": map[string]any{"event_types": []string{}, "categories": []string{"routers"}, "min_severity": "high"}}), 201)
	sub := ch.Body["subscription"].(map[string]any)
	if sub["notify_recovery"] != true || len(sub["categories"].([]any)) != 1 {
		t.Fatalf("suscripción = %s", ch.Raw)
	}
	if r := a.post(A.token, "/api/v1/notification-channels", map[string]any{"name": "x", "kind": "telegram", "config": map[string]any{"chat_id": "-1"},
		"subscription": map[string]any{"event_types": []string{}, "categories": []string{"weather"}}}); r.Status != 422 {
		t.Fatalf("categoría inválida: %d %s", r.Status, r.Raw)
	}
	ct := a.must(a.post(A.token, "/api/v1/notification-channels/"+ch.str("id")+"/connection-test", nil), 200)
	if ct.Body["ok"] != true || ct.Body["bot_username"] != "@horus_alertas_bot" || ct.Body["chat_title"] != "NOC" {
		t.Fatalf("connection-test = %s", ct.Raw)
	}

	time.Sleep(time.Second) // durables deliver-new creados
	router := uuid.NewString()
	publishSNMP(t, js, A.id, "horus.snmp.router.state_changed", router, map[string]any{"router_id": router, "site_id": uuid.NewString(),
		"previous_state": "online", "state": "offline", "reason": "tunnel_down", "router_name": "rt-core-<01>"})
	a.waitFor("aviso de router caído", func() bool { return len(tg.messages()) == 1 })
	// Interfaz no troncal (medium) filtrada por min_severity; servicio fuera de la categoría.
	publishSNMP(t, js, A.id, "horus.snmp.interface.oper_status_changed", uuid.NewString(), map[string]any{"interface_id": uuid.NewString(),
		"router_id": router, "name": "ether5", "previous": "up", "current": "down", "uplink": false})
	publishSNMP(t, js, A.id, "horus.snmp.service.state_changed", uuid.NewString(), map[string]any{"service_id": uuid.NewString(),
		"name": "DNS", "check_type": "tcp", "target": "dns1.isp.example:53", "previous_state": "up", "state": "down"})
	// Recuperación (info): llega porque el canal recibió la alarma.
	publishSNMP(t, js, A.id, "horus.snmp.router.state_changed", router, map[string]any{"router_id": router,
		"previous_state": "offline", "state": "online", "reason": "ok", "router_name": "rt-core-<01>"})
	// Un evento de B no llega al canal de A.
	publishSNMP(t, js, B.id, "horus.snmp.router.state_changed", router, map[string]any{"router_id": router,
		"previous_state": "online", "state": "offline", "reason": "tunnel_down"})
	a.waitFor("recuperación", func() bool { return len(tg.messages()) >= 2 })
	time.Sleep(700 * time.Millisecond)
	msgs := tg.messages()
	if len(msgs) != 2 {
		t.Fatalf("mensajes = %d: %v", len(msgs), msgs)
	}
	var first, second map[string]any
	_ = json.Unmarshal([]byte(msgs[0]), &first)
	_ = json.Unmarshal([]byte(msgs[1]), &second)
	if first["parse_mode"] != "HTML" || first["message_thread_id"] != float64(7) ||
		!strings.Contains(first["text"].(string), "fuera de línea") || !strings.Contains(first["text"].(string), "rt-core-&lt;01&gt;") {
		t.Fatalf("aviso = %v", first)
	}
	if !strings.Contains(second["text"].(string), "recuperado") || !strings.HasPrefix(second["text"].(string), "🟢") {
		t.Fatalf("recuperación = %v", second)
	}
	dl := a.must(a.do(req{Method: "GET", Path: "/api/v1/notification-deliveries", Token: A.token}), 200)
	rows := dl.Body["data"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["event_type"] != "router_recovered" || rows[0].(map[string]any)["severity"] != "info" ||
		!strings.Contains(rows[1].(map[string]any)["summary"].(string), "fuera de línea") {
		t.Fatalf("entregas = %s", dl.Raw)
	}

	// Bot compartido: «Detectar chat» exige un código del ISP y solo devuelve
	// el chat que lo envió; el código de A no sirve a B.
	if r := a.post(A.token, "/api/v1/notification-channels/telegram/chat-detection", map[string]any{}); r.Status != 422 {
		t.Fatalf("sin código: %d %s", r.Status, r.Raw)
	}
	codeA := a.must(a.post(A.token, "/api/v1/notification-channels/telegram/link-codes", nil), 201)
	codeB := a.must(a.post(B.token, "/api/v1/notification-channels/telegram/link-codes", nil), 201)
	if codeA.Body["bot_username"] != "@horus_alertas_bot" || !strings.Contains(codeA.str("start_url"), "start="+codeA.str("code")) {
		t.Fatalf("código = %s", codeA.Raw)
	}
	tg.mu.Lock()
	tg.updates = []string{
		`{"update_id":40,"message":{"chat":{"id":-100777,"type":"supergroup","title":"Guardia ISP A"},"text":"/start@horus_alertas_bot ` + codeA.str("code") + `"}}`,
		`{"update_id":41,"message":{"chat":{"id":555,"type":"private","first_name":"Otro"},"text":"hola"}}`,
	}
	tg.mu.Unlock()
	if r := a.post(B.token, "/api/v1/notification-channels/telegram/chat-detection", map[string]any{"code": codeA.str("code")}); r.Status != 422 ||
		r.code() != "NOTIFICATION_TELEGRAM_LINK_CODE_INVALID" {
		t.Fatalf("código de A usado por B: %d %s", r.Status, r.Raw)
	}
	// B detecta con su código: las actualizaciones (de A) se reparten por código y B no ve nada.
	db := a.must(a.post(B.token, "/api/v1/notification-channels/telegram/chat-detection", map[string]any{"code": codeB.str("code")}), 200)
	if len(db.Body["data"].([]any)) != 0 || db.Body["shared_bot"] != true {
		t.Fatalf("B ve chats: %s", db.Raw)
	}
	if tg.acked != 42 {
		t.Fatalf("actualizaciones sin confirmar: offset %d", tg.acked)
	}
	da := a.must(a.post(A.token, "/api/v1/notification-channels/telegram/chat-detection", map[string]any{"code": codeA.str("code")}), 200)
	chats := da.Body["data"].([]any)
	if len(chats) != 1 || chats[0].(map[string]any)["id"] != "-100777" || chats[0].(map[string]any)["title"] != "Guardia ISP A" {
		t.Fatalf("A detecta = %s", da.Raw)
	}
	// Validar un bot propio: token rechazado → 422 con la descripción saneada.
	if r := a.post(A.token, "/api/v1/notification-channels/telegram/bot-validation", map[string]any{"bot_token": "nope"}); r.Status != 422 {
		t.Fatalf("token mal formado: %d", r.Status)
	}
	tenanttest.AssertRLS(t, a.admin, []string{"alerts"}, []string{"alerts.outbox"}, []string{"alerts.outbox"})
}

// Fase 4: umbrales de routers por ISP en el esquema snmp con RLS: lo que fija
// un ISP no lo ve ni lo cambia otro; sin ajustes rigen los de la instalación.
func TestSNMPThresholdsPerTenant(t *testing.T) {
	t.Parallel()
	a := startAppWith(t, []roleFactory{{snmp.Role, snmp.Register}}, "HORUS_SNMP_ENABLED=false", "HORUS_SNMP_CPU_WARNING=81")
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-ta"), a.newISP(pt, "isp-tb")
	def := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/thresholds", Token: A.token}), 200)
	if def.Body["customized"] != false || def.Body["cpu_warning"] != float64(81) {
		t.Fatalf("por defecto = %s", def.Raw)
	}
	in := map[string]any{"cpu_warning": 70, "cpu_critical": 90, "memory_warning": 80, "memory_critical": 92,
		"temperature_warning": 60, "temperature_critical": 75}
	put := a.must(a.do(req{Method: "PUT", Path: "/api/v1/routers/thresholds", Token: A.token, Body: in}), 200)
	if put.Body["customized"] != true || put.Body["cpu_warning"] != float64(70) || put.Body["version"] != float64(1) {
		t.Fatalf("put = %s", put.Raw)
	}
	bad := map[string]any{"cpu_warning": 95, "cpu_critical": 90, "memory_warning": 80, "memory_critical": 92,
		"temperature_warning": 60, "temperature_critical": 75}
	if r := a.do(req{Method: "PUT", Path: "/api/v1/routers/thresholds", Token: A.token, Body: bad}); r.Status != 422 {
		t.Fatalf("aviso > crítico: %d", r.Status)
	}
	if b := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/thresholds", Token: B.token}), 200); b.Body["customized"] != false {
		t.Fatalf("B ve los umbrales de A: %s", b.Raw)
	}
	if a.auditCount("snmp.thresholds.updated") != 1 {
		t.Fatal("umbrales no auditados")
	}
	// RLS: sin contexto de tenant el rol de la aplicación no ve filas.
	tenanttest.AssertRLS(t, a.admin, []string{"snmp"}, nil, nil)
	var n int
	if err := a.admin.QueryRow(context.Background(), `SELECT count(*) FROM snmp.router_thresholds`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("filas = %d %v", n, err)
	}
	a.must(a.do(req{Method: "DELETE", Path: "/api/v1/routers/thresholds", Token: A.token}), 200)
	if g := a.must(a.do(req{Method: "GET", Path: "/api/v1/routers/thresholds", Token: A.token}), 200); g.Body["customized"] != false {
		t.Fatalf("tras reset = %s", g.Raw)
	}
}
