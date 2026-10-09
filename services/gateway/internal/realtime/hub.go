package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/jsonapi"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	dashapi "github.com/hcdestroyer/horus-flow/services/analytics/api/dashboards"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Códigos de cierre propios (topics.yaml close_codes).
const (
	CloseInvalidMessage websocket.StatusCode = 4400
	CloseUnauthorized   websocket.StatusCode = 4401
	CloseForbidden      websocket.StatusCode = 4403
	CloseSlowClient     websocket.StatusCode = 4408
	CloseRevoked        websocket.StatusCode = 4409
	CloseLimit          websocket.StatusCode = 4429
)

// Options configura el hub.
type Options struct {
	Verifier      *authz.Verifier
	Sessions      authapi.SessionChecker
	Kiosks        authapi.KioskChecker
	Dashboards    func() (dashapi.Access, bool)
	Origins       authz.Origins
	PublicBaseURL string
	Logger        *slog.Logger
	Now           func() time.Time
	Metrics       prometheus.Registerer
	// ResumeWindow y ResumeSize acotan la reanudación (por tenant).
	ResumeWindow time.Duration
	ResumeSize   int
	// RevokeEvery es el periodo de comprobación de revocación por conexión.
	RevokeEvery time.Duration
	// Heartbeat es el periodo de {"type":"heartbeat"} sin otro tráfico.
	Heartbeat time.Duration
}

// Hub es el hub en memoria: topic → conexiones, por tenant.
type Hub struct {
	o       Options
	tickets *tickets

	mu    sync.RWMutex
	conns map[*conn]struct{}
	rings map[uuid.UUID]*ring
	state map[string]stateMsg // tenant|topic|key → último estado

	busOK    atomic.Bool
	mismatch prometheus.Counter
	gauge    prometheus.Gauge
}

type stateMsg struct {
	Type  string          `json:"type"`
	Topic string          `json:"topic"`
	Key   string          `json:"key"`
	Time  string          `json:"time"`
	Data  json.RawMessage `json:"data"`
}

// outEvent es un evento listo para enviar (con data completa y sin PII).
type outEvent struct {
	proj   Projection
	fields dataFields
}

