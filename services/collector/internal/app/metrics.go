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
	// DropSpoolFull: registros que el spool descartó (lleno o sin poder escribir).
	DropSpoolFull = "spool_full"
)

// Metrics del collector (docs/observability.md: prefijo horus_collector_).
type Metrics struct {
	Datagrams *prometheus.CounterVec
	Records   prometheus.Counter
	// ReceivedRecords son los registros decodificados de exportadores
	// registrados, antes de filtros y lotes (lo recibido por el collector).
	ReceivedRecords prometheus.Counter
	Dropped         *prometheus.CounterVec
	SeqGaps         *prometheus.CounterVec
	LostRecords     *prometheus.CounterVec
	ClockSkew       *prometheus.GaugeVec
	Batches         prometheus.Counter
	BufferBytes     prometheus.Gauge
	BusConnected    prometheus.Gauge

	// Spool a disco (docs/architecture.md §10.15).
	Spooling            prometheus.Gauge
	SpoolBytes          prometheus.Gauge
	SpoolBatches        prometheus.Gauge
	SpoolRecords        prometheus.Gauge
	SpoolOldestAge      prometheus.Gauge
	SpoolWritten        prometheus.Counter
	SpoolReplayed       prometheus.Counter
	SpoolDropped        *prometheus.CounterVec
	SpoolDroppedRecords *prometheus.CounterVec
	// IngressBytesRate y Autonomy: ritmo de entrada de lotes y cuánto tiempo
	// más aguanta cada búfer (memoria, spool) sin bus a ese ritmo.
	IngressBytesRate prometheus.Gauge
	Autonomy         *prometheus.GaugeVec
}

// NewMetrics crea y registra las métricas (reg puede ser nil en tests).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Datagrams: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_collector_datagrams_total",
			Help: "Datagramas de exportación recibidos por versión."}, []string{"version"}),
		Records: prometheus.NewCounter(prometheus.CounterOpts{Name: "horus_collector_records_total",
			Help: "Registros de flujo decodificados y publicados."}),
		ReceivedRecords: prometheus.NewCounter(prometheus.CounterOpts{Name: "horus_collector_received_records_total",
			Help: "Registros de flujo decodificados de exportadores registrados (antes de filtros y lotes)."}),
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
		Spooling: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_collector_spooling",
			Help: "1 si los lotes van al spool a disco (bus caído o búfer en memoria por encima del umbral)."}),
		SpoolBytes: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_collector_spool_bytes",
			Help: "Bytes de los segmentos del spool en disco."}),
		SpoolBatches: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_collector_spool_batches",
			Help: "Lotes en el spool pendientes de reenviar."}),
		SpoolRecords: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_collector_spool_records",
			Help: "Registros de flujo en el spool pendientes de reenviar."}),
		SpoolOldestAge: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_collector_spool_oldest_age_seconds",
			Help: "Antigüedad del lote más antiguo del spool."}),
		SpoolWritten: prometheus.NewCounter(prometheus.CounterOpts{Name: "horus_collector_spool_written_total",
			Help: "Lotes escritos en el spool."}),
		SpoolReplayed: prometheus.NewCounter(prometheus.CounterOpts{Name: "horus_collector_spool_replayed_total",
			Help: "Lotes reenviados desde el spool con PubAck."}),
		SpoolDropped: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_collector_spool_dropped_total",
			Help: "Lotes descartados por el spool (full: lo más antiguo al llenarse; corrupt; write_error)."}, []string{"reason"}),
		SpoolDroppedRecords: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_collector_spool_dropped_records_total",
			Help: "Registros de los lotes descartados por el spool."}, []string{"reason"}),
		IngressBytesRate: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_collector_ingress_bytes_per_second",
			Help: "Bytes de lotes por segundo que produce el collector (media de 5 s)."}),
		Autonomy: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "horus_collector_autonomy_seconds",
			Help: "Segundos que aguanta cada búfer sin bus al ritmo de entrada actual (-1 sin entrada)."}, []string{"buffer"}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{m.Datagrams, m.Records, m.ReceivedRecords, m.Dropped, m.SeqGaps, m.LostRecords,
			m.ClockSkew, m.Batches, m.BufferBytes, m.BusConnected, m.Spooling, m.SpoolBytes, m.SpoolBatches, m.SpoolRecords,
			m.SpoolOldestAge, m.SpoolWritten, m.SpoolReplayed, m.SpoolDropped, m.SpoolDroppedRecords, m.IngressBytesRate, m.Autonomy} {
			_ = reg.Register(c)
		}
	}
	return m
}
