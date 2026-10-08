package authz

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/observability"
	"github.com/hcdestroyer/horus-flow/packages/go/problem"
)

// Guard protege los handlers de un módulo: revalida el access JWT (firma,
// `exp`, `aud`), el tipo de principal, el ámbito del token y el permiso
// (docs/security.md §6.4 punto 2). La revocación de sesiones la comprueba el
// gateway.
type Guard struct {
	v *Verifier
}

// NewGuard crea un guard con el validador v.
func NewGuard(v *Verifier) *Guard { return &Guard{v: v} }

// Verifier devuelve el validador.
func (g *Guard) Verifier() *Verifier { return g.v }

// BearerToken extrae el token de `Authorization: Bearer`.
func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(tok)
}

// Authenticate valida el token de r. Escribe el error y devuelve nil si no
// es válido.
func (g *Guard) Authenticate(w http.ResponseWriter, r *http.Request) *Principal {
	tok := BearerToken(r)
	if tok == "" {
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeUnauthenticated)
		return nil
	}
	p, err := g.v.Verify(tok)
	switch {
	case errors.Is(err, ErrTokenExpired):
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeTokenExpired)
		return nil
	case err != nil:
		problem.Std(w, r, http.StatusUnauthorized, problem.CodeUnauthenticated)
		return nil
	}
	return p
}

// Requirement describe qué exige una ruta.
type Requirement struct {
	// Scope: tenant | platform | session (session = cualquier token de usuario).
	Scope string
	// Permission requerida (vacío = solo autenticación).
	Permission string
	// AllowKiosk permite tokens de kiosco (solo rutas con principals kiosk).
	AllowKiosk bool
}

// Check aplica req a p. Devuelve false tras escribir el error.
func Check(w http.ResponseWriter, r *http.Request, p *Principal, req Requirement) bool {
	if p.Type == TypeKiosk && !req.AllowKiosk {
		problem.Std(w, r, http.StatusForbidden, problem.CodeKioskForbidden)
		return false
	}
	switch req.Scope {
	case ScopeTenant:
		if p.Scope != ScopeTenant && !(req.AllowKiosk && p.Scope == ScopeKiosk) {
			problem.Std(w, r, http.StatusForbidden, problem.CodeTokenScopeInvalid,
				problem.WithDetail("Esta ruta exige un token de ISP (POST /auth/token con tenant_id)."))
			return false
		}
	case ScopePlatform:
		if p.Scope != ScopePlatform {
			problem.Std(w, r, http.StatusForbidden, problem.CodeTokenScopeInvalid,
				problem.WithDetail("Esta ruta exige un token de plataforma (POST /auth/token con scope=platform)."))
			return false
		}
	}
	if req.Permission != "" && !p.Has(req.Permission) {
		problem.Std(w, r, http.StatusForbidden, problem.CodePermissionDenied)
		return false
	}
	return true
}

// Wrap devuelve h protegido por req: valida el token, aplica req y deja el
// principal (y el tenant, para los logs) en el contexto.
func (g *Guard) Wrap(req Requirement, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := g.Authenticate(w, r)
		if p == nil {
			return
		}
		if !Check(w, r, p, req) {
			return
		}
		ctx := WithPrincipal(r.Context(), p)
		if p.TenantID != uuid.Nil {
			ctx = observability.WithTenant(ctx, p.TenantID.String())
		}
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Tenant exige un token de tenant con perm.
func (g *Guard) Tenant(perm string, h http.HandlerFunc) http.Handler {
	return g.Wrap(Requirement{Scope: ScopeTenant, Permission: perm}, h)
}

// Platform exige un token de plataforma con perm.
func (g *Guard) Platform(perm string, h http.HandlerFunc) http.Handler {
	return g.Wrap(Requirement{Scope: ScopePlatform, Permission: perm}, h)
}

// Session exige cualquier token de usuario válido.
func (g *Guard) Session(h http.HandlerFunc) http.Handler {
	return g.Wrap(Requirement{Scope: ScopeSession}, h)
}
