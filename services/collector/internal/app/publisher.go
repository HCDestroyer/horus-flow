package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
)

// Sink publica un mensaje en JetStream y espera el PubAck.
type Sink interface {
	PublishMsg(ctx context.Context, m *nats.Msg) error
}

// Publisher desacopla la recepción UDP del bus: Enqueue nunca bloquea; los
// lotes esperan en un búfer acotado en bytes mientras NATS no responde y,
// si se llena, se descartan los nuevos y se cuentan (events.md §9.3). Al
// recuperarse se publica horus.flows.collector.data_gap.
type Publisher struct {
	sink        Sink
	maxBytes    int64
	collectorID string
	m           *Metrics
	log         *slog.Logger

	mu      sync.Mutex
	queue   []*nats.Msg
	records []int
	bytes   int64
	wake    chan struct{}

	// hueco en curso
	gapFrom    time.Time
	gapBatches uint64
	gapRecords uint64
	gapReason  string
	down       bool
}

// NewPublisher crea el publicador.
func NewPublisher(sink Sink, maxBytes int64, collectorID string, m *Metrics, log *slog.Logger) *Publisher {
	return &Publisher{sink: sink, maxBytes: maxBytes, collectorID: collectorID, m: m, log: log, wake: make(chan struct{}, 1)}
}

// Enqueue encola un mensaje con n registros. Devuelve false si se descartó.
func (p *Publisher) Enqueue(msg *nats.Msg, n int) bool {
	size := int64(len(msg.Data))
	p.mu.Lock()
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
	select {
	case p.wake <- struct{}{}:
	default:
	}
	return true
}

// Pending devuelve los mensajes y bytes en cola.
func (p *Publisher) Pending() (int, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queue), p.bytes
}

// Run publica en orden FIFO hasta que ctx se cancela; al cancelar intenta
// vaciar la cola durante drain.
func (p *Publisher) Run(ctx context.Context, drain time.Duration) {
	backoff := 100 * time.Millisecond
	for {
		p.mu.Lock()
		var msg *nats.Msg
		var n int
		if len(p.queue) > 0 {
			msg, n = p.queue[0], p.records[0]
		}
		p.mu.Unlock()
		if msg == nil {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
				continue
			}
		}
		pctx := ctx
		if ctx.Err() != nil {
			var cancel context.CancelFunc
			pctx, cancel = context.WithTimeout(context.Background(), drain)
			err := p.sink.PublishMsg(pctx, msg)
			cancel()
			if err != nil {
				return
			}
			p.pop(int64(len(msg.Data)), n)
			continue
		}
		tctx, cancel := context.WithTimeout(pctx, 5*time.Second)
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
		p.pop(int64(len(msg.Data)), n)
		p.markUp(ctx)
	}
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

func (p *Publisher) markDown(err error) {
	p.mu.Lock()
	first := !p.down
	p.down = true
	p.mu.Unlock()
	p.m.BusConnected.Set(0)
	if first {
		p.log.Warn("flows bus unavailable: buffering batches", "error", err)
	}
}

// markUp publica data_gap si hubo descartes durante la caída.
func (p *Publisher) markUp(ctx context.Context) {
	p.mu.Lock()
	wasDown := p.down
	p.down = false
	var gap *flowbus.Event
	if p.gapBatches > 0 && len(p.queue) == 0 {
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
