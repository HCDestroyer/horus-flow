package realtime

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/natsx"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
)

// Límites por conexión (api.md §4.9).
const (
	maxSubscriptions = 50
	maxInPerSecond   = 20
	maxInBurst       = 50
	outQueue         = 256
	writeTimeout     = 10 * time.Second
	expiringNotice   = 60 * time.Second
)

type conn struct {
	h  *Hub
	ws *websocket.Conn

	tenant  uuid.UUID
	userID  uuid.UUID
	sid     uuid.UUID
	kioskID uuid.UUID

	mu        sync.Mutex
	p         *authz.Principal
	subs      map[string]bool
	kioskTops []string
	kioskPII  bool
	resume    uuid.UUID
	pending   map[string]stateMsg // topic|key → estado coalescido
	overflows []time.Time
	warned    bool

	out       chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func newConn(h *Hub, ws *websocket.Conn, p *authz.Principal, lastEventID string) *conn {
	c := &conn{h: h, ws: ws, p: p, tenant: p.TenantID, userID: p.UserID, sid: p.SessionID, kioskID: p.KioskID,
		subs: map[string]bool{}, pending: map[string]stateMsg{}, out: make(chan []byte, outQueue), done: make(chan struct{})}
	if id, err := uuid.Parse(lastEventID); err == nil {
		c.resume = id
	}
	return c
}

func (c *conn) principal() *authz.Principal {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.p
}

func (c *conn) isKiosk() bool { return c.kioskID != uuid.Nil }

func marshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// send encola sin bloquear; devuelve false si la cola está llena.
func (c *conn) send(msg []byte) bool {
	select {
	case <-c.done:
		return false
	case c.out <- msg:
		return true
	default:
		return false
	}
}

func (c *conn) close(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.ws.Close(code, reason)
	})
}

func (c *conn) errorMsg(id, code, msg string) []byte {
	return marshal(map[string]any{"type": "error", "id": id, "code": code, "message": msg})
}

// run atiende la conexión hasta que se cierra.
func (c *conn) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.writer(ctx)
	go c.watch(ctx)
	if !c.h.busOK.Load() {
		c.send(marshal(map[string]any{"type": "realtime_status", "status": "degraded", "reason": "event_bus_unavailable"}))
	}
	tokens, last := float64(maxInBurst), time.Now()
	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			c.close(websocket.StatusNormalClosure, "")
			return
		}
		now := time.Now()
		tokens = min(maxInBurst, tokens+now.Sub(last).Seconds()*maxInPerSecond)
		last = now
		if tokens < 1 {
			c.close(CloseLimit, "demasiados mensajes")
			return
		}
		tokens--
		if typ != websocket.MessageText {
			c.close(CloseInvalidMessage, "mensaje inválido")
			return
		}
		var m struct {
			Type        string `json:"type"`
			ID          string `json:"id"`
			Topic       string `json:"topic"`
			AccessToken string `json:"access_token"`
		}
		if json.Unmarshal(data, &m) != nil || len(m.ID) > 32 {
			c.close(CloseInvalidMessage, "mensaje inválido")
			return
		}
		switch m.Type {
		case "subscribe":
			c.subscribe(ctx, m.ID, m.Topic)
		case "unsubscribe":
			c.mu.Lock()
			delete(c.subs, m.Topic)
			c.mu.Unlock()
			c.send(marshal(map[string]any{"type": "ack", "id": m.ID, "topic": m.Topic}))
		case "auth":
			if !c.renew(m.AccessToken) {
				return
			}
			c.send(marshal(map[string]any{"type": "ack", "id": m.ID, "topic": ""}))
		case "ping":
			c.send(marshal(map[string]any{"type": "pong", "id": m.ID}))
		default:
			c.close(CloseInvalidMessage, "tipo de mensaje desconocido")
			return
		}
	}
}

// renew aplica la renovación en banda: mismo tid y misma sesión o kiosco.
func (c *conn) renew(tok string) bool {
	p, err := c.h.o.Verifier.Verify(tok)
	if err != nil {
		c.close(CloseUnauthorized, "token inválido")
		return false
	}
	if p.TenantID != c.tenant || p.SessionID != c.sid || p.KioskID != c.kioskID || p.UserID != c.userID {
		c.close(CloseForbidden, "renovación con otro tid o sesión")
		return false
	}
	c.mu.Lock()
	c.p, c.warned = p, false
	for t := range c.subs {
		if !c.allowedLocked(t) {
			delete(c.subs, t)
			c.send(marshal(map[string]any{"type": "unsubscribed", "topic": t, "reason": "forbidden"}))
		}
	}
	c.mu.Unlock()
	return true
}

