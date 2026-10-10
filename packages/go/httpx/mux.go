package httpx

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hcdestroyer/horus-flow/packages/go/health"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
)

// Mux es el router de la API REST compartido por los roles locales del
// proceso (puerto HTTPAddr). Cada rol monta sus rutas a través de su
// [ServiceMux], que añade el rol al contexto y las métricas con su service.
type Mux struct {
	mux      *http.ServeMux
	metrics  *Metrics
	logger   *slog.Logger
	routes   atomic.Int32
	mu       sync.Mutex
	edge     func(http.Handler) http.Handler
	patterns []string
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

// Handler devuelve el handler raíz con request_id, traza W3C (Trace), recuperación de pánicos y,
// si el rol gateway lo instaló, su middleware de borde (autenticación,
// permiso grueso por ruta, rate limit) delante de todas las rutas.
func (m *Mux) Handler() http.Handler {
	m.mu.Lock()
	edge := m.edge
	m.mu.Unlock()
	var h http.Handler = m.mux
	if edge != nil {
		h = edge(h)
	}
	return RequestID(Trace(Recover(m.logger)(h)))
}

// Matches indica si alguna ruta registrada atiende r (método y ruta).
func (m *Mux) Matches(r *http.Request) bool {
	_, pattern := m.mux.Handler(r)
	return pattern != ""
}

// Patterns devuelve los patrones registrados ("GET /api/v1/sites/{site_id}").
func (m *Mux) Patterns() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.patterns...)
}

// ServiceMux registra rutas de un rol.
type ServiceMux struct {
	parent  *Mux
	service string
}

// Handle registra h para pattern (sintaxis de http.ServeMux, p. ej.
// "GET /api/v1/devices/{id}").
func (s *ServiceMux) Handle(pattern string, h http.Handler) {
	hasRouter := strings.Contains(pattern, "{router_id}")
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := observability.WithRole(r.Context(), s.service)
		// router_id en todos los logs de las rutas de un router (§3.1).
		if hasRouter {
			if id := r.PathValue("router_id"); id != "" {
				ctx = observability.WithRouter(ctx, id)
			}
		}
		h.ServeHTTP(w, r.WithContext(ctx))
	})
	var final http.Handler = wrapped
	if s.parent.metrics != nil {
		final = s.parent.metrics.Middleware(s.service)(wrapped)
	}
	s.parent.mux.Handle(pattern, final)
	s.parent.routes.Add(1)
	s.parent.mu.Lock()
	s.parent.patterns = append(s.parent.patterns, pattern)
	s.parent.mu.Unlock()
}

// SetEdge instala el middleware de borde de la API del proceso (lo usa el
// rol gateway, ADR-0025: monta en proceso los handlers de los módulos
// locales). Solo puede haber uno.
func (s *ServiceMux) SetEdge(mw func(http.Handler) http.Handler) error {
	s.parent.mu.Lock()
	defer s.parent.mu.Unlock()
	if s.parent.edge != nil {
		return errors.New("httpx: edge middleware already set")
	}
	s.parent.edge = mw
	return nil
}

// Matches indica si alguna ruta del proceso atiende r.
func (s *ServiceMux) Matches(r *http.Request) bool { return s.parent.Matches(r) }

// Patterns devuelve los patrones registrados en la API del proceso.
func (s *ServiceMux) Patterns() []string { return s.parent.Patterns() }

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
