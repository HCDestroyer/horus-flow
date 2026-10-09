package authz

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ScopeAll es el alcance "todo el tenant" en `perms`.
const ScopeAll = "*"

// Principal es la identidad autenticada de una petición.
type Principal struct {
	Type        string // user | kiosk | service
	Subject     string
	UserID      uuid.UUID // solo Type == user
	SessionID   uuid.UUID
	Scope       string    // session | tenant | platform | kiosk
	TenantID    uuid.UUID // solo Scope tenant/kiosk
	KioskID     uuid.UUID // solo Type kiosk
	ViaPlatform bool
	AMR         []string
	AuthTime    time.Time
	ExpiresAt   time.Time
	TokenID     string
	// Perms: permiso → alcances (`*`, `site:<uuid>`, `router_group:<uuid>`)
	// en el tenant del token, o permisos de plataforma con alcance `*`.
	Perms map[string][]string
	// Raw es el JWT original (para reenviarlo; nunca se registra).
	Raw string
}

// Has indica si tiene perm en algún alcance (permiso grueso del gateway).
func (p *Principal) Has(perm string) bool {
	if p == nil {
		return false
	}
	return len(p.Perms[perm]) > 0
}

// HasScope indica si tiene perm en todo el tenant o en el alcance scope
// (`site:<uuid>`).
func (p *Principal) HasScope(perm, scope string) bool {
	if p == nil {
		return false
	}
	s := p.Perms[perm]
	return slices.Contains(s, ScopeAll) || (scope != "" && slices.Contains(s, scope))
}

// TenantWide indica si tiene perm en todo el tenant.
func (p *Principal) TenantWide(perm string) bool { return p.HasScope(perm, "") }

// SiteScopes devuelve los nodos a los que se limita perm (nil si tiene
// alcance de tenant completo o ninguno).
func (p *Principal) SiteScopes(perm string) []uuid.UUID {
	if p == nil || p.TenantWide(perm) {
		return nil
	}
	var out []uuid.UUID
	for _, s := range p.Perms[perm] {
		if id, ok := strings.CutPrefix(s, "site:"); ok {
			if u, err := uuid.Parse(id); err == nil {
				out = append(out, u)
			}
		}
	}
	return out
}

// AuthAge devuelve el tiempo desde la última autenticación (re-auth).
func (p *Principal) AuthAge(now time.Time) time.Duration {
	if p == nil || p.AuthTime.IsZero() {
		return time.Duration(1<<63 - 1)
	}
	return now.Sub(p.AuthTime)
}

// ActorID es el identificador del actor para auditoría y eventos.
func (p *Principal) ActorID() string {
	if p == nil {
		return ""
	}
	return p.Subject
}

type ctxKey struct{}

// WithPrincipal guarda p en el contexto.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext devuelve el principal de la petición o nil.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// Errores de autorización.
var (
	ErrUnauthenticated = errors.New("authz: unauthenticated")
	ErrForbidden       = errors.New("authz: permission denied")
	ErrScope           = errors.New("authz: token scope invalid")
)

// Require comprueba que el principal del contexto tiene perm en algún
// alcance de su tenant (o de plataforma).
func Require(ctx context.Context, perm string) error {
	p := FromContext(ctx)
	if p == nil {
		return ErrUnauthenticated
	}
	if !p.Has(perm) {
		return ErrForbidden
	}
	return nil
}

// TenantOf devuelve el tenant del token de la petición o un error si el
// token no es de tenant.
func TenantOf(ctx context.Context) (uuid.UUID, error) {
	p := FromContext(ctx)
	if p == nil {
		return uuid.Nil, ErrUnauthenticated
	}
	if p.Scope != ScopeTenant && p.Scope != ScopeKiosk || p.TenantID == uuid.Nil {
		return uuid.Nil, ErrScope
	}
	return p.TenantID, nil
}
