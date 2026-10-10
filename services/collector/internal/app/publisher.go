package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/spool"
)

// Sink publica un mensaje en JetStream y espera el PubAck.
type Sink interface {
	PublishMsg(ctx context.Context, m *nats.Msg) error
}

// Spooler es el spool a disco (services/collector/internal/spool).
type Spooler interface {
	Append(m *nats.Msg, records int) error
	Peek() (*nats.Msg, int, bool)
	Commit()
	Empty() bool
	Stats() spool.Stats
	Close() error
}

// Publisher desacopla la recepción UDP del bus: Enqueue nunca bloquea; los
// lotes esperan en un búfer acotado en bytes mientras NATS no responde y,
// si se llena, se descartan los nuevos y se cuentan (events.md §9.3). Al
// recuperarse se publica horus.flows.collector.data_gap.
//
// Con spool a disco (docs/architecture.md §10.15): en cuanto una
// publicación falla, o el búfer en memoria pasa de spoolAfter bytes, los
// lotes van al spool (los que esperaban en memoria también, si el spool
// estaba vacío) y se reenvían de ahí en orden, con el mismo Nats-Msg-Id,
// hasta vaciarlo; entonces se vuelve al búfer en memoria. Invariante: lo que
// queda en memoria es siempre más antiguo que lo del spool, y se publica
// antes. Lleno, el spool descarta lo más antiguo y lo cuenta.
type Publisher struct {
	sink        Sink
	maxBytes    int64
	collectorID string
	m           *Metrics
	log         *slog.Logger

	mu       sync.Mutex
	queue    []*nats.Msg
	records  []int
	bytes    int64
	wake     chan struct{}
	spool    Spooler
	spoolAt  int64 // bytes en memoria a partir de los que se usa el spool
	spooling bool
	inBytes  int64 // bytes encolados (ritmo de entrada para la autonomía)

	// hueco en curso
	gapFrom    time.Time
	gapBatches uint64
	gapRecords uint64
	gapReason  string
	down       bool

	// descartes del spool (OnDrop llega con p.mu tomado desde Enqueue: van
	// aparte y markUp los suma al hueco)
	sgMu      sync.Mutex
	sgFrom    time.Time
	sgBatches uint64
	sgRecords uint64
	sgReason  string
}

// NewPublisher crea el publicador.
func NewPublisher(sink Sink, maxBytes int64, collectorID string, m *Metrics, log *slog.Logger) *Publisher {
	return &Publisher{sink: sink, maxBytes: maxBytes, collectorID: collectorID, m: m, log: log, wake: make(chan struct{}, 1)}
}

// UseSpool activa el spool a disco: se usa al fallar el bus o cuando el
// búfer en memoria pasa de afterRatio × maxBytes. Si el spool trae lotes de
// antes del arranque, se reenvían primero.
func (p *Publisher) UseSpool(s Spooler, afterRatio float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.spool = s
	if afterRatio <= 0 || afterRatio > 1 {
		afterRatio = 0.5
	}
	p.spoolAt = int64(float64(p.maxBytes) * afterRatio)
	p.spooling = !s.Empty()
}

// SpoolDropped registra lotes que el spool ha descartado (lleno, corrupto o
// sin poder escribir) para las métricas y el evento data_gap.
func (p *Publisher) SpoolDropped(reason string, batches, records int) {
	p.m.SpoolDropped.WithLabelValues(reason).Add(float64(batches))
	p.m.SpoolDroppedRecords.WithLabelValues(reason).Add(float64(records))
	if reason == spool.DropCorrupt {
		return // ya contados al escribirse; el dato dañado no se puede publicar
	}
	p.m.Dropped.WithLabelValues(DropSpoolFull).Add(float64(records))
	p.sgMu.Lock()
	defer p.sgMu.Unlock()
	if p.sgFrom.IsZero() {
		p.sgFrom = time.Now()
	}
	p.sgReason = "spool_" + reason
	p.sgBatches += uint64(batches) //nolint:gosec // >= 0
	p.sgRecords += uint64(records) //nolint:gosec // >= 0
}

// ReportDowntime publica horus.flows.collector.data_gap con reason
// collector_down: lo que el exportador envió mientras el collector estaba
// caído (medido con la secuencia guardada). No bloquea a quien lo llama.
func (p *Publisher) ReportDowntime(exp *flowinv.Exporter, from, to time.Time, records uint64) {
	p.log.Warn("flows sent while the collector was down (UDP, lost)", "router_id", exp.RouterID, "tenant_id", exp.TenantID,
		"from", from.UTC(), "to", to.UTC(), "records", records)
	collector := uuid.NewSHA1(uuid.NameSpaceOID, []byte("horus-collector:"+p.collectorID))
	ev := flowbus.Event{Type: flowbus.TypeCollectorDataGap, Source: "horus/flows/collector",
		Entity: collector.String(), AggregateType: "collector", AggregateVersion: 1,
		Data: map[string]any{
			"collector_id": p.collectorID, "from": from.UTC().Format(flowbus.TimeFormat),
			"to": to.UTC().Format(flowbus.TimeFormat), "dropped_batches": "0",
			"dropped_records_estimated": uintString(records), "reason": "collector_down",
		}}
	go func() {
		m, err := ev.Msg()
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err = p.sink.PublishMsg(ctx, m)
			cancel()
		}
		if err != nil {
			p.log.Warn("data_gap (collector_down) not published", "error", err)
		}
	}()
}

