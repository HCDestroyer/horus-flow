// Package health implementa liveness y readiness por rol
// (docs/observability.md §7, ADR-0025).
//
//   - /healthz (liveness) nunca consulta dependencias externas: responde 200
//     mientras el proceso atiende.
//   - /readyz agrega los chequeos de cada rol activo. Un chequeo crítico que
//     falla deja su rol "unavailable" (el proceso responde 503); un chequeo
//     degradable que falla deja su rol "degraded" con el nombre de la
//     dependencia, y el proceso sigue listo (200). /readyz?role=<rol> consulta
//     un solo rol.
//   - Cada chequeo corre con un timeout (1 s por defecto) y su resultado se
//     cachea (5 s por defecto).
//   - Al recibir SIGTERM el proceso marca Draining y /readyz pasa a 503.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Status de un rol o del proceso.
type Status string

// Estados posibles.
const (
	StatusOK          Status = "ok"
	StatusDegraded    Status = "degraded"
	StatusUnavailable Status = "unavailable"
	StatusStarting    Status = "starting"
	StatusStopping    Status = "stopping"
	StatusDraining    Status = "draining"
)

// Estados de un chequeo individual.
const (
	CheckOK   = "ok"
	CheckFail = "fail"
)

// Valores por defecto de docs/observability.md §7.
const (
	DefaultTimeout  = time.Second
	DefaultCacheTTL = 5 * time.Second
)

// State es la fase del ciclo de vida de un rol.
type State int

// Fases del rol.
const (
	StateStarting State = iota
	StateRunning
	StateStopping
)

// Check es un chequeo de dependencia de un rol.
type Check struct {
	// Name identifica la dependencia (postgres, nats, clickhouse…); aparece en
	// /readyz.
	Name string
	// Critical: si falla, el rol no está listo. Si es false, el rol queda
	// degradado pero listo.
	Critical bool
	// Timeout por ejecución; 0 = el del registro.
	Timeout time.Duration
	// Probe devuelve nil si la dependencia responde.
	Probe func(ctx context.Context) error
}

// Options configura un [Registry].
type Options struct {
	Service  string
	Version  string
	Timeout  time.Duration    // por defecto DefaultTimeout
	CacheTTL time.Duration    // por defecto DefaultCacheTTL; negativo = sin caché
	Now      func() time.Time // reloj inyectable
}

// Registry agrupa los roles activos del proceso y sus chequeos.
type Registry struct {
	opts Options

	mu       sync.RWMutex
	roles    map[string]*Role
	draining bool
}

// NewRegistry crea un registro vacío.
func NewRegistry(opts Options) *Registry {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.CacheTTL == 0 {
		opts.CacheTTL = DefaultCacheTTL
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Registry{opts: opts, roles: map[string]*Role{}}
}

// Role devuelve (creándolo si no existe, en estado starting) el rol name.
func (r *Registry) Role(name string) *Role {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ro, ok := r.roles[name]; ok {
		return ro
	}
	ro := &Role{name: name, reg: r}
	r.roles[name] = ro
	return ro
}

// SetDraining marca el proceso como en apagado: /readyz responde 503.
func (r *Registry) SetDraining() {
	r.mu.Lock()
	r.draining = true
	r.mu.Unlock()
}

// Role es el estado de readiness de un rol.
type Role struct {
	name string
	reg  *Registry

	mu     sync.Mutex
	state  State
	checks []*check
}

// Name devuelve el nombre del rol.
func (ro *Role) Name() string { return ro.name }

// ErrInvalidCheck lo reporta un chequeo registrado sin Name o sin Probe: el
// error de cableado se hace visible en /readyz en lugar de ignorarse.
var ErrInvalidCheck = errors.New("invalid health check: Name and Probe are required")

// AddCheck registra un chequeo de dependencia. Name y Probe son
// obligatorios; si faltan, se registra un chequeo crítico que siempre falla
// con [ErrInvalidCheck].
func (ro *Role) AddCheck(c Check) {
	if c.Name == "" || c.Probe == nil {
		if c.Name == "" {
			c.Name = "invalid_check"
		}
		c.Critical = true
		c.Probe = func(context.Context) error { return ErrInvalidCheck }
	}
	if c.Timeout <= 0 {
		c.Timeout = ro.reg.opts.Timeout
	}
	ro.mu.Lock()
	ro.checks = append(ro.checks, &check{Check: c})
	ro.mu.Unlock()
}

// SetState cambia la fase del rol (la gestiona el ciclo de vida).
func (ro *Role) SetState(s State) {
	ro.mu.Lock()
	ro.state = s
	ro.mu.Unlock()
}

type check struct {
	Check

	mu      sync.Mutex
	at      time.Time
	cached  bool
	err     error
	latency time.Duration
}

func (c *check) run(ctx context.Context, now func() time.Time, ttl time.Duration) (time.Duration, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached && ttl > 0 && now().Sub(c.at) < ttl {
		return c.latency, c.err
	}
	// Sin la cancelación del llamador: un cliente que corta /readyz no debe
	// dejar cacheado un fallo falso.
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.Timeout)
	defer cancel()
	start := now()
	done := make(chan error, 1)
	go func() { done <- c.Probe(cctx) }()
	var err error
	select {
	case err = <-done:
	case <-cctx.Done():
		err = cctx.Err()
	}
	c.latency = now().Sub(start)
	c.err, c.at, c.cached = err, now(), true
	return c.latency, err
}

