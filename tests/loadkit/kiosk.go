package loadkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
)

// NOCTemplate es la plantilla de dashboard NOC (kiosk_allowed) de analytics.
const NOCTemplate = "0192f000-0000-7000-8000-00000000d001"

// Kiosk emula una pantalla de kiosco como lo hace el frontend: credencial de
// dispositivo (cookie rotativa) → JWT de kiosco → ticket → WebSocket con
// reconexión cada 2 s, suscrito a `security` (topic del dashboard NOC), y
// sondea /kiosk/config y los datos de los widgets del dashboard.
type Kiosk struct {
	api    *API
	cookie string
	token  string

	mu           sync.Mutex
	connected    bool
	connectedAt  time.Time
	disconnects  int
	events       map[string]time.Time // id de evento → recepción
	lastHTTP     int                  // último estado de las sondas HTTP
	lastHTTPOKAt time.Time
	widgetURLs   []string
}

// NewKiosk da de alta y enrola un kiosco del ISP con una playlist NOC.
func NewKiosk(ctx context.Context, a *API) (*Kiosk, error) {
	pl, err := a.expect(ctx, true, Call{Method: http.MethodPost, Path: "/api/v1/playlists", Body: map[string]any{
		"name": "NOC carga", "items": []any{map[string]any{"dashboard_id": NOCTemplate, "duration_seconds": 30}}}}, "alta de playlist", 201)
	if err != nil {
		return nil, err
	}
	k, err := a.expect(ctx, true, Call{Method: http.MethodPost, Path: "/api/v1/kiosks",
		Body: map[string]any{"name": "TV NOC carga", "playlist_id": pl.Str("id")}}, "alta de kiosco", 201)
	if err != nil {
		return nil, err
	}
	code, err := a.expect(ctx, true, Call{Method: http.MethodPost, Path: "/api/v1/kiosks/" + k.Str("id") + "/enrollment-codes",
		Header: map[string]string{"Idempotency-Key": uuid.NewString()}}, "código de enrolamiento", 201)
	if err != nil {
		return nil, err
	}
	en, err := a.expect(ctx, false, Call{Method: http.MethodPost, Path: "/api/v1/kiosk/enroll",
		Header: map[string]string{"X-Requested-With": "horus"}, Body: map[string]any{"code": code.Str("code")}}, "enrolamiento", 204)
	if err != nil {
		return nil, err
	}
	kz := &Kiosk{api: a, cookie: kioskCookie(en), events: map[string]time.Time{}}
	if kz.cookie == "" {
		return nil, errors.New("el enrolamiento no devolvió la cookie del kiosco")
	}
	if err := kz.refresh(ctx); err != nil {
		return nil, err
	}
	d, err := a.expect(ctx, false, Call{Method: http.MethodGet, Path: "/api/v1/dashboards/" + NOCTemplate, Token: kz.token}, "dashboard NOC (kiosco)", 200)
	if err != nil {
		return nil, err
	}
	if ws, ok := d.Body["widgets"].([]any); ok {
		for _, w := range ws {
			if m, ok := w.(map[string]any); ok {
				if id, _ := m["id"].(string); id != "" {
					kz.widgetURLs = append(kz.widgetURLs, "/api/v1/dashboards/"+NOCTemplate+"/widgets/"+id+"/data")
				}
			}
		}
	}
	return kz, nil
}

func kioskCookie(r Resp) string {
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == "__Secure-hf_kiosk" {
			return c.Value
		}
	}
	return ""
}

// refresh canjea la cookie (rotativa) por un JWT de kiosco.
func (k *Kiosk) refresh(ctx context.Context) error {
	r, err := k.api.expect(ctx, false, Call{Method: http.MethodPost, Path: "/api/v1/kiosk/token",
		Header: map[string]string{"X-Requested-With": "horus", "Cookie": "__Secure-hf_kiosk=" + k.cookie}}, "token de kiosco", 200)
	if err != nil {
		return err
	}
	if c := kioskCookie(r); c != "" {
		k.cookie = c
	}
	k.token = r.Str("access_token")
	return nil
}

// call hace una petición con el JWT del kiosco, renovándolo ante un 401.
func (k *Kiosk) call(ctx context.Context, c Call) (Resp, error) {
	c.Token = k.token
	r, err := k.api.Do(ctx, c)
	if err == nil && r.Status == http.StatusUnauthorized {
		if err := k.refresh(ctx); err != nil {
			return r, err
		}
		c.Token = k.token
		r, err = k.api.Do(ctx, c)
	}
	return r, err
}