// SpoolHealth es la sonda de /readyz: degradado mientras los lotes van al
// spool (bus caído o lento), con lo pendiente.
func (p *Publisher) SpoolHealth() error {
	p.mu.Lock()
	s, spooling := p.spool, p.spooling
	p.mu.Unlock()
	if s == nil || !spooling {
		return nil
	}
	st := s.Stats()
	return fmt.Errorf("batches going to the disk spool: %d batches (%d records, %d bytes of %d) pending", st.Batches, st.Records,
		st.Bytes, st.MaxBytes)
}

// Enqueue encola un mensaje con n registros. Devuelve false si se descartó.
func (p *Publisher) Enqueue(msg *nats.Msg, n int) bool {
	size := int64(len(msg.Data))
	p.mu.Lock()
	p.inBytes += size
	if p.spool != nil && (p.spooling || p.bytes+size > p.spoolAt) {
		p.spooling = true
		err := p.spool.Append(msg, n)
		p.mu.Unlock()
		if err != nil {
			return false // contado por SpoolDropped (write_error)
		}
		p.m.SpoolWritten.Inc()
		p.signal()
		return true
	}
	if p.bytes+size > p.maxBytes {
		if p.gapFrom.IsZero() {
			p.gapFrom = time.Now()
		}
		if p.gapReason == "" {
			p.gapReason = "local_buffer_full"
			if p.down {
				p.gapReason = "bus_unavailable"
			}
		}
		p.gapBatches++
		p.gapRecords += uint64(n) //nolint:gosec // n >= 0
		p.mu.Unlock()
		p.m.Dropped.WithLabelValues(DropBusUnavailable).Add(float64(n))
		return false
	}
	p.queue = append(p.queue, msg)
	p.records = append(p.records, n)
	p.bytes += size
	p.m.BufferBytes.Set(float64(p.bytes))
	p.mu.Unlock()
	p.signal()
	return true
}

func (p *Publisher) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Pending devuelve los mensajes y bytes en cola en memoria.
func (p *Publisher) Pending() (int, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queue), p.bytes
}

// next elige el lote más antiguo: memoria primero, luego el spool. Al vaciarse
// el spool se vuelve al búfer en memoria.
func (p *Publisher) next() (msg *nats.Msg, n int, fromSpool bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) > 0 {
		return p.queue[0], p.records[0], false
	}
	if p.spool == nil || !p.spooling {
		return nil, 0, false
	}
	if m, n, ok := p.spool.Peek(); ok {
		return m, n, true
	}
	p.spooling = false
	p.log.Info("collector spool drained: back to the memory buffer")
	return nil, 0, false
}

// Run publica en orden FIFO hasta que ctx se cancela; al cancelar intenta
// vaciar la cola durante drain y lo que no pueda publicar lo deja en el spool.
func (p *Publisher) Run(ctx context.Context, drain time.Duration) {
	backoff := 100 * time.Millisecond
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	p.observe(0)
	last := time.Now()
	for {
		select {
		case now := <-tick.C:
			p.observe(now.Sub(last))
			last = now
		default:
		}
		msg, n, fromSpool := p.next()
		if msg == nil {
			select {
			case <-ctx.Done():
				p.closeSpool()
				return
			case <-p.wake:
				continue
			case now := <-tick.C:
				p.observe(now.Sub(last))
				last = now
				continue
			}
		}
		if ctx.Err() != nil {
			if fromSpool {
				p.closeSpool() // lo del spool espera al siguiente arranque
				return
			}
			dctx, cancel := context.WithTimeout(context.Background(), drain)
			err := p.sink.PublishMsg(dctx, msg)
			cancel()
			if err != nil {
				p.spoolRest()
				p.closeSpool()
				return
			}
			p.done(fromSpool, int64(len(msg.Data)), n)
			continue
		}
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := p.sink.PublishMsg(tctx, msg)
		cancel()
		if err != nil {
			p.markDown(err)
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 5*time.Second)
			continue
		}
		backoff = 100 * time.Millisecond
		p.done(fromSpool, int64(len(msg.Data)), n)
		p.markUp(ctx)
	}
}

func (p *Publisher) done(fromSpool bool, size int64, n int) {
	if fromSpool {
		p.spool.Commit()
		p.m.SpoolReplayed.Inc()
		p.m.Batches.Inc()
		p.m.Records.Add(float64(n))
		return
	}
	p.pop(size, n)
}

func (p *Publisher) pop(size int64, n int) {
	p.mu.Lock()
	p.queue[0] = nil
	p.queue, p.records = p.queue[1:], p.records[1:]
	p.bytes -= size
	p.m.BufferBytes.Set(float64(p.bytes))
	p.mu.Unlock()
	p.m.Batches.Inc()
	p.m.Records.Add(float64(n))
}