// CheckResult es el resultado de un chequeo en /readyz.
type CheckResult struct {
	Status    string `json:"status"`
	Critical  bool   `json:"critical"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// RoleReport es el estado de un rol en /readyz.
type RoleReport struct {
	Status Status                 `json:"status"`
	Ready  bool                   `json:"ready"`
	Checks map[string]CheckResult `json:"checks,omitempty"`
}

// Report es la respuesta de /readyz.
type Report struct {
	Status  Status                `json:"status"`
	Ready   bool                  `json:"ready"`
	Service string                `json:"service,omitempty"`
	Version string                `json:"version,omitempty"`
	Roles   map[string]RoleReport `json:"roles"`
}

func (ro *Role) report(ctx context.Context) RoleReport {
	ro.mu.Lock()
	state := ro.state
	checks := append([]*check(nil), ro.checks...)
	ro.mu.Unlock()

	rep := RoleReport{Checks: map[string]CheckResult{}}
	type res struct {
		c   *check
		lat time.Duration
		err error
	}
	results := make([]res, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lat, err := c.run(ctx, ro.reg.opts.Now, ro.reg.opts.CacheTTL)
			results[i] = res{c: c, lat: lat, err: err}
		}()
	}
	wg.Wait()

	critFail, softFail := false, false
	for _, r := range results {
		cr := CheckResult{Status: CheckOK, Critical: r.c.Critical, LatencyMS: r.lat.Milliseconds()}
		if r.err != nil {
			cr.Status, cr.Error = CheckFail, r.err.Error()
			if r.c.Critical {
				critFail = true
			} else {
				softFail = true
			}
		}
		rep.Checks[r.c.Name] = cr
	}
	if len(rep.Checks) == 0 {
		rep.Checks = nil
	}
	switch {
	case state == StateStarting:
		rep.Status = StatusStarting
	case state == StateStopping:
		rep.Status = StatusStopping
	case critFail:
		rep.Status = StatusUnavailable
	case softFail:
		rep.Status, rep.Ready = StatusDegraded, true
	default:
		rep.Status, rep.Ready = StatusOK, true
	}
	return rep
}

// Report evalúa los roles indicados (todos si only está vacío). Devuelve
// false en ok si algún rol de only no existe.
func (r *Registry) Report(ctx context.Context, only ...string) (Report, bool) {
	r.mu.RLock()
	draining := r.draining
	var roles []*Role
	if len(only) == 0 {
		for _, ro := range r.roles {
			roles = append(roles, ro)
		}
	} else {
		for _, name := range only {
			ro, ok := r.roles[name]
			if !ok {
				r.mu.RUnlock()
				return Report{}, false
			}
			roles = append(roles, ro)
		}
	}
	r.mu.RUnlock()
	sort.Slice(roles, func(i, j int) bool { return roles[i].name < roles[j].name })

	rep := Report{Service: r.opts.Service, Version: r.opts.Version, Roles: map[string]RoleReport{}}
	ready, degraded := true, false
	for _, ro := range roles {
		rr := ro.report(ctx)
		rep.Roles[ro.name] = rr
		ready = ready && rr.Ready
		degraded = degraded || rr.Status == StatusDegraded
	}
	switch {
	case draining:
		rep.Status = StatusDraining
	case !ready:
		rep.Status = StatusUnavailable
	case degraded:
		rep.Status, rep.Ready = StatusDegraded, true
	default:
		rep.Status, rep.Ready = StatusOK, true
	}
	return rep, true
}

// LiveHandler sirve /healthz: 200 siempre que el proceso atienda.
func (r *Registry) LiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": string(StatusOK), "service": r.opts.Service, "version": r.opts.Version,
		})
	})
}

// ReadyHandler sirve /readyz y /readyz?role=<rol>: 200 si listo, 503 si no,
// 404 si el rol pedido no está activo en este proceso.
func (r *Registry) ReadyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var only []string
		if role := req.URL.Query().Get("role"); role != "" {
			only = []string{role}
		}
		rep, ok := r.Report(req.Context(), only...)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "unknown_role", "role": only[0]})
			return
		}
		code := http.StatusOK
		if !rep.Ready {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, rep)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v) // el cliente puede haber cerrado; nada más que hacer
}
