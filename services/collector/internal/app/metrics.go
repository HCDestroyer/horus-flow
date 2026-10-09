package app

import "github.com/prometheus/client_golang/prometheus"

// Razones de descarte de horus_collector_dropped_total.
const (
	DropUnknownExporter = "unknown_exporter"
	DropMalformed       = "malformed"
	DropNoTemplate      = "no_template"
	DropExcluded        = "excluded"
	DropTunnel          = "tunnel"
	DropQueueFull       = "queue_full"
	DropBusUnavailable  = "bus_unavailable"
)

// Metrics del collector (docs/observability.md: prefijo horus_collector_).
type Metrics struct {
	Datagrams    *prometheus.CounterVec
	Records      prometheus.Counter
	Dropped      *prometheus.CounterVec
	SeqGaps      *prometheus.CounterVec
	LostRecords  *prometheus.CounterVec
	ClockSkew    *prometheus.GaugeVec
	Batches      prometheus.Counter
	BufferBytes  prometheus.Gauge
	BusConnected prometheus.Gauge
}

// NewMetrics crea y registra las métricas (reg puede ser nil en tests).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Datagrams: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_collector_datagrams_total",
			Help: "Datagramas de exportación recibidos por versión."}, []string{"version"}),
		Records: prometheus.NewCounter(prometheus.CounterOpts{Name: "horus_collector_records_total",
			Help: "Registros de flujo decodificados y publicados."}),
		Dropped: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_collector_dropped_total",
			Help: "Datagramas, conjuntos o registros descartados por razón."}, []string{"reason"}),
		SeqGaps: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_collector_sequence_gaps_total",
			Help: "Saltos en la secuencia del exportador."}, []string{"router_id"}),
		LostRecords: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_collector_lost_records_total",
			Help: "Registros que el exportador envió y no llegaron (según la secuencia)."}, []string{"router_id"}),
		ClockSkew: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "horus_collector_clock_skew_seconds",
			Help: "exportTime del exportador menos el reloj del collector."}, []string{"router_id"}),
		Batches: prometheus.NewCounter(prometheus.CounterOpts{Name: "horus_collector_batches_published_total",
			Help: "Lotes publicados en TLM_FLOWS."}),
		BufferBytes: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_collector_buffer_bytes",
			Help: "Bytes de lotes pendientes de publicar."}),
		BusConnected: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_collector_bus_connected",
			Help: "1 si la última publicación en NATS tuvo éxito."}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{m.Datagrams, m.Records, m.Dropped, m.SeqGaps, m.LostRecords,
			m.ClockSkew, m.Batches, m.BufferBytes, m.BusConnected} {
			_ = reg.Register(c)
		}
	}
	return m
}
