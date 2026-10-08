package observability

import (
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// LatencyBuckets son los buckets comunes de histogramas de latencia en
// segundos (docs/observability.md §2.1).
var LatencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// BuildInfo describe la versión del binario para horus_build_info.
type BuildInfo struct {
	Service string
	Version string
	Commit  string
}

// NewRegistry crea un registro Prometheus propio (no el global) con los
// colectores de runtime Go y de proceso y la métrica
// horus_build_info{service,version,commit,go_version} 1.
func NewRegistry(info BuildInfo) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	build := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "horus_build_info",
		Help: "Build information of the horus binary; always 1.",
	}, []string{"service", "version", "commit", "go_version"})
	build.WithLabelValues(info.Service, info.Version, info.Commit, runtime.Version()).Set(1)
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		build,
	)
	return reg
}

// MetricsHandler expone g en formato Prometheus/OpenMetrics.
func MetricsHandler(g prometheus.Gatherer) http.Handler {
	return promhttp.HandlerFor(g, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

// forbiddenLabels son labels prohibidos por cardinalidad o privacidad
// (docs/observability.md §2.2).
var forbiddenLabels = map[string]bool{
	"ip": true, "client_ip": true, "src_ip": true, "dst_ip": true, "src_addr": true, "dst_addr": true,
	"address": true, "customer_id": true, "realm_id": true, "finding_id": true, "kiosk_id": true,
	"user_id": true, "session_id": true, "trace_id": true, "span_id": true, "request_id": true,
	"interface_id": true, "ifindex": true, "if_index": true, "asn": true, "port": true, "src_port": true,
	"dst_port": true, "path": true, "url": true, "error": true, "message": true, "email": true,
	"tenant_id": true,
}

// ErrForbiddenLabel indica un label de métrica prohibido.
var ErrForbiddenLabel = errors.New("forbidden metric label")

// ValidateLabels rechaza labels de la lista negra de docs/observability.md
// §2.2. Las librerías que crean métricas la llaman al construirlas; los
// módulos deben llamarla en sus tests.
func ValidateLabels(labels ...string) error {
	var errs []error
	for _, l := range labels {
		if forbiddenLabels[strings.ToLower(l)] {
			errs = append(errs, fmt.Errorf("%w: %q", ErrForbiddenLabel, l))
		}
	}
	return errors.Join(errs...)
}
