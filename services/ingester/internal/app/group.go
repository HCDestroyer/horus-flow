package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Escritura agrupada (FLOW, isp10k): varios lotes de TLM_FLOWS (≈ 500
// registros cada uno) se acumulan en un solo INSERT de hasta GroupRows filas
// o GroupWait de antigüedad. Con un INSERT por lote, ClickHouse creaba una
// parte por cada 500 filas en flows_raw y en cada una de las vistas
// materializadas, y ese era el cuello de botella medido (tests/load/REPORT.md).
//
// Garantías (sin pérdida ni duplicados):
//
//   - Los mensajes de un grupo se confirman (DoubleAck) solo después de que el
//     INSERT del grupo haya terminado bien.
//   - Cada grupo lleva un token propio (insert_deduplication_token). Antes del
//     INSERT se guarda en el Ledger (KV de NATS) qué batch_id contiene. Si el
//     proceso muere entre el INSERT y las confirmaciones, JetStream reentrega
//     los mensajes; al arrancar se cargan los grupos pendientes del Ledger y los
//     mensajes reentregados se reagrupan con su token original: si el INSERT ya
//     se había hecho, ClickHouse lo descarta por token; si no, se inserta. Una
//     confirmación parcial (unos mensajes sí y otros no) implica que el INSERT
//     se hizo, así que reinsertar el subconjunto con el mismo token también es
//     correcto.
//   - Un batch_id que ya está en un grupo pendiente (reentrega mientras
//     ClickHouse no responde) o que se insertó hace poco (confirmación perdida)
//     no vuelve a insertarse: el mensaje se confirma junto al original.
//
// Supuesto: un único proceso consume el durable flows-ingester (los workers
// son goroutines). Con varios procesos, un grupo pendiente podría repartirse
// entre ellos al recuperarse; ver docs/architecture.md §10.1.

// Ledger guarda la composición de los grupos en curso (token → batch_id).
type Ledger interface {
	Put(ctx context.Context, token string, batchIDs []string) error
	Delete(ctx context.Context, token string) error
	// Load devuelve los grupos que quedaron sin cerrar (arranque).
	Load(ctx context.Context) (map[string][]string, error)
}

// BusMsg es lo que el consumidor usa de un mensaje de JetStream.
type BusMsg interface {
	Data() []byte
	Headers() nats.Header
	Subject() string
	Metadata() (*jetstream.MsgMetadata, error)
	DoubleAck(ctx context.Context) error
	InProgress() error
	NakWithDelay(delay time.Duration) error
	Term() error
}

// GroupOptions configura la escritura agrupada.
type GroupOptions struct {
	// Rows es el umbral de filas de un INSERT (HORUS_INGESTER_INSERT_ROWS).
	Rows int
	// Wait es la antigüedad máxima de un grupo (HORUS_INGESTER_INSERT_WAIT).
	Wait time.Duration
	// Flushers son los INSERT de grupos que pueden estar en curso a la vez.
	Flushers int
	// RecoveryWait es cuánto se espera al resto de un grupo pendiente del
	// Ledger desde el último mensaje recibido de ese grupo.
	RecoveryWait time.Duration
	// SeenTTL es cuánto se recuerda un batch_id ya insertado.
	SeenTTL time.Duration
}

func (o GroupOptions) withDefaults() GroupOptions {
	if o.Rows <= 0 {
		o.Rows = 50000
	}
	if o.Wait <= 0 {
		o.Wait = time.Second
	}
	if o.Flushers <= 0 {
		o.Flushers = 2
	}
	if o.RecoveryWait <= 0 {
		o.RecoveryWait = 30 * time.Second
	}
	if o.SeenTTL <= 0 {
		o.SeenTTL = 10 * time.Minute
	}
	return o
}

// prepared es un mensaje ya decodificado y atribuido.
type prepared struct {
	msg     BusMsg
	batchID string
	rows    []Row
}

type group struct {
	token    string
	recovery bool
	msgs     []BusMsg
	ids      []string
	rows     []Row
	started  time.Time
	last     time.Time
	reason   string
}

// Prepare decodifica y atribuye un mensaje (sin insertar).
func (c *Consumer) Prepare(data []byte, headers nats.Header) (string, []Row, outcome, error) {
	fb, err := flowpb.UnmarshalFlowBatch(data)
	if err != nil {
		return "", nil, outTerm, err
	}
	if c.Paused != nil && c.Paused(fb.TenantID) {
		// ISP suspendido con pause_ingest: el lote se confirma sin guardar.
		c.M.Batches.WithLabelValues("tenant_suspended").Inc()
		return fb.BatchID, nil, outAck, nil
	}
	rows, err := c.Proc.Rows(fb, headers.Get(flowbus.HeaderTenant))
	switch {
	case errors.Is(err, ErrPermanent):
		return fb.BatchID, nil, outTerm, err
	case err != nil:
		return fb.BatchID, nil, outNak, err
	}
	return fb.BatchID, rows, outAck, nil
}

// grouper acumula mensajes preparados y decide cuándo se escribe cada grupo.
type grouper struct {
	c    *Consumer
	opts GroupOptions

	mu       sync.Mutex
	cur      *group
	pending  map[string]*group // batch_id → grupo aún sin confirmar
	recover  map[string]string // batch_id → token del Ledger (grupos de antes del arranque)
	recGroup map[string]*group // token → grupo en recuperación
	seen     map[string]time.Time
}

func newGrouper(c *Consumer, opts GroupOptions, recovered map[string][]string) *grouper {
	g := &grouper{c: c, opts: opts.withDefaults(), pending: map[string]*group{}, recover: map[string]string{},
		recGroup: map[string]*group{}, seen: map[string]time.Time{}}
	for token, ids := range recovered {
		for _, id := range ids {
			g.recover[id] = token
		}
	}
	return g
}

// skip resuelve antes de decodificar las reentregas de un batch_id ya
// insertado (se confirman) o ya en un grupo pendiente (se ignoran).
func (g *grouper) skip(batchID string, m BusMsg, now time.Time) bool {
	if batchID == "" {
		return false
	}
	g.mu.Lock()
	at, seen := g.seen[batchID]
	_, pending := g.pending[batchID]
	g.mu.Unlock()
	switch {
	case seen && now.Sub(at) < g.opts.SeenTTL:
		g.c.M.Batches.WithLabelValues("duplicate").Inc()
		_ = m.DoubleAck(context.Background())
		return true
	case pending:
		g.c.M.Batches.WithLabelValues("duplicate").Inc()
		return true
	}
	return false
}

// add incorpora un mensaje preparado; devuelve los grupos listos para escribir.
func (g *grouper) add(p prepared, now time.Time) []*group {
	g.mu.Lock()
	defer g.mu.Unlock()
	if at, ok := g.seen[p.batchID]; ok && now.Sub(at) < g.opts.SeenTTL {
		// Ya insertado (confirmación perdida o reentrega tardía): solo se confirma.
		g.c.M.Batches.WithLabelValues("duplicate").Inc()
		_ = p.msg.DoubleAck(context.Background())
		return nil
	}
	if _, ok := g.pending[p.batchID]; ok {
		// Reentrega de un lote que ya está en un grupo sin confirmar (ClickHouse
		// lento o caído): no se guarda la copia (memoria) ni se confirma; la
		// confirmación del original cubre la secuencia y una copia posterior
		// cae en seen.
		g.c.M.Batches.WithLabelValues("duplicate").Inc()
		return nil
	}
	if token, ok := g.recover[p.batchID]; ok {
		gr := g.recGroup[token]
		if gr == nil {
			gr = &group{token: token, recovery: true, started: now, reason: "recovery"}
			g.recGroup[token] = gr
		}
		g.attach(gr, p, now)
		delete(g.recover, p.batchID)
		if !g.waitingFor(token) {
			delete(g.recGroup, token)
			return []*group{gr}
		}
		return nil
	}
	if g.cur == nil {
		g.cur = &group{started: now}
	}
	g.attach(g.cur, p, now)
	if len(g.cur.rows) >= g.opts.Rows {
		gr := g.cur
		gr.reason = "rows"
		g.cur = nil
		return []*group{gr}
	}
	return nil
}

func (g *grouper) attach(gr *group, p prepared, now time.Time) {
	gr.msgs = append(gr.msgs, p.msg)
	gr.ids = append(gr.ids, p.batchID)
	gr.rows = append(gr.rows, p.rows...)
	gr.last = now
	g.pending[p.batchID] = gr
}

// waitingFor dice si quedan batch_id del grupo token por llegar.
func (g *grouper) waitingFor(token string) bool {
	for _, t := range g.recover {
		if t == token {
			return true
		}
	}
	return false
}

// tick devuelve los grupos que han cumplido su espera.
func (g *grouper) tick(now time.Time) []*group {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []*group
	if g.cur != nil && now.Sub(g.cur.started) >= g.opts.Wait {
		g.cur.reason = "time"
		out = append(out, g.cur)
		g.cur = nil
	}
	for token, gr := range g.recGroup {
		if now.Sub(gr.last) >= g.opts.RecoveryWait {
			// El resto ya estaba confirmado o el stream lo descartó.
			for id, t := range g.recover {
				if t == token {
					delete(g.recover, id)
				}
			}
			delete(g.recGroup, token)
			out = append(out, gr)
		}
	}
	for id, at := range g.seen {
		if now.Sub(at) >= g.opts.SeenTTL {
			delete(g.seen, id)
		}
	}
	return out
}

// drain devuelve el grupo en curso (apagado).
func (g *grouper) drain() []*group {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []*group
	if g.cur != nil {
		g.cur.reason = "time"
		out = append(out, g.cur)
		g.cur = nil
	}
	return out
}

// done cierra un grupo: insertado (ok) o abandonado (apagado).
func (g *grouper) done(gr *group, inserted bool, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range gr.ids {
		delete(g.pending, id)
		if inserted {
			g.seen[id] = now
		}
	}
}

// keepAlive alarga ack_wait de los mensajes de los grupos sin confirmar.
func (g *grouper) keepAlive() {
	g.mu.Lock()
	groups := map[*group]struct{}{}
	for _, gr := range g.pending {
		groups[gr] = struct{}{}
	}
	g.mu.Unlock()
	for gr := range groups {
		for _, m := range gr.msgs {
			_ = m.InProgress()
		}
	}
}

// flush escribe un grupo: Ledger, INSERT con reintentos y confirmaciones.
// Devuelve false si se abandonó por apagado (los mensajes se reentregarán).
func (c *Consumer) flush(ctx context.Context, stop <-chan struct{}, g *grouper, gr *group) bool {
	if len(gr.msgs) == 0 {
		return true
	}
	if gr.token == "" {
		gr.token = uuid.NewString()
	}
	backoff := 200 * time.Millisecond
	wait := func() bool {
		for _, m := range gr.msgs {
			_ = m.InProgress()
		}
		select {
		case <-stop:
			return false
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 10*time.Second)
		return true
	}
	if c.Ledger != nil && !gr.recovery {
		// El Ledger solo protege de duplicados tras una caída del proceso: si
		// no responde tras varios intentos se inserta igualmente (disponibilidad
		// antes que la deduplicación de un caso raro).
		for attempt := 1; ; attempt++ {
			err := c.Ledger.Put(ctx, gr.token, gr.ids)
			if err == nil {
				break
			}
			if attempt >= 5 {
				c.M.Batches.WithLabelValues("ledger_unavailable").Inc()
				c.Log.Warn("ingester ledger unavailable: inserting without crash protection", "token", gr.token, "error", err)
				break
			}
			if !wait() {
				g.done(gr, false, time.Now())
				return false
			}
		}
		backoff = 200 * time.Millisecond
	}
	start := time.Now()
	for {
		err := c.Ins.Insert(ctx, gr.token, gr.rows)
		if err == nil {
			break
		}
		c.M.Batches.WithLabelValues("insert_retry").Inc()
		c.Log.Warn("flows_raw insert failed: retrying", "token", gr.token, "batches", len(gr.ids), "rows", len(gr.rows), "error", err)
		if !wait() {
			g.done(gr, false, time.Now())
			return false
		}
	}
	c.M.InsertDur.Observe(time.Since(start).Seconds())
	if c.M.InsertRows != nil {
		c.M.InsertRows.Observe(float64(len(gr.rows)))
		c.M.Flushes.WithLabelValues(gr.reason).Inc()
	}
	counts := map[string]float64{}
	for i := range gr.rows {
		counts[gr.rows[i].AttributionStatus]++
	}
	for s, n := range counts {
		c.M.Rows.WithLabelValues(s).Add(n)
	}
	g.done(gr, true, time.Now())
	acked := true
	for _, m := range gr.msgs {
		if err := m.DoubleAck(ctx); err != nil {
			acked = false
			c.Log.Warn("flow batch ack failed after insert", "token", gr.token, "error", err)
			continue
		}
		c.M.Batches.WithLabelValues("ok").Inc()
	}
	// Con alguna confirmación perdida el grupo queda en el Ledger y en seen:
	// la reentrega se confirma sin insertar.
	if acked && c.Ledger != nil {
		if err := c.Ledger.Delete(ctx, gr.token); err != nil {
			c.Log.Debug("ingester ledger delete failed", "token", gr.token, "error", err)
		}
	}
	return true
}

// msgContext añade al contexto de log el lote (event_id = batch_id), el ISP y
// el router del mensaje (docs/observability.md §3).
func msgContext(ctx context.Context, m BusMsg) context.Context {
	h := m.Headers()
	if id := h.Get(flowbus.HeaderMsgID); id != "" {
		ctx = observability.WithEventID(ctx, id)
	}
	if t := h.Get(flowbus.HeaderTenant); t != "" && t != flowbus.TenantPlatform {
		ctx = observability.WithTenant(ctx, t)
	}
	if r, ok := strings.CutPrefix(m.Subject(), flowbus.SubjectBatchPrefix); ok {
		ctx = observability.WithRouter(ctx, r)
	}
	return ctx
}

func (c *Consumer) reject(ctx context.Context, m BusMsg, out outcome, err error) {
	ctx = msgContext(ctx, m)
	switch out {
	case outNak:
		c.M.Batches.WithLabelValues("retry").Inc()
		c.Log.WarnContext(ctx, "flow batch will be retried", "subject", m.Subject(), "error", err)
		if md, mErr := m.Metadata(); mErr == nil && md.NumDelivered >= 5 {
			c.terminate(ctx, m, err)
			return
		}
		_ = m.NakWithDelay(2 * time.Second)
	case outTerm:
		c.terminate(ctx, m, err)
	}
}

// runGrouped es el bucle de Run con escritura agrupada.
func (c *Consumer) runGrouped(ctx context.Context, next func() (BusMsg, error), stopIter func()) error {
	opts := c.Group.withDefaults()
	var recovered map[string][]string
	if c.Ledger != nil {
		r, err := c.Ledger.Load(ctx)
		if err != nil {
			c.Log.Warn("ingester ledger load failed: in-flight groups from a previous run are not recovered", "error", err)
		} else if len(r) > 0 {
			c.Log.Info("ingester recovering in-flight insert groups", "groups", len(r))
			recovered = r
		}
	}
	g := newGrouper(c, opts, recovered)
	bg := context.WithoutCancel(ctx)
	stop := ctx.Done()
	workers := max(c.Workers, 1)

	in := make(chan BusMsg, workers)
	ready := make(chan *group, opts.Flushers)
	var prepWG, flushWG sync.WaitGroup
	for range workers {
		prepWG.Add(1)
		go func() {
			defer prepWG.Done()
			for m := range in {
				// Nats-Msg-Id = batch_id: las reentregas se descartan sin decodificar.
				if g.skip(m.Headers().Get(flowbus.HeaderMsgID), m, time.Now()) {
					continue
				}
				id, rows, out, err := c.Prepare(m.Data(), m.Headers())
				if out != outAck {
					c.reject(bg, m, out, err)
					continue
				}
				if len(rows) == 0 {
					// Lote sin filas que guardar (todo descartado): se confirma ya.
					c.M.Batches.WithLabelValues("ok").Inc()
					_ = m.DoubleAck(bg)
					continue
				}
				for _, gr := range g.add(prepared{msg: m, batchID: id, rows: rows}, time.Now()) {
					ready <- gr
				}
			}
		}()
	}
	for range opts.Flushers {
		flushWG.Add(1)
		go func() {
			defer flushWG.Done()
			for gr := range ready {
				c.flush(bg, stop, g, gr)
			}
		}()
	}
	// InProgress de los grupos sin confirmar en su propia goroutine: el bucle
	// de abajo puede quedarse esperando a un flusher mientras ClickHouse no
	// responde.
	go func() {
		keep := time.NewTicker(15 * time.Second)
		defer keep.Stop()
		for {
			select {
			case <-stop:
				return
			case <-keep.C:
				g.keepAlive()
			}
		}
	}()
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		t := time.NewTicker(min(opts.Wait/4, 250*time.Millisecond))
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				for _, gr := range g.tick(now) {
					select {
					case ready <- gr:
					case <-stop:
						g.done(gr, false, now)
						return
					}
				}
			}
		}
	}()
	go func() {
		<-stop
		stopIter()
	}()
	for {
		m, err := next()
		if err != nil {
			break
		}
		in <- m
	}
	close(in)
	prepWG.Wait()
	<-tickDone
	// Apagado: el grupo en curso se intenta escribir una vez (si ClickHouse no
	// responde, sus mensajes se reentregan al volver).
	for _, gr := range g.drain() {
		ready <- gr
	}
	close(ready)
	flushWG.Wait()
	return nil
}
