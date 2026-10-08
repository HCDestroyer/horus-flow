package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound lo devuelve el repositorio cuando no hay fila.
var ErrNotFound = errors.New("auth: not found")

// Estados.
const (
	UserActive  = "active"
	UserPending = "pending"

	MembershipActive  = "active"
	MembershipInvited = "invited"

	TenantActive    = "active"
	TenantSuspended = "suspended"
)

// AMR (RFC 8176).
const (
	AMRPassword = "pwd"
	AMROTP      = "otp"
)

// User es un usuario global de la plataforma.
type User struct {
	ID                 uuid.UUID
	Email              string
	DisplayName        string
	PasswordHash       string
	Status             string
	IsPlatformAdmin    bool
	MFAEnforced        bool
	MustChangePassword bool
	FailedLogins       int
	LockedUntil        *time.Time
	Locale             *string
	Timezone           *string
	DefaultTenantID    *uuid.UUID
	Version            int
}

// Session es la sesión de una persona (sin tenant).
type Session struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	CreatedAt     time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
	RevokedReason string
	AMR           []string
	MFAVerifiedAt *time.Time
	IP            string
	UserAgent     string
}

// Active indica si la sesión sigue vigente en now.
func (s *Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// AuthTime es el último instante de autenticación de la sesión.
func (s *Session) AuthTime() time.Time {
	if s.MFAVerifiedAt != nil && s.MFAVerifiedAt.After(s.CreatedAt) {
		return *s.MFAVerifiedAt
	}
	return s.CreatedAt
}

// RefreshToken es un refresh opaco (solo su SHA-256).
type RefreshToken struct {
	ID        uuid.UUID
	SessionID uuid.UUID
	Hash      []byte
	IssuedAt  time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// TOTP es la credencial TOTP de un usuario (secreto cifrado).
type TOTP struct {
	UserID       uuid.UUID
	Ciphertext   []byte
	DEKWrapped   []byte
	KEKID        string
	ConfirmedAt  *time.Time
	LastUsedStep int64
}

// Tenant es un ISP.
type Tenant struct {
	ID                  uuid.UUID
	Slug                string
	Name                string
	Status              string
	Country             string
	Timezone            string
	Quotas              map[string]any
	Settings            map[string]any
	SupportAccessPolicy string
	CreatedBy           *uuid.UUID
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Version             int
	Members             int
}

// Assignment es una asignación (rol, alcance) de una membresía.
type Assignment struct {
	RoleID      uuid.UUID
	RoleKey     string
	ScopeType   string // tenant | site | router_group
	ScopeID     *uuid.UUID
	RequiresMFA bool
	Permissions []string
}

// Scope devuelve el alcance en notación del contrato (`tenant`, `site:<id>`).
func (a Assignment) Scope() string {
	if a.ScopeType == "tenant" || a.ScopeID == nil {
		return "tenant"
	}
	return a.ScopeType + ":" + a.ScopeID.String()
}

// PermScope devuelve el alcance en notación de `perms` (`*`, `site:<id>`).
func (a Assignment) PermScope() string {
	if a.ScopeType == "tenant" || a.ScopeID == nil {
		return "*"
	}
	return a.ScopeType + ":" + a.ScopeID.String()
}

// Membership es la pertenencia de un usuario a un tenant.
type Membership struct {
	ID          uuid.UUID
	Status      string
	Tenant      Tenant
	Assignments []Assignment
}

// EffectivePermissions une los permisos de las asignaciones con su alcance
// (permiso → alcances; `*` absorbe los demás). Nunca mezcla tenants.
func (m Membership) EffectivePermissions() map[string][]string {
	out := map[string][]string{}
	for _, a := range m.Assignments {
		sc := a.PermScope()
		for _, p := range a.Permissions {
			cur := out[p]
			if len(cur) == 1 && cur[0] == "*" {
				continue
			}
			if sc == "*" {
				out[p] = []string{"*"}
				continue
			}
			dup := false
			for _, c := range cur {
				dup = dup || c == sc
			}
			if !dup {
				out[p] = append(cur, sc)
			}
		}
	}
	return out
}

// RequiresMFA indica si algún rol de la membresía exige 2FA.
func (m Membership) RequiresMFA() bool {
	for _, a := range m.Assignments {
		if a.RequiresMFA {
			return true
		}
	}
	return false
}