// moveToSpoolLocked pasa la cola en memoria al spool (en orden). Solo si el
// spool está vacío, para no adelantar lotes nuevos a los que ya tiene.
func (p *Publisher) moveToSpoolLocked(force bool) int {
	if p.spool == nil || len(p.queue) == 0 || (!force && !p.spool.Empty()) {
		return 0
	}
	moved := 0
	for i, m := range p.queue {
		if err := p.spool.Append(m, p.records[i]); err != nil {
			continue // contado por SpoolDropped
		}
		moved++
		p.m.SpoolWritten.Inc()
	}
	p.queue, p.records, p.bytes = nil, nil, 0
	p.m.BufferBytes.Set(0)
	return moved
}

// spoolRest guarda en el spool lo que no se pudo publicar al apagar.
func (p *Publisher) spoolRest() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n := p.moveToSpoolLocked(true); n > 0 {
		p.log.Warn("collector stopping with the bus unavailable: pending batches kept in the spool", "batches", n)
	}
}

func (p *Publisher) closeSpool() {
	p.mu.Lock()
	s := p.spool
	p.mu.Unlock()
	if s != nil {
		_ = s.Close()
	}
}

func (p *Publisher) markDown(err error) {
	p.mu.Lock()
	first := !p.down
	p.down = true
	moved := 0
	if p.spool != nil {
		moved = p.moveToSpoolLocked(false)
		p.spooling = true
	}
	p.mu.Unlock()
	p.m.BusConnected.Set(0)
	if first {
		p.log.Warn("flows bus unavailable: buffering batches", "error", err, "spool", p.spool != nil, "moved_to_spool", moved)
	}
}

// markUp publica data_gap si hubo descartes durante la caída.
func (p *Publisher) markUp(ctx context.Context) {
	p.mu.Lock()
	wasDown := p.down
	p.down = false
	p.sgMu.Lock()
	if p.sgBatches > 0 {
		if p.gapFrom.IsZero() || p.sgFrom.Before(p.gapFrom) {
			p.gapFrom = p.sgFrom
		}
		p.gapBatches += p.sgBatches
		p.gapRecords += p.sgRecords
		p.gapReason = p.sgReason
		p.sgFrom, p.sgBatches, p.sgRecords, p.sgReason = time.Time{}, 0, 0, ""
	}
	p.sgMu.Unlock()
	var gap *flowbus.Event
	if p.gapBatches > 0 && len(p.queue) == 0 && !p.spooling {
		collector := uuid.NewSHA1(uuid.NameSpaceOID, []byte("horus-collector:"+p.collectorID))
		gap = &flowbus.Event{Type: flowbus.TypeCollectorDataGap, Source: "horus/flows/collector",
			Entity: collector.String(), AggregateType: "collector", AggregateVersion: 1,
			Data: map[string]any{
				"collector_id": p.collectorID, "from": p.gapFrom.UTC().Format(flowbus.TimeFormat),
				"to": time.Now().UTC().Format(flowbus.TimeFormat), "dropped_batches": uintString(p.gapBatches),
				"dropped_records_estimated": uintString(p.gapRecords), "reason": p.gapReason,
			}}
		p.gapFrom, p.gapBatches, p.gapRecords, p.gapReason = time.Time{}, 0, 0, ""
	}
	p.mu.Unlock()
	p.m.BusConnected.Set(1)
	if wasDown {
		p.log.Info("flows bus available again")
	}
	if gap != nil {
		m, err := gap.Msg()
		if err == nil {
			err = p.sink.PublishMsg(ctx, m)
		}
		if err != nil {
			p.log.Warn("data_gap event not published", "error", err)
		}
	}
}

// observe actualiza las métricas del spool y la autonomía: cuánto tiempo más
// se puede seguir sin bus al ritmo de entrada actual (memoria y spool).
func (p *Publisher) observe(elapsed time.Duration) {
	p.mu.Lock()
	in := p.inBytes
	p.inBytes = 0
	free := p.maxBytes - p.bytes
	s := p.spool
	spooling := p.spooling
	p.mu.Unlock()
	p.m.Spooling.Set(b2f(spooling))
	if elapsed <= 0 {
		return
	}
	rate := float64(in) / elapsed.Seconds() // bytes/s encolados
	p.m.IngressBytesRate.Set(rate)
	autonomy := func(free int64) float64 {
		if rate <= 0 {
			return -1
		}
		return float64(max(free, 0)) / rate
	}
	p.m.Autonomy.WithLabelValues("memory").Set(autonomy(free))
	if s == nil {
		return
	}
	st := s.Stats()
	p.m.SpoolBytes.Set(float64(st.Bytes))
	p.m.SpoolBatches.Set(float64(st.Batches))
	p.m.SpoolRecords.Set(float64(st.Records))
	age := 0.0
	if !st.Oldest.IsZero() && st.Batches > 0 {
		age = time.Since(st.Oldest).Seconds()
	}
	p.m.SpoolOldestAge.Set(age)
	p.m.Autonomy.WithLabelValues("spool").Set(autonomy(st.MaxBytes - st.Bytes))
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
