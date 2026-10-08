package httpx

import (
	"log/slog"
	"net/http"
	"net/http/pprof"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Mux es el router de la API REST compartido por los roles locales del
// proceso (puerto HTTPAddr). Cada rol monta sus rutas a través de su
// [ServiceMux], que añade el rol al contexto y las métricas con su service.
type Mux struct {
	mux     *http.ServeMux
	metrics *Metrics
	logger  *slog.Logger
	routes  atomic.Int32
}

// NewMux crea el router de la API.
func NewMux(metrics *Metrics, logger *slog.Logger) *Mux {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Mux{mux: http.NewServeMux(), metrics: metrics, logger: logger}
}

// ForService devuelve el registrador de rutas del rol service.
func (m *Mux) ForService(service string) *ServiceMux {
	return &ServiceMux{parent: m, service: service}
}

// Routes devuelve cuántas rutas se han registrado (0 = no hace falta
// levantar el servidor de API).
func (m *Mux) Routes() int { return int(m.routes.Load()) }

// Handler devuelve el handler raíz con request_id y recuperación de pánicos.
func (m *Mux) Handler() http.Handler {
	return RequestID(Recover(m.logger)(m.mux))
}

// ServiceMux registra rutas de un rol.
type ServiceMux struct {
	parent  *Mux
	service string
}

// Handle registra h para pattern (sintaxis de http.ServeMux, p. ej.
// "GET /api/v1/devices/{id}").
func (s *ServiceMux) Handle(pattern string, h http.Handler) {
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(observability.WithRole(r.Context(), s.service)))
	})
	var final http.Handler = wrapped
	if s.parent.metrics != nil {
		final = s.parent.metrics.Middleware(s.service)(wrapped)
	}
	s.parent.mux.Handle(pattern, final)
	s.parent.routes.Add(1)
}

// HandleFunc es Handle para funciones.
func (s *ServiceMux) HandleFunc(pattern string, f func(http.ResponseWriter, *http.Request)) {
	s.Handle(pattern, http.HandlerFunc(f))
}

// AdminOptions configura [AdminHandler].
type AdminOptions struct {
	Health   *health.Registry
	Gatherer prometheus.Gatherer
	Pprof    bool
}

// AdminHandler sirve el puerto de administración (HORUS_ADMIN_ADDR):
// /healthz, /readyz, /metrics y, si Pprof, /debug/pprof/*. Nunca se publica
// al exterior (docs/observability.md §1).
func AdminHandler(o AdminOptions) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", o.Health.LiveHandler())
	mux.Handle("GET /readyz", o.Health.ReadyHandler())
	mux.Handle("GET /metrics", observability.MetricsHandler(o.Gatherer))
	if o.Pprof {
		mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	}
	return mux
}
