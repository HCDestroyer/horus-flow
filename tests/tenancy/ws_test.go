//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
)

type wsClient struct {
	t  *testing.T
	c  *websocket.Conn
	in chan map[string]any
	// closeCode es el código de cierre recibido.
	closed chan websocket.StatusCode
}

func (a *app) wsConnect(token, query string) *wsClient {
	a.t.Helper()
	r := a.must(a.do(req{Method: "POST", Path: "/api/v1/ws/tickets", Token: token}), 201)
	if !strings.HasSuffix(r.str("url"), "/api/v1/ws") {
		a.t.Fatalf("url = %s", r.Raw)
	}
	u := "ws" + strings.TrimPrefix(a.srv.URL, "http") + "/api/v1/ws?ticket=" + r.str("ticket") + query
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{Subprotocols: []string{"horus.ws.v1"}})
	if err != nil {
		a.t.Fatalf("dial: %v", err)
	}
	ws := &wsClient{t: a.t, c: c, in: make(chan map[string]any, 100), closed: make(chan websocket.StatusCode, 1)}
	go func() {
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				ws.closed <- websocket.CloseStatus(err)
				close(ws.in)
				return
			}
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			ws.in <- m
		}
	}()
	a.t.Cleanup(func() { _ = c.CloseNow() })
	return ws
}

func (w *wsClient) send(v any) {
	w.t.Helper()
	b, _ := json.Marshal(v)
	if err := w.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		w.t.Fatal(err)
	}
}

// next devuelve el siguiente mensaje que cumple match (ignora heartbeats).
func (w *wsClient) next(d time.Duration, match func(map[string]any) bool) (map[string]any, bool) {
	deadline := time.After(d)
	for {
		select {
		case m, ok := <-w.in:
			if !ok {
				return nil, false
			}
			if match(m) {
				return m, true
			}
		case <-deadline:
			return nil, false
		}
	}
}

func typeIs(t string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["type"] == t }
}

func (w *wsClient) subscribe(id, topic string) map[string]any {
	w.t.Helper()
	w.send(map[string]any{"type": "subscribe", "id": id, "topic": topic})
	m, ok := w.next(5*time.Second, func(m map[string]any) bool { return m["id"] == id })
	if !ok {
		w.t.Fatalf("sin respuesta a subscribe %s", topic)
	}
	return m
}

func publishFinding(t *testing.T, js jetstream.JetStream, tenant string) string {
	t.Helper()
	tid := uuid.MustParse(tenant)
	id := uuid.Must(uuid.NewV7())
	env := natsx.Envelope{ID: uuid.Must(uuid.NewV7()), Type: "horus.detection.finding.opened", Source: "horus/detection", Subject: id.String(),
		TenantID: &tid, AggregateType: "finding", AggregateVersion: 1,
		Data: json.RawMessage(`{"id":"` + id.String() + `","kind":"outbound_scanning","severity":"high","state":"open"}`)}
	if err := natsx.Publish(context.Background(), js, &env); err != nil {
		t.Fatal(err)
	}
	return env.ID.String()
}

