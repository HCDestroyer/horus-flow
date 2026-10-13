package app

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// metrics son las métricas Prometheus del poller (docs/observability.md §2.5).
type metrics struct {
	polls     *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	lag       prometheus.Histogram
	stale     prometheus.Gauge
	heartbeat prometheus.Gauge
	targets   prometheus.Gauge
	routers   *prometheus.GaugeVec
	events    *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		polls: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_snmp_polls_total",
			Help: "Sondeos SNMP por fabricante y resultado (ok, timeout, auth_failed, error, no_credentials)."}, []string{"vendor", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "horus_snmp_poll_duration_seconds",
			Help: "Duración del sondeo de un router.", Buckets: []float64{.05, .1, .25, .5, 1, 2, 5, 10, 20}}, []string{"vendor"}),
		lag: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "horus_snmp_poll_lag_seconds",
			Help:    "Retraso entre la hora programada del ciclo y el inicio real del sondeo de cada router.",
			Buckets: []float64{.1, .5, 1, 2, 5, 10, 30, 60}}),
		stale: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_snmp_targets_stale",
			Help: "Routers sin observación en más de 3 intervalos (estado stale)."}),
		heartbeat: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_snmp_poller_heartbeat_timestamp_seconds",
			Help: "Último latido del sondeador (Unix)."}),
		targets: prometheus.NewGauge(prometheus.GaugeOpts{Name: "horus_snmp_targets", Help: "Routers en sondeo (con túnel y alta avanzada)."}),
		routers: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "horus_snmp_routers",
			Help: "Routers por estado efectivo (online, warning, critical, degraded, offline, stale, unknown)."}, []string{"status"}),
		events: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "horus_snmp_publish_errors_total",
			Help: "Publicaciones en NATS fallidas por clase (event, telemetry, state)."}, []string{"kind"}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{m.polls, m.duration, m.lag, m.stale, m.heartbeat, m.targets, m.routers, m.events} {
			if err := reg.Register(c); err != nil {
				var already prometheus.AlreadyRegisteredError
				if !errors.As(err, &already) {
					panic(err)
				}
			}
		}
	}
	return m
}
