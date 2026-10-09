// Package edge es el middleware de borde del gateway (docs/api.md §3,
// docs/security.md §5.1 y §6.4 punto 1), montado delante de las rutas de
// los módulos locales (ADR-0025):
//
//  1. deny by default: solo pasan las rutas de la tabla gateway-routes
//     (404 NOT_FOUND / 405 METHOD_NOT_ALLOWED);
//  2. elimina cabeceras X-Horus-* entrantes y aplica el rate limit de la ruta;
//  3. valida el access JWT (firma, exp, aud), el tipo de principal
//     (kioscos solo en rutas que lo declaran), el ámbito del token
//     (`tenant` → token con tid; si no, 403 TOKEN_SCOPE_INVALID sin tocar la
//     base) y el permiso grueso ("¿lo tiene en algún alcance?");
//  4. re-autenticación reciente (x-reauth) e Idempotency-Key obligatoria;
//  5. revocación de sesión y membresía (auth.SessionService) y tenant
//     suspendido;
//  6. rechaza un `tenant_id` del cuerpo distinto de `tid` (403 TENANT_MISMATCH);
//  7. audita los accesos de plataforma a datos de un ISP (via_platform);
//  8. deja el principal y el tenant en el contexto y llama al módulo.
package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/authz"
	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
	authapi "github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/gateway/internal/routes"
)

// HeaderTenant es la cabecera de observabilidad con el tid.
const HeaderTenant = "X-Horus-Tenant"

// WSPath es la ruta del WebSocket (autenticada por ticket de un uso).
const WSPath = "/api/v1/ws"

// ReauthMaxAge es la antigüedad máxima de auth_time en rutas 🔒.
const ReauthMaxAge = 5 * time.Minute

// MaxBody es el límite de cuerpo (413).
const MaxBody = 1 << 20

// Matcher indica si algún módulo local atiende la petición.
type Matcher interface {
	Matches(r *http.Request) bool
}

// Options configura el borde.
type Options struct {
	Routes   []routes.Route
	Verifier *authz.Verifier
	// Sessions comprueba la revocación; nil = 503 en rutas autenticadas
	// (fail-closed) salvo AllowNoSessionCheck.
	Sessions            authapi.SessionChecker
	AllowNoSessionCheck bool
	// Kiosks comprueba revocación y allowed_cidrs de los kioscos (nil = 503
	// para tokens de kiosco, fail-closed).
	Kiosks authapi.KioskChecker
	Audit  authapi.AuditRecorder
	Local  Matcher
	Logger *slog.Logger
	Now    func() time.Time
	// RateLimits: nombre (x-rate-limit) → peticiones por minuto y por IP.
	RateLimits map[string]int
}

// DefaultRateLimits son los límites por IP de docs/api.md §1.10.
var DefaultRateLimits = map[string]int{"login": 20, "kiosk_enroll": 5, "enroll": 10, "connection_test": 10}

// Edge es el middleware.
type Edge struct {
	o       Options
	limiter *limiter
}

// New crea el borde.
func New(o Options) *Edge {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.RateLimits == nil {
		o.RateLimits = DefaultRateLimits
	}
	return &Edge{o: o, limiter: newLimiter(o.Now)}
}