// allowedLocked indica si el principal vigente puede usar topic (sin
// consultar dashboards; c.mu tomado).
func (c *conn) allowedLocked(topic string) bool {
	if c.isKiosk() {
		return slices.Contains(c.kioskTops, topic)
	}
	def, ok := LookupTopic(topic)
	if !ok {
		return false
	}
	if c.tenant == uuid.Nil {
		return topic == "me" || topic == "system"
	}
	return def.Permission == "" || c.p.Has(def.Permission)
}

func (c *conn) subscribe(ctx context.Context, id, topic string) {
	def, ok := LookupTopic(topic)
	if !ok {
		c.send(c.errorMsg(id, "INVALID_TOPIC", "topic desconocido"))
		return
	}
	allowed, denyMsg := c.authorize(ctx, topic, def)
	if !allowed {
		c.send(c.errorMsg(id, "PERMISSION_DENIED", denyMsg))
		return
	}
	c.mu.Lock()
	if len(c.subs) >= maxSubscriptions && !c.subs[topic] {
		c.mu.Unlock()
		c.send(c.errorMsg(id, "LIMIT_EXCEEDED", "máximo 50 suscripciones"))
		return
	}
	c.subs[topic] = true
	resume := c.resume
	c.mu.Unlock()
	c.send(marshal(map[string]any{"type": "ack", "id": id, "topic": topic}))
	switch {
	case topic == "system":
		status := "ok"
		if !c.h.busOK.Load() {
			status = "degraded"
		}
		c.send(marshal(stateMsg{Type: "state", Topic: "system", Key: "status", Time: c.h.o.Now().UTC().Format(natsx.TimeFormat),
			Data: marshal(map[string]any{"realtime_status": status})}))
	case def.Class == ClassState:
		for _, st := range c.h.snapshot(c.tenant, topic) {
			c.send(marshal(st))
		}
	case resume != uuid.Nil && c.tenant != uuid.Nil:
		// Reanudación: lo perdido dentro de la ventana o la orden de snapshot.
		items, ok := c.h.ringFor(c.tenant).since(resume, c.h.o.Now())
		if !ok {
			c.send(marshal(map[string]any{"type": "resync_required", "topic": topic}))
			return
		}
		for _, it := range items {
			if slices.Contains(it.topics, topic) || (it.topics[0] == "dashboard.*" && def.Name != "me" && isDash(topic)) {
				c.deliverOne(topic, it.ev)
			}
		}
	}
}

func isDash(topic string) bool {
	_, ok := DashboardTopic(topic)
	return ok
}

// authorize decide la suscripción sin revelar si un recurso de otro ISP existe.
func (c *conn) authorize(ctx context.Context, topic string, def TopicDef) (bool, string) {
	p := c.principal()
	if c.isKiosk() {
		acc, ok := c.h.o.Dashboards()
		if !ok {
			return false, "kioscos no disponibles"
		}
		tops, err := acc.KioskTopics(ctx, c.tenant, c.kioskID)
		if err != nil {
			return false, "kiosco no disponible"
		}
		st, err := c.kioskStatus(ctx)
		c.mu.Lock()
		c.kioskTops = tops
		c.kioskPII = err == nil && st.ShowPersonalData
		c.mu.Unlock()
		if !slices.Contains(tops, topic) {
			return false, "el topic no pertenece a los dashboards del kiosco"
		}
		return true, ""
	}
	if c.tenant == uuid.Nil {
		return topic == "me" || topic == "system", "este topic exige un token de ISP"
	}
	if id, ok := DashboardTopic(topic); ok {
		acc, ok := c.h.o.Dashboards()
		if !ok {
			return false, "dashboards no disponibles"
		}
		can, err := acc.CanRead(ctx, c.tenant, c.userID, p.Perms, id)
		if err != nil || !can {
			return false, "sin acceso al dashboard"
		}
		return true, ""
	}
	if def.Permission != "" && !p.Has(def.Permission) {
		return false, "falta " + def.Permission
	}
	return true, ""
}

func (c *conn) kioskStatus(ctx context.Context) (*authapi.KioskStatus, error) {
	if c.h.o.Kiosks == nil {
		return nil, context.Canceled
	}
	return c.h.o.Kiosks.CheckKiosk(ctx, c.tenant, c.kioskID)
}

func (c *conn) unsubscribeAll(reason string) {
	c.mu.Lock()
	topics := make([]string, 0, len(c.subs))
	for t := range c.subs {
		topics = append(topics, t)
	}
	c.subs = map[string]bool{}
	c.mu.Unlock()
	for _, t := range topics {
		c.send(marshal(map[string]any{"type": "unsubscribed", "topic": t, "reason": reason}))
	}
}

// deliver entrega un evento a los topics suscritos que lo admiten.
func (c *conn) deliver(topics []string, ev *outEvent) {
	c.mu.Lock()
	var targets []string
	for _, t := range topics {
		if t == "dashboard.*" {
			for s := range c.subs {
				if isDash(s) {
					targets = append(targets, s)
					break // una vez por conexión
				}
			}
			continue
		}
		if c.subs[t] {
			targets = append(targets, t)
		}
	}
	c.mu.Unlock()
	for _, t := range targets {
		c.deliverOne(t, ev)
	}
}