// Run mantiene el WebSocket y las sondas HTTP hasta que ctx se cancela.
func (k *Kiosk) Run(ctx context.Context) {
	go k.pollHTTP(ctx)
	for ctx.Err() == nil {
		err := k.session(ctx)
		k.mu.Lock()
		if k.connected {
			k.disconnects++
		}
		k.connected = false
		k.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (k *Kiosk) session(ctx context.Context) error {
	r, err := k.call(ctx, Call{Method: http.MethodPost, Path: "/api/v1/ws/tickets"})
	if err != nil {
		return err
	}
	if r.Status != http.StatusCreated {
		return fmt.Errorf("ticket: HTTP %d", r.Status)
	}
	u := "ws" + strings.TrimPrefix(k.api.Base, "http") + "/api/v1/ws?ticket=" + r.Str("ticket")
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	c, _, err := websocket.Dial(dctx, u, &websocket.DialOptions{Subprotocols: []string{"horus.ws.v1"}})
	cancel()
	if err != nil {
		return err
	}
	defer func() { _ = c.CloseNow() }()
	sub, _ := json.Marshal(map[string]any{"type": "subscribe", "id": "s-1", "topic": "security"})
	if err := c.Write(ctx, websocket.MessageText, sub); err != nil {
		return err
	}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		switch m["type"] {
		case "ack":
			k.mu.Lock()
			if !k.connected {
				k.connected, k.connectedAt = true, time.Now()
			}
			k.mu.Unlock()
		case "event":
			if ev, ok := m["event"].(map[string]any); ok {
				if id, _ := ev["id"].(string); id != "" {
					k.mu.Lock()
					k.events[id] = time.Now()
					k.mu.Unlock()
				}
			}
		}
	}
}

func (k *Kiosk) pollHTTP(ctx context.Context) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		paths := append([]string{"/api/v1/kiosk/config"}, k.widgetURLs...)
		worst := http.StatusOK
		for _, p := range paths {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			r, err := k.call(cctx, Call{Method: http.MethodGet, Path: p})
			cancel()
			if err != nil {
				worst = 0
			} else if r.Status != http.StatusOK && worst == http.StatusOK {
				worst = r.Status
			}
		}
		k.mu.Lock()
		k.lastHTTP = worst
		if worst == http.StatusOK {
			k.lastHTTPOKAt = time.Now()
		}
		k.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// KioskStatus es una foto del kiosco.
type KioskStatus struct {
	Connected   bool
	ConnectedAt time.Time
	Disconnects int
	HTTPStatus  int
	HTTPOKAt    time.Time
}

// Status devuelve el estado actual.
func (k *Kiosk) Status() KioskStatus {
	k.mu.Lock()
	defer k.mu.Unlock()
	return KioskStatus{Connected: k.connected, ConnectedAt: k.connectedAt, Disconnects: k.disconnects,
		HTTPStatus: k.lastHTTP, HTTPOKAt: k.lastHTTPOKAt}
}

// ProbeRealtime publica un hallazgo sintético del ISP en JetStream y espera
// a que llegue al WebSocket del kiosco (topic security). Devuelve la latencia.
func (k *Kiosk) ProbeRealtime(ctx context.Context, b *Bus, tenant string, timeout time.Duration) (time.Duration, error) {
	tid, err := uuid.Parse(tenant)
	if err != nil {
		return 0, err
	}
	id := uuid.Must(uuid.NewV7())
	env := natsx.Envelope{ID: uuid.Must(uuid.NewV7()), Type: "horus.detection.finding.opened", Source: "horus/load-probe",
		Subject: id.String(), TenantID: &tid, AggregateType: "finding", AggregateVersion: 1,
		Data: json.RawMessage(`{"id":"` + id.String() + `","kind":"outbound_scanning","severity":"low","state":"open","site_id":null}`)}
	start := time.Now()
	if err := natsx.Publish(ctx, b.JS, &env); err != nil {
		return 0, err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		k.mu.Lock()
		at, ok := k.events[env.ID.String()]
		k.mu.Unlock()
		if ok {
			return at.Sub(start), nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return 0, fmt.Errorf("el evento no llegó al kiosco en %s", timeout)
}