// New crea el hub.
func New(o Options) *Hub {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.ResumeWindow <= 0 {
		o.ResumeWindow = 5 * time.Minute
	}
	if o.ResumeSize <= 0 {
		o.ResumeSize = 2000
	}
	if o.RevokeEvery <= 0 {
		o.RevokeEvery = 2 * time.Second
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = 25 * time.Second
	}
	if o.Dashboards == nil {
		o.Dashboards = func() (dashapi.Access, bool) { return nil, false }
	}
	h := &Hub{o: o, tickets: newTickets(o.Now), conns: map[*conn]struct{}{}, rings: map[uuid.UUID]*ring{}, state: map[string]stateMsg{},
		mismatch: prometheus.NewCounter(prometheus.CounterOpts{Name: "horus_gateway_ws_tenant_mismatch_total",
			Help: "Mensajes de negocio descartados por Horus-Tenant ausente o distinto del sobre."}),
		gauge: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_gateway_ws_connections", Help: "Conexiones WebSocket abiertas."})}
	if o.Metrics != nil {
		_ = o.Metrics.Register(h.mismatch)
		_ = o.Metrics.Register(h.gauge)
	}
	return h
}

// SetBusStatus informa del estado de NATS (realtime_status a los clientes).
func (h *Hub) SetBusStatus(ok bool) {
	if h.busOK.Swap(ok) == ok {
		return
	}
	status, reason := "ok", any(nil)
	if !ok {
		status, reason = "degraded", "event_bus_unavailable"
	}
	msg, _ := json.Marshal(map[string]any{"type": "realtime_status", "status": status, "reason": reason})
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.conns {
		c.send(msg)
	}
}

// Subscribe suscribe el hub a NATS core (best effort, sin consumidores).
func (h *Hub) Subscribe(nc *nats.Conn) ([]*nats.Subscription, error) {
	var subs []*nats.Subscription
	for _, s := range Subjects {
		sub, err := nc.Subscribe(s, h.Dispatch)
		if err != nil {
			for _, x := range subs {
				_ = x.Unsubscribe()
			}
			return nil, err //nolint:wrapcheck // error de nats
		}
		subs = append(subs, sub)
	}
	h.SetBusStatus(nc.IsConnected())
	nc.SetDisconnectErrHandler(func(*nats.Conn, error) { h.SetBusStatus(false) })
	nc.SetReconnectHandler(func(*nats.Conn) { h.SetBusStatus(true) })
	return subs, nil
}

func (h *Hub) ringFor(t uuid.UUID) *ring {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rings[t]
	if !ok {
		r = &ring{size: h.o.ResumeSize, window: h.o.ResumeWindow}
		h.rings[t] = r
	}
	return r
}

// Dispatch enruta un mensaje NATS por su cabecera Horus-Tenant.
func (h *Hub) Dispatch(m *nats.Msg) {
	ht := m.Header.Get(natsx.HeaderTenant)
	if strings.HasPrefix(m.Subject, "horus.telemetry.flows.summary.") {
		h.dispatchSummary(ht, m)
		return
	}
	env, err := natsx.DecodeEnvelope(m.Data)
	if err != nil {
		return
	}
	var tenant uuid.UUID
	switch ht {
	case "":
		h.mismatch.Inc()
		return
	case natsx.TenantPlatform:
		if env.TenantID != nil {
			h.mismatch.Inc()
			return
		}
	default:
		id, err := uuid.Parse(ht)
		if err != nil || env.TenantID == nil || *env.TenantID != id {
			h.mismatch.Inc()
			return
		}
		tenant = id
	}
	ev := &outEvent{proj: Projection{ID: env.ID.String(), Type: env.Type, Time: env.Time.UTC().Format(natsx.TimeFormat), Subject: env.Subject,
		Data: env.Data}}
	if env.TenantID != nil {
		ev.proj.TenantID = env.TenantID.String()
	}
	if env.AggregateVersion > 0 {
		v := env.AggregateVersion
		ev.proj.AggregateVersion = &v
	}
	if env.Actor != nil {
		ev.proj.Actor = &ProjActor{Type: env.Actor.Type, ID: env.Actor.ID}
	}
	_ = json.Unmarshal(env.Data, &ev.fields)
	h.control(tenant, env.Type, ev)
	topics := TopicsForType(env.Type, env.Subject)
	if len(topics) == 0 {
		return
	}
	if tenant != uuid.Nil {
		h.ringFor(tenant).add(ringItem{id: env.ID, at: h.o.Now(), topics: topics, ev: ev})
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.conns {
		if c.tenant == tenant || (tenant == uuid.Nil && slices0(topics) == "me") {
			c.deliver(topics, ev)
		}
	}
}

func slices0(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// control aplica eventos de control: revocación de kioscos, sesiones y
// membresías (cierre 4409) y suspensión del tenant.
func (h *Hub) control(tenant uuid.UUID, typ string, ev *outEvent) {
	switch typ {
	case "horus.auth.kiosk.revoked":
		if ev.fields.ID != nil {
			h.closeWhere(func(c *conn) bool { return c.tenant == tenant && c.kioskID == *ev.fields.ID }, CloseRevoked, "kiosco revocado")
		}
	case "horus.auth.session.revoked":
		if ev.fields.SessionID != nil {
			h.closeWhere(func(c *conn) bool { return c.sid == *ev.fields.SessionID }, CloseRevoked, "sesión revocada")
		}
	case "horus.auth.membership.revoked":
		if ev.fields.UserID != nil {
			h.closeWhere(func(c *conn) bool { return c.tenant == tenant && c.userID == *ev.fields.UserID }, CloseRevoked, "membresía revocada")
		}
	case "horus.auth.tenant.suspended":
		h.mu.RLock()
		for c := range h.conns {
			if c.tenant == tenant {
				c.unsubscribeAll("tenant_suspended")
			}
		}
		h.mu.RUnlock()
	}
}

func (h *Hub) closeWhere(match func(*conn) bool, code websocket.StatusCode, reason string) {
	h.mu.RLock()
	var hit []*conn
	for c := range h.conns {
		if match(c) {
			hit = append(hit, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range hit {
		c.close(code, reason)
	}
}

func (h *Hub) dispatchSummary(ht string, m *nats.Msg) {
	tenant, err := uuid.Parse(ht)
	if err != nil {
		h.mismatch.Inc()
		return
	}
	var data map[string]any
	key := "tenant"
	if strings.HasPrefix(m.Header.Get(natsx.HeaderContentType), "application/json") {
		if json.Unmarshal(m.Data, &data) != nil {
			return
		}
		if s, _ := data["site_id"].(string); s != "" {
			key = "site:" + s
		}
	} else {
		ts, err := flowpb.UnmarshalTrafficSummary(m.Data)
		if err != nil {
			return
		}
		var site any
		if ts.SiteID != "" {
			site, key = ts.SiteID, "site:"+ts.SiteID
		}
		data = map[string]any{"site_id": site, "down_bps": ts.DownBps, "up_bps": ts.UpBps, "flows_per_second": ts.FlowsPerSecond,
			"active_customers": ts.ActiveCustomers, "window_seconds": int(ts.WindowTo.Sub(ts.WindowFrom).Seconds()), "partial": ts.Partial}
	}
	raw, _ := json.Marshal(data)
	st := stateMsg{Type: "state", Topic: "traffic.summary", Key: key, Time: h.o.Now().UTC().Format(natsx.TimeFormat), Data: raw}
	h.mu.Lock()
	h.state[tenant.String()+"|traffic.summary|"+key] = st
	h.mu.Unlock()
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.conns {
		if c.tenant == tenant {
			c.pushState(st)
		}
	}
}

// snapshot devuelve los últimos estados de un topic del tenant.
func (h *Hub) snapshot(tenant uuid.UUID, topic string) []stateMsg {
	h.mu.RLock()
	defer h.mu.RUnlock()
	prefix := tenant.String() + "|" + topic + "|"
	var out []stateMsg
	for k, v := range h.state {
		if strings.HasPrefix(k, prefix) {
			out = append(out, v)
		}
	}
	return out
}

func (h *Hub) add(c *conn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
	h.gauge.Inc()
}

func (h *Hub) remove(c *conn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
	h.gauge.Dec()
}

// wsURL construye la URL absoluta del WebSocket (D14).
func (h *Hub) wsURL(r *http.Request) string {
	base := h.o.PublicBaseURL
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	u, err := url.Parse(base)
	if err != nil {
		return "/api/v1/ws"
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/ws"
	return u.String()
}

// HandleTicket sirve POST /api/v1/ws/tickets (principal validado por el borde).
func (h *Hub) HandleTicket(w http.ResponseWriter, r *http.Request) {
	p := authz.FromContext(r.Context())
	if p == nil {
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeUnauthenticated)
		return
	}
	tk, exp := h.tickets.Issue(p)
	w.Header().Set("Cache-Control", "no-store")
	jsonapi.Write(w, http.StatusCreated, map[string]any{"ticket": tk, "expires_at": exp.UTC().Format(natsx.TimeFormat), "url": h.wsURL(r)})
}

// HandleWS sirve GET /api/v1/ws (ticket de un uso o Bearer validado por el borde).
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	var p *authz.Principal
	if tk := r.URL.Query().Get("ticket"); tk != "" {
		p, _ = h.tickets.Redeem(tk)
	} else {
		p = authz.FromContext(r.Context())
	}
	originOK := true
	if o := r.Header.Get("Origin"); o != "" && !h.o.Origins.Allowed(o) {
		originOK = false
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Protocol}, InsecureSkipVerify: true,
		CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	ws.SetReadLimit(16 << 10)
	switch {
	case !originOK:
		_ = ws.Close(CloseForbidden, "Origin no permitido")
		return
	case p == nil:
		_ = ws.Close(CloseUnauthorized, "ticket inválido")
		return
	case p.ExpiresAt.Before(h.o.Now()):
		_ = ws.Close(CloseUnauthorized, "token vencido")
		return
	}
	c := newConn(h, ws, p, r.URL.Query().Get("last_event_id"))
	h.add(c)
	defer h.remove(c)
	c.run(context.WithoutCancel(r.Context()))
}

// Shutdown cierra todas las conexiones con 1001 (reinicio del gateway).
func (h *Hub) Shutdown() {
	h.closeWhere(func(*conn) bool { return true }, websocket.StatusGoingAway, "reinicio")
}