// I1-13: hallazgos en < 2 s solo al ISP dueño, topics de otro ISP rechazados
// sin revelarlo, reanudación con el último ID, kioscos limitados a sus
// dashboards y cierre 4409 al revocar.
func TestRealtimeWebSocket(t *testing.T) {
	t.Parallel()
	a, js := startAppNATS(t)
	pt := a.superadmin().platformToken()
	A, B := a.newISP(pt, "isp-a"), a.newISP(pt, "isp-b")
	fb := a.fixtureISP(B, "10.30.0.0/24")

	ws := a.wsConnect(A.token, "")
	if m := ws.subscribe("c-1", "security"); m["type"] != "ack" {
		t.Fatalf("subscribe = %v", m)
	}
	time.Sleep(300 * time.Millisecond) // suscripciones NATS listas

	// Criterio 1: el de A llega en < 2 s; el de B no.
	publishFinding(t, js, B.id)
	idA := publishFinding(t, js, A.id)
	m, ok := ws.next(2*time.Second, typeIs("event"))
	if !ok {
		t.Fatal("el hallazgo de A no llegó en 2 s")
	}
	ev := m["event"].(map[string]any)
	if ev["id"] != idA || ev["tenant_id"] != A.id || m["topic"] != "security" {
		t.Fatalf("evento = %v", m)
	}
	if _, ok := ws.next(700*time.Millisecond, typeIs("event")); ok {
		t.Fatal("A recibió un evento de B")
	}
	// Mensaje con cabecera de A y sobre de B: se descarta.
	bad := nats.NewMsg("horus.detection.finding.opened." + uuid.NewString())
	bad.Header.Set(natsx.HeaderTenant, A.id)
	bad.Data = []byte(`{"id":"` + uuid.NewString() + `","type":"horus.detection.finding.opened","tenant_id":"` + B.id + `","data":{}}`)
	if _, err := js.PublishMsg(context.Background(), bad); err != nil {
		t.Fatal(err)
	}
	if _, ok := ws.next(700*time.Millisecond, typeIs("event")); ok {
		t.Fatal("se entregó un mensaje con Horus-Tenant ≠ sobre")
	}

	// Criterio 2: topic de un dashboard de otro ISP (o inexistente): mismo rechazo.
	foreign := ws.subscribe("c-2", "dashboard."+fb["dashboard_id"])
	ghost := ws.subscribe("c-3", "dashboard."+uuid.NewString())
	if foreign["code"] != "PERMISSION_DENIED" || foreign["message"] != ghost["message"] {
		t.Fatalf("topic ajeno = %v / inexistente = %v", foreign, ghost)
	}
	if m := ws.subscribe("c-4", "customers"); m["type"] != "ack" {
		t.Fatalf("customers = %v", m)
	}

	// Criterio 3: reanudación dentro de la ventana y fuera de ella.
	missed1, missed2 := publishFinding(t, js, A.id), publishFinding(t, js, A.id)
	time.Sleep(500 * time.Millisecond)
	ws2 := a.wsConnect(A.token, "&last_event_id="+idA)
	ws2.send(map[string]any{"type": "subscribe", "id": "r-1", "topic": "security"})
	var got []string
	for len(got) < 2 {
		m, ok := ws2.next(3*time.Second, typeIs("event"))
		if !ok {
			t.Fatalf("reanudación: recibidos %v", got)
		}
		got = append(got, m["event"].(map[string]any)["id"].(string))
	}
	if got[0] != missed1 || got[1] != missed2 {
		t.Fatalf("reanudación = %v, quiero %s %s", got, missed1, missed2)
	}
	ws3 := a.wsConnect(A.token, "&last_event_id="+uuid.NewString())
	ws3.send(map[string]any{"type": "subscribe", "id": "r-2", "topic": "security"})
	if _, ok := ws3.next(3*time.Second, typeIs("resync_required")); !ok {
		t.Fatal("fuera de la ventana sin resync_required")
	}

	// Criterio 4: kiosco con la plantilla NOC: solo sus topics.
	pl := a.must(a.post(A.token, "/api/v1/playlists", map[string]any{"name": "NOC", "items": []any{
		map[string]any{"dashboard_id": nocTemplate, "duration_seconds": 30}}}), 201)
	kid, _, kjwt := a.enrolledKiosk(A, map[string]any{"name": "TV", "playlist_id": pl.str("id")})
	kws := a.wsConnect(kjwt, "")
	for i, topic := range []string{"security", "traffic.summary", "dashboard." + nocTemplate, "system"} {
		if m := kws.subscribe(fmt.Sprintf("k-%d", i), topic); m["type"] != "ack" {
			t.Fatalf("kiosco en %s: %v", topic, m)
		}
	}
	for i, topic := range []string{"wireguard.peers", "routers", "dashboard.0192f000-0000-7000-8000-00000000d002"} {
		if m := kws.subscribe(fmt.Sprintf("d-%d", i), topic); m["code"] != "PERMISSION_DENIED" {
			t.Fatalf("kiosco en %s: %v", topic, m)
		}
	}

	// I1-14 criterio 5: revocación → cierre 4409 en < 5 s.
	a.must(a.post(A.token, "/api/v1/kiosks/"+kid+"/revoke", map[string]any{"reason": "device_lost"}), 200)
	select {
	case code := <-kws.closed:
		if code != 4409 {
			t.Fatalf("cierre = %d, quiero 4409", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("el WebSocket del kiosco no se cerró en 5 s")
	}

	// Ticket de un solo uso.
	r := a.must(a.do(req{Method: "POST", Path: "/api/v1/ws/tickets", Token: A.token}), 201)
	u := "ws" + strings.TrimPrefix(a.srv.URL, "http") + "/api/v1/ws?ticket=" + r.str("ticket")
	for i, want := range []websocket.StatusCode{-1, 4401} {
		c, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{Subprotocols: []string{"horus.ws.v1"}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		_, _, err = c.Read(ctx)
		cancel()
		code := websocket.CloseStatus(err)
		if i == 0 && code != -1 { // aceptado: sin cierre del servidor (vence el plazo del cliente)
			t.Fatalf("primer uso del ticket: %v", err)
		}
		if i == 1 && code != want {
			t.Fatalf("segundo uso del ticket: código %d (%v)", code, err)
		}
		_ = c.CloseNow()
	}
}
