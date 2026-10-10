package httpx

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// HeaderRequestID es la cabecera de correlación de peticiones.
const HeaderRequestID = "X-Request-Id"

const maxRequestIDLen = 64

// RequestID propaga X-Request-Id (o genera un UUIDv7 si falta o no es
// válido), lo guarda en el contexto para los logs y lo devuelve en la
// respuesta.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(observability.WithRequestID(r.Context(), id)))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for _, c := range id {
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

func newRequestID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}

// Recover convierte un pánico en un 500 y lo registra una vez, sin cuerpo de
// la petición ni detalles al cliente.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { //nolint:errorlint // centinela de net/http, comparado por identidad
						panic(v) //nolint:forbidigo // net/http debe abortar la conexión
					}
					logger.ErrorContext(r.Context(), "http handler panic",
						slog.String("method", r.Method), slog.String("route", r.Pattern), slog.Any("panic", v))
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Metrics son las métricas RED HTTP comunes (docs/observability.md §2.3).
type Metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight *prometheus.GaugeVec
}

// NewMetrics crea y registra en reg las métricas RED HTTP.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_server_requests_total",
			Help: "HTTP requests handled, by service, method, route template and status code.",
		}, []string{"service", "method", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_server_request_duration_seconds",
			Help:    "HTTP request latency in seconds, by service, method, route template and status class.",
			Buckets: observability.LatencyBuckets,
		}, []string{"service", "method", "route", "status_class"}),
		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "http_server_requests_in_flight",
			Help: "HTTP requests currently being served, by service.",
		}, []string{"service"}),
	}
	for _, c := range []prometheus.Collector{m.requests, m.duration, m.inFlight} {
		if err := reg.Register(c); err != nil {
			return nil, err //nolint:wrapcheck // error de registro autoexplicativo
		}
	}
	return m, nil
}

// Middleware instrumenta un handler de service. La ruta es la plantilla de
// http.ServeMux (r.Pattern), nunca el path real; "unmatched" si no hay.
func (m *Metrics) Middleware(service string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inFlight := m.inFlight.WithLabelValues(service)
			inFlight.Inc()
			defer inFlight.Dec()
			sw := &statusWriter{ResponseWriter: w}
			start := time.Now()
			defer func() {
				code := sw.status()
				route := r.Pattern
				if route == "" {
					route = "unmatched"
				}
				method := normalizeMethod(r.Method)
				m.requests.WithLabelValues(service, method, route, strconv.Itoa(code)).Inc()
				m.duration.WithLabelValues(service, method, route, strconv.Itoa(code/100)+"xx").
					Observe(time.Since(start).Seconds())
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

func normalizeMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions:
		return m
	default:
		return "OTHER"
	}
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b) //nolint:wrapcheck // passthrough de io.Writer
}

func (w *statusWriter) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

// Unwrap permite a http.ResponseController llegar al writer original.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Hijack delega en el writer original: las bibliotecas WebSocket (coder/websocket
// en el hub del gateway) exigen http.Hijacker por aserción de tipo y no usan
// http.ResponseController; sin él, GET /api/v1/ws respondía 501 detrás de este
// middleware (I1-26).
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil && w.code == 0 {
		w.code = http.StatusSwitchingProtocols
	}
	return c, rw, err //nolint:wrapcheck // passthrough de http.Hijacker
}

// Flush delega en el writer original (respuestas en streaming).
func (w *statusWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
