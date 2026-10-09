package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

// Inserter escribe filas de un lote (flows_raw).
type Inserter interface {
	Insert(ctx context.Context, batchID string, rows []Row) error
}

// Metrics del ingester.
type Metrics struct {
	Batches   *prometheus.CounterVec
	Rows      *prometheus.CounterVec
	InsertDur prometheus.Histogram
	Lag       prometheus.Gauge
}

// NewMetrics crea y registra las métricas (reg puede ser nil).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Batches: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_ingester_batches_total",
			Help: "Lotes de flujos procesados por resultado."}, []string{"result"}),
		Rows: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_ingester_rows_total",
			Help: "Filas insertadas en flows_raw por estado de atribución."}, []string{"attribution_status"}),
		InsertDur: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "horus_ingester_insert_seconds",
			Help: "Duración de cada INSERT en flows_raw.", Buckets: prometheus.ExponentialBuckets(0.005, 2, 12)}),
		Lag: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_ingester_consumer_pending",
			Help: "Mensajes pendientes del durable flows-ingester."}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{m.Batches, m.Rows, m.InsertDur, m.Lag} {
			_ = reg.Register(c)
		}
	}
	return m
}

// Consumer consume TLM_FLOWS con el durable flows-ingester y confirma cada
// lote después del INSERT (at-least-once + token de deduplicación).
type Consumer struct {
	Proc    *Processor
	Ins     Inserter
	Workers int
	M       *Metrics
	Log     *slog.Logger
	// DLQ publica una copia de los lotes terminados (opcional).
	DLQ func(ctx context.Context, m *nats.Msg) error
}

type outcome int

const (
	outAck outcome = iota
	outNak
	outTerm
)

// Process procesa un mensaje y devuelve qué hacer con él (testeable sin NATS).
func (c *Consumer) Process(ctx context.Context, data []byte, headers nats.Header) (outcome, error) {
	fb, err := flowpb.UnmarshalFlowBatch(data)
	if err != nil {
		return outTerm, err
	}
	rows, err := c.Proc.Rows(fb, headers.Get(flowbus.HeaderTenant))
	switch {
	case errors.Is(err, ErrPermanent):
		return outTerm, err
	case err != nil:
		return outNak, err
	}
	start := time.Now()
	if err := c.Ins.Insert(ctx, fb.BatchID, rows); err != nil {
		return outNak, err
	}
	c.M.InsertDur.Observe(time.Since(start).Seconds())
	for i := range rows {
		c.M.Rows.WithLabelValues(rows[i].AttributionStatus).Inc()
	}
	return outAck, nil
}

func (c *Consumer) handle(ctx context.Context, msg jetstream.Msg) {
	out, err := c.Process(ctx, msg.Data(), msg.Headers())
	switch out {
	case outAck:
		c.M.Batches.WithLabelValues("ok").Inc()
		_ = msg.Ack()
	case outNak:
		c.M.Batches.WithLabelValues("retry").Inc()
		c.Log.Warn("flow batch will be retried", "subject", msg.Subject(), "error", err)
		if md, mErr := msg.Metadata(); mErr == nil && md.NumDelivered >= 5 {
			c.terminate(ctx, msg, err)
			return
		}
		_ = msg.NakWithDelay(2 * time.Second)
	case outTerm:
		c.terminate(ctx, msg, err)
	}
}

func (c *Consumer) terminate(ctx context.Context, msg jetstream.Msg, cause error) {
	c.M.Batches.WithLabelValues("dlq").Inc()
	c.Log.Error("flow batch sent to DLQ", "subject", msg.Subject(), "error", cause)
	if c.DLQ != nil {
		d := nats.NewMsg("horus.dlq.flows.flows-ingester")
		d.Data = msg.Data()
		for k, v := range msg.Headers() {
			d.Header[k] = v
		}
		d.Header.Set("Horus-Dlq-Original-Subject", msg.Subject())
		d.Header.Set("Horus-Dlq-Consumer", flowbus.ConsumerIngester)
		d.Header.Set("Horus-Dlq-Failed-At", time.Now().UTC().Format(flowbus.TimeFormat))
		if md, err := msg.Metadata(); err == nil {
			d.Header.Set("Horus-Dlq-Stream", md.Stream)
			d.Header.Set("Horus-Dlq-Stream-Seq", strconv.FormatUint(md.Sequence.Stream, 10))
			d.Header.Set("Horus-Dlq-Attempts", strconv.FormatUint(md.NumDelivered, 10))
			d.Header.Set(flowbus.HeaderMsgID, fmt.Sprintf("dlq:%s:%d", md.Stream, md.Sequence.Stream))
		}
		e := fmt.Sprint(cause)
		if len(e) > 1024 {
			e = e[:1024]
		}
		d.Header.Set("Horus-Dlq-Error", e)
		if err := c.DLQ(ctx, d); err != nil {
			c.Log.Warn("DLQ publish failed", "error", err)
		}
	}
	_ = msg.Term()
}

// Run consume hasta que ctx se cancela.
func (c *Consumer) Run(ctx context.Context, cons jetstream.Consumer) error {
	workers := max(c.Workers, 1)
	it, err := cons.Messages(jetstream.PullMaxMessages(workers * 4))
	if err != nil {
		return err
	}
	ch := make(chan jetstream.Msg, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range ch {
				c.handle(context.WithoutCancel(ctx), m)
			}
		}()
	}
	go func() {
		<-ctx.Done()
		it.Stop()
	}()
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if info, err := cons.Info(ctx); err == nil {
					c.M.Lag.Set(float64(info.NumPending + uint64(info.NumAckPending))) //nolint:gosec // contador
				}
			}
		}
	}()
	for {
		m, err := it.Next()
		if err != nil {
			break
		}
		ch <- m
	}
	close(ch)
	wg.Wait()
	return nil
}