func (c *conn) deliverOne(topic string, ev *outEvent) {
	def, _ := LookupTopic(topic)
	p := c.principal()
	if topic == "me" {
		f := ev.fields
		uid := f.UserID
		if f.RecipientUserID != nil {
			uid = f.RecipientUserID
		}
		if c.isKiosk() || uid == nil || *uid != c.userID {
			return
		}
	}
	if def.SiteFilter && !c.isKiosk() && !p.TenantWide(def.Permission) {
		if ev.fields.SiteID == nil || !p.HasScope(def.Permission, "site:"+ev.fields.SiteID.String()) {
			return
		}
	}
	proj := ev.proj
	c.mu.Lock()
	pii := c.kioskPII
	c.mu.Unlock()
	if c.isKiosk() && !pii {
		proj.Data = StripPII(proj.Data, def.PII)
		proj.Actor = nil
	}
	msg := marshal(map[string]any{"type": "event", "topic": topic, "event": proj})
	if len(msg) > 64<<10 {
		proj.Data = json.RawMessage(`{}`)
		msg = marshal(map[string]any{"type": "event", "topic": topic, "event": proj})
	}
	if !c.send(msg) {
		c.overflow(topic)
	}
}

// overflow aplica el backpressure de topics evento (api.md §4.7).
func (c *conn) overflow(topic string) {
	now := time.Now()
	c.mu.Lock()
	c.overflows = append(c.overflows, now)
	var recent []time.Time
	for _, t := range c.overflows {
		if now.Sub(t) < 5*time.Minute {
			recent = append(recent, t)
		}
	}
	c.overflows = recent
	n := len(recent)
	c.mu.Unlock()
	if n >= 3 {
		c.close(CloseSlowClient, "cliente lento")
		return
	}
	// Vacía la cola y pide resincronizar el topic.
	for len(c.out) > 0 {
		select {
		case <-c.out:
		default:
		}
	}
	c.send(marshal(map[string]any{"type": "resync_required", "topic": topic}))
}

// pushState coalesce un estado (≤ 1 actualización/s por topic y clave).
func (c *conn) pushState(st stateMsg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.subs[st.Topic] {
		return
	}
	c.pending[st.Topic+"|"+st.Key] = st
}

func (c *conn) flushStates() {
	c.mu.Lock()
	pend := c.pending
	c.pending = map[string]stateMsg{}
	c.mu.Unlock()
	for _, st := range pend {
		c.send(marshal(st))
	}
}

// writer escribe la cola, coalesce estados cada segundo y envía heartbeats.
func (c *conn) writer(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastWrite := time.Now()
	for {
		select {
		case <-c.done:
			return
		case <-ctx.Done():
			return
		case msg := <-c.out:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.ws.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.close(CloseSlowClient, "escritura vencida")
				return
			}
			lastWrite = time.Now()
		case <-tick.C:
			c.flushStates()
			if time.Since(lastWrite) >= c.h.o.Heartbeat {
				c.send(marshal(map[string]any{"type": "heartbeat", "time": c.h.o.Now().UTC().Format(natsx.TimeFormat)}))
				lastWrite = time.Now()
			}
		}
	}
}

// watch comprueba vencimiento del token y revocación (4401/4409 en < 5 s).
func (c *conn) watch(ctx context.Context) {
	t := time.NewTicker(c.h.o.RevokeEvery)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ctx.Done():
			return
		case <-t.C:
		}
		p := c.principal()
		now := c.h.o.Now()
		if now.After(p.ExpiresAt) {
			c.close(CloseUnauthorized, "token vencido sin renovar")
			return
		}
		c.mu.Lock()
		warn := !c.warned && p.ExpiresAt.Sub(now) <= expiringNotice
		if warn {
			c.warned = true
		}
		c.mu.Unlock()
		if warn {
			c.send(marshal(map[string]any{"type": "auth_expiring", "expires_at": p.ExpiresAt.UTC().Format(natsx.TimeFormat)}))
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		revoked := c.revoked(cctx, p)
		cancel()
		if revoked {
			c.close(CloseRevoked, "revocado")
			return
		}
	}
}

func (c *conn) revoked(ctx context.Context, p *authz.Principal) bool {
	if c.isKiosk() {
		st, err := c.kioskStatus(ctx)
		return err == nil && !st.Active
	}
	if p.Type != authz.TypeUser || c.h.o.Sessions == nil {
		return false
	}
	res, err := c.h.o.Sessions.CheckSession(ctx, authapi.CheckSessionRequest{SID: p.SessionID, UserID: p.UserID, TenantID: p.TenantID,
		ViaPlatform: p.ViaPlatform})
	if err != nil {
		return false
	}
	return !res.Active || (p.TenantID != uuid.Nil && (!res.MembershipActive || !res.TenantActive))
}