// Middleware devuelve el handler de borde delante de next (el mux de los
// módulos locales).
func (e *Edge) Middleware(next http.Handler) http.Handler {
	r := chi.NewRouter()
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		problem.Std(w, req, http.StatusNotFound, problem.CodeNotFound)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		problem.Std(w, req, http.StatusMethodNotAllowed, problem.CodeMethodNotAllowed)
	})
	for _, rt := range e.o.Routes {
		rt := rt
		r.Method(rt.Method, rt.Prefix, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			e.serve(w, req, rt, next)
		}))
	}
	return r
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (e *Edge) serve(w http.ResponseWriter, r *http.Request, rt routes.Route, next http.Handler) {
	for k := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-horus-") {
			r.Header.Del(k)
		}
	}
	if rt.RateLimit != "" {
		if n, ok := e.o.RateLimits[rt.RateLimit]; ok && !e.limiter.allow(rt.RateLimit+"|"+clientIP(r), n) {
			w.Header().Set("Retry-After", "60")
			problem.Std(w, r, http.StatusTooManyRequests, problem.CodeRateLimited)
			return
		}
	}
	if rt.SelfAuthenticating() || (rt.Prefix == WSPath && r.URL.Query().Has("ticket")) {
		// El dueño autentica (cookie, token de un uso o ticket WebSocket).
		e.forward(w, r, rt, next)
		return
	}
	tok := authz.BearerToken(r)
	if tok == "" {
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeUnauthenticated)
		return
	}
	p, err := e.o.Verifier.Verify(tok)
	if err != nil {
		code := problem.CodeUnauthenticated
		if err == authz.ErrTokenExpired { //nolint:errorlint // centinela devuelto tal cual
			code = problem.CodeTokenExpired
		}
		problem.Std(w, r, http.StatusUnauthorized, code)
		return
	}
	req := authz.Requirement{Scope: rt.Scope, AllowKiosk: rt.AllowsKiosk()}
	switch rt.Permission {
	case routes.PermAuthenticated, routes.PermWidgetType, routes.PermDashboardAccess:
		// Solo autenticación: el permiso fino lo evalúa el módulo dueño.
	case routes.PermKioskSelf:
		// Exclusiva de kioscos (se comprueba tras el ámbito).
	default:
		if p.Type != authz.TypeKiosk { // un kiosco no lleva permisos: lo limita la lista blanca
			req.Permission = rt.Permission
		}
	}
	if !authz.Check(w, r, p, req) {
		return
	}
	if rt.Permission == routes.PermKioskSelf && p.Type != authz.TypeKiosk {
		problem.Std(w, r, http.StatusForbidden, problem.CodePermissionDenied, problem.WithDetail("Ruta exclusiva de kioscos."))
		return
	}
	if rt.Reauth && p.AuthAge(e.o.Now()) > ReauthMaxAge {
		problem.Std(w, r, http.StatusForbidden, problem.CodeReauthRequired,
			problem.WithDetail("Repita la autenticación (POST /auth/reauth)."))
		return
	}
	if rt.Idempotent {
		if _, err := uuid.Parse(r.Header.Get("Idempotency-Key")); err != nil {
			problem.Std(w, r, http.StatusPreconditionRequired, problem.CodePreconditionRequired,
				problem.WithDetail("Idempotency-Key (UUID) es obligatoria."))
			return
		}
	}
	if !e.checkSession(w, r, p) {
		return
	}
	if p.Scope == authz.ScopeTenant && !e.checkBodyTenant(w, r, p) {
		return
	}
	ctx := authz.WithPrincipal(r.Context(), p)
	if p.TenantID != uuid.Nil {
		ctx = observability.WithTenant(ctx, p.TenantID.String())
		r.Header.Set(HeaderTenant, p.TenantID.String())
	}
	r = r.WithContext(ctx)
	if p.ViaPlatform && p.TenantID != uuid.Nil {
		e.auditPlatformAccess(r, rt, p)
	}
	e.forward(w, r, rt, next)
}

func (e *Edge) forward(w http.ResponseWriter, r *http.Request, rt routes.Route, next http.Handler) {
	if e.o.Local != nil && !e.o.Local.Matches(r) {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable,
			problem.WithDetail("El módulo "+rt.Module+" no atiende esta ruta en este proceso."))
		return
	}
	next.ServeHTTP(w, r)
}

func (e *Edge) checkSession(w http.ResponseWriter, r *http.Request, p *authz.Principal) bool {
	if p.Type == authz.TypeKiosk {
		return e.checkKiosk(w, r, p)
	}
	if p.Type != authz.TypeUser {
		return true
	}
	if e.o.Sessions == nil {
		if e.o.AllowNoSessionCheck {
			return true
		}
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable,
			problem.WithDetail("No se puede comprobar la revocación de la sesión."))
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	res, err := e.o.Sessions.CheckSession(ctx, authapi.CheckSessionRequest{
		SID: p.SessionID, UserID: p.UserID, TenantID: p.TenantID, ViaPlatform: p.ViaPlatform,
	})
	if err != nil {
		e.o.Logger.ErrorContext(r.Context(), "session check failed", slog.Any("error", err))
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return false
	}
	if !res.Active {
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeSessionRevoked)
		return false
	}
	if p.TenantID != uuid.Nil {
		if !res.MembershipActive {
			problem.Std(w, r, http.StatusForbidden, problem.CodePermissionDenied,
				problem.WithDetail("La membresía en este ISP ya no está activa."))
			return false
		}
		if !res.TenantActive {
			problem.Std(w, r, http.StatusForbidden, problem.CodeTenantSuspended)
			return false
		}
	}
	return true
}

