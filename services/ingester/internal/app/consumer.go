package app

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
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
	// InsertRows: filas por INSERT; Flushes: grupos escritos por motivo
	// (rows, time, recovery).
	InsertRows prometheus.Histogram
	Flushes    *prometheus.CounterVec
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
		InsertRows: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "horus_ingester_insert_rows",
			Help: "Filas de cada INSERT agrupado en flows_raw.", Buckets: prometheus.ExponentialBuckets(500, 2, 10)}),
		Flushes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_ingester_insert_groups_total",
			Help: "INSERT agrupados por motivo de cierre del grupo (rows, time, recovery)."}, []string{"reason"}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{m.Batches, m.Rows, m.InsertDur, m.Lag, m.InsertRows, m.Flushes} {
			_ = reg.Register(c)
		}
	}
	return m
}

// Consumer consume TLM_FLOWS con el durable flows-ingester, agrupa lotes en
// INSERT grandes y confirma cada lote después del INSERT de su grupo
// (at-least-once + token de deduplicación por grupo, ver group.go).
type Consumer struct {
	Proc *Processor
	Ins  Inserter
	// Workers decodifican y atribuyen lotes en paralelo.
	Workers int
	// Group configura la escritura agrupada; Ledger hace idempotentes los
	// reintentos tras una caída (nil: sin recuperación de grupos en curso).
	Group  GroupOptions
	Ledger Ledger
	M      *Metrics
	Log    *slog.Logger
	// DLQ publica una copia de los lotes terminados (opcional).
	DLQ func(ctx context.Context, m *nats.Msg) error
}

type outcome int

const (
	outAck outcome = iota
	outNak
	outTerm
)

// Process procesa un mensaje con un INSERT propio y devuelve qué hacer con él
// (testeable sin NATS; el camino de producción es runGrouped). stop corta los
// reintentos de INSERT (apagado); progress alarga ack_wait.
func (c *Consumer) Process(ctx context.Context, stop <-chan struct{}, data []byte, headers nats.Header, progress func()) (outcome, error) {
	batchID, rows, out, err := c.Prepare(data, headers)
	if out != outAck {
		return out, err
	}
	start := time.Now()
	// ClickHouse caído: se reintenta sin soltar el mensaje (InProgress alarga
	// ack_wait) para que el stream retenga lo pendiente y no se agoten las
	// entregas (I1-04 criterio 6). Se sale solo al apagar.
	backoff := 200 * time.Millisecond
	for {
		err := c.Ins.Insert(ctx, batchID, rows)
		if err == nil {
			break
		}
		if progress != nil {
			progress()
		}
		c.M.Batches.WithLabelValues("insert_retry").Inc()
		c.Log.Warn("flows_raw insert failed: retrying", "batch_id", batchID, "error", err)
		select {
		case <-stop:
			return outNak, err
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 10*time.Second)
	}
	c.M.InsertDur.Observe(time.Since(start).Seconds())
	for i := range rows {
		c.M.Rows.WithLabelValues(rows[i].AttributionStatus).Inc()
	}
	return outAck, nil
}

func (c *Consumer) terminate(ctx context.Context, msg BusMsg, cause error) {
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
	opts := c.Group.withDefaults()
	// Mensajes en vuelo: los de los grupos que se escriben y el que se
	// acumula (≈ 500 registros por lote), con margen.
	inflight := max(c.Workers*4, (opts.Flushers+2)*opts.Rows/500)
	it, err := cons.Messages(jetstream.PullMaxMessages(inflight))
	if err != nil {
		return err
	}
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
	return c.runGrouped(ctx, func() (BusMsg, error) { return it.Next() }, it.Stop)
}
