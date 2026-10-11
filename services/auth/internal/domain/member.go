package domain

import (
	"crypto/rand"
	"encoding/base64"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MembershipRevoked es el estado de una membresía quitada del tenant.
const MembershipRevoked = "revoked"

// InvitationTTL es la vida de una invitación (docs/api.md §2.1: 72 h, un uso).
const InvitationTTL = 72 * time.Hour

// Member es la membresía de un usuario en un tenant vista por el tenant
// (solo los datos del usuario necesarios: nunca sus otras membresías).
type Member struct {
	MembershipID uuid.UUID
	UserID       uuid.UUID
	TenantID     uuid.UUID
	DisplayName  string
	Email        string
	Status       string
	MFAEnabled   bool
	Assignments  []Assignment
	Version      int
	CreatedAt    time.Time
}

// TenantRoleRecord es un rol visible en un tenant: plantilla de sistema
// (TenantID nil) o rol propio del tenant.
type TenantRoleRecord struct {
	ID          uuid.UUID
	TenantID    *uuid.UUID
	Key         string
	Name        string
	IsSystem    bool
	RequiresMFA bool
	Permissions []string
}

// PlatformUser es un usuario de la plataforma con sus membresías (vista de
// GET /platform/users).
type PlatformUser struct {
	ID            uuid.UUID
	Email         string
	DisplayName   string
	Status        string
	MFAEnabled    bool
	PlatformRoles []string
	Memberships   []PlatformMembership
	CreatedAt     time.Time
}

// PlatformMembership resume una membresía para la plataforma.
type PlatformMembership struct {
	TenantID   uuid.UUID
	TenantSlug string
	RoleKeys   []string
}

// Invitation es una invitación pendiente a un tenant (solo el SHA-256 del
// token se guarda) con el estado de lo que la rodea.
type Invitation struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	MembershipID     uuid.UUID
	UserID           uuid.UUID
	ExpiresAt        time.Time
	UsedAt           *time.Time
	MembershipStatus string
	UserStatus       string
	UserEmail        string
	UserDisplayName  string
	// TenantName es el nombre del ISP (página de aceptación).
	TenantName string
}

// Usable indica si la invitación puede aceptarse en now.
func (i *Invitation) Usable(now time.Time) bool {
	return i.UsedAt == nil && now.Before(i.ExpiresAt) && i.MembershipStatus == MembershipInvited
}

// NewInvitationToken genera un token de invitación opaco de 256 bits
// (base64url, 43 caracteres).
func NewInvitationToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

var scopeRe = regexp.MustCompile(`^(tenant|site:[0-9a-f-]{36}|router_group:[0-9a-f-]{36})$`)

// ParseScope interpreta un alcance del contrato (`tenant`, `site:<uuid>`,
// `router_group:<uuid>`).
func ParseScope(s string) (scopeType string, scopeID *uuid.UUID, ok bool) {
	if !scopeRe.MatchString(s) {
		return "", nil, false
	}
	if s == "tenant" {
		return "tenant", nil, true
	}
	typ, id, _ := strings.Cut(s, ":")
	u, err := uuid.Parse(id)
	if err != nil {
		return "", nil, false
	}
	return typ, &u, true
}