// checkKiosk aplica la revocación inmediata y allowed_cidrs a cada petición
// de un kiosco (I1-14 criterios 3–5).
func (e *Edge) checkKiosk(w http.ResponseWriter, r *http.Request, p *authz.Principal) bool {
	if e.o.Kiosks == nil {
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable,
			problem.WithDetail("No se puede comprobar la revocación del kiosco."))
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	st, err := e.o.Kiosks.CheckKiosk(ctx, p.TenantID, p.KioskID)
	if err != nil {
		e.o.Logger.ErrorContext(r.Context(), "kiosk check failed", slog.Any("error", err))
		problem.Std(w, r, http.StatusServiceUnavailable, problem.CodeServiceUnavailable)
		return false
	}
	if !st.Active {
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeSessionRevoked, problem.WithDetail("Kiosco revocado o caducado."))
		return false
	}
	if !st.AllowsIP(clientIP(r)) {
		problem.Std(w, r, http.StatusForbidden, problem.CodeKioskForbidden, problem.WithDetail("Red no permitida para este kiosco."))
		return false
	}
	return true
}

// checkBodyTenant rechaza un tenant_id de primer nivel distinto de tid.
func (e *Edge) checkBodyTenant(w http.ResponseWriter, r *http.Request, p *authz.Principal) bool {
	if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodDelete {
		return true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		problem.Std(w, r, http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var probe struct {
		TenantID *string `json:"tenant_id"`
	}
	if len(body) > 0 && json.Unmarshal(body, &probe) == nil && probe.TenantID != nil {
		if id, err := uuid.Parse(*probe.TenantID); err != nil || id != p.TenantID {
			problem.Std(w, r, http.StatusForbidden, problem.CodeTenantMismatch)
			return false
		}
	}
	return true
}

func (e *Edge) auditPlatformAccess(r *http.Request, rt routes.Route, p *authz.Principal) {
	if e.o.Audit == nil {
		e.o.Logger.WarnContext(r.Context(), "platform access to tenant data not audited: auth not local")
		return
	}
	resID := ""
	if rc := chi.RouteContext(r.Context()); rc != nil && len(rc.URLParams.Values) > 0 {
		resID = rc.URLParams.Values[len(rc.URLParams.Values)-1]
	}
	entry := authapi.AuditEntry{
		TenantID: p.TenantID, ActorType: "user", ActorID: p.Subject, ViaPlatform: true,
		Action: "platform.tenant_data.accessed", ResourceType: rt.Module + ":" + rt.Prefix, ResourceID: resID,
		Outcome: "success", IP: clientIP(r), RequestID: observability.RequestIDFrom(r.Context()),
		Changes: map[string]any{"method": r.Method, "route": rt.Prefix, "sid": p.SessionID.String()},
	}
	if err := e.o.Audit.Record(r.Context(), entry); err != nil {
		e.o.Logger.ErrorContext(r.Context(), "platform access audit failed", slog.Any("error", err))
	}
}

// limiter es una ventana fija por minuto en memoria (por réplica; el rate
// limit distribuido en Valkey llega con I1, api.md §1.10).
type limiter struct {
	mu     sync.Mutex
	now    func() time.Time
	window time.Time
	counts map[string]int
}

func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, counts: map[string]int{}}
}

func (l *limiter) allow(key string, perMinute int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.now().Truncate(time.Minute)
	if !w.Equal(l.window) {
		l.window, l.counts = w, map[string]int{}
	}
	l.counts[key]++
	return l.counts[key] <= perMinute
}
