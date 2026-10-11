// Package app contiene los casos de uso del módulo auth: login con bloqueo
// progresivo, segundo factor TOTP, sesiones revocables con refresh rotativo y
// detección de reutilización, emisión de tokens por tenant o de plataforma,
// cuenta propia, catálogo de permisos, gestión de ISP por la plataforma,
// comprobación de sesión para el gateway y auditoría con cadena de hashes.
package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/auth/api"
	"github.com/hcdestroyer/horus-flow/services/auth/internal/domain"
)

// TenantFilter filtra el listado de tenants.
type TenantFilter struct {
	Q      string
	Status string
	// After: clave keyset (created_at, id) del último visto.
	AfterCreated *time.Time
	AfterID      uuid.UUID
	Limit        int
}

// OutboxEvent es un evento a publicar por el outbox de auth.
type OutboxEvent struct {
	ID               uuid.UUID
	Type             string // horus.auth.<entidad>.<evento>
	TenantID         *uuid.UUID
	AggregateType    string
	AggregateID      uuid.UUID
	AggregateVersion int
	Actor            Actor
	OccurredAt       time.Time
	Data             map[string]any
}

// Actor de un evento o registro de auditoría.
type Actor struct {
	Type        string
	ID          string
	SID         string
	ViaPlatform bool
}

// NewTenant es el alta de un ISP con su primer administrador. Admin es la
// invitación del administrador (TenantID = Tenant.ID), que se resuelve en la
// misma transacción y con las mismas reglas que InviteMember.
type NewTenant struct {
	Tenant domain.Tenant
	Event  OutboxEvent
	Admin  NewInvitation
}

// SessionStatus es el estado de una sesión y una membresía (gateway).
type SessionStatus struct {
	SessionActive    bool
	RevokedReason    string
	UserActive       bool
	MembershipActive bool
	TenantActive     bool
}

// Store es el repositorio PostgreSQL del módulo.
type Store interface {
	UserByEmail(ctx context.Context, email string) (*domain.User, error)
	UserByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	SetLoginFailure(ctx context.Context, id uuid.UUID, failed int, lockedUntil *time.Time) error
	SetLoginSuccess(ctx context.Context, id uuid.UUID, now time.Time, rehash string) error
	ChangePassword(ctx context.Context, id uuid.UUID, hash string, now time.Time, keepSession uuid.UUID) error

	CreateSession(ctx context.Context, s *domain.Session, rt *domain.RefreshToken) error
	Session(ctx context.Context, id uuid.UUID) (*domain.Session, error)
	SetSessionMFA(ctx context.Context, sid uuid.UUID, now time.Time, amr []string, rt *domain.RefreshToken) (bool, error)
	// MFAFailure suma un fallo de 2FA a la sesión pendiente y devuelve el total.
	MFAFailure(ctx context.Context, sid uuid.UUID) (int, error)
	RevokeSession(ctx context.Context, sid uuid.UUID, reason string, now time.Time, ev *OutboxEvent) error
	RefreshByHash(ctx context.Context, hash []byte) (*domain.RefreshToken, error)
	RotateRefresh(ctx context.Context, oldID uuid.UUID, next *domain.RefreshToken, now time.Time) (bool, error)
	SessionStatus(ctx context.Context, sid, userID, tenantID uuid.UUID) (SessionStatus, error)

	PlatformRoles(ctx context.Context, userID uuid.UUID) ([]string, error)
	Memberships(ctx context.Context, userID uuid.UUID) ([]domain.Membership, error)

	TOTP(ctx context.Context, userID uuid.UUID) (*domain.TOTP, error)
	PutPendingTOTP(ctx context.Context, t *domain.TOTP) error
	ConfirmTOTP(ctx context.Context, userID uuid.UUID, step int64, codeHashes [][]byte, now time.Time, sid uuid.UUID) error
	AdvanceTOTPStep(ctx context.Context, userID uuid.UUID, step int64) (bool, error)
	UseRecoveryCode(ctx context.Context, userID uuid.UUID, hash []byte, now time.Time) (bool, error)

	ListTenants(ctx context.Context, f TenantFilter) ([]domain.Tenant, error)
	CountTenants(ctx context.Context, f TenantFilter) (int, error)
	Tenant(ctx context.Context, id uuid.UUID) (*domain.Tenant, error)
	CreateTenant(ctx context.Context, nt NewTenant) (*InviteResult, error)
	UpdateTenant(ctx context.Context, t *domain.Tenant, expectVersion int, ev func(domain.Tenant) OutboxEvent) (bool, error)
	// SetTenantStatus cambia el estado de from a to (CAS) y emite el evento.
	SetTenantStatus(ctx context.Context, id uuid.UUID, from, to string, at time.Time, ev func(domain.Tenant) OutboxEvent) (bool, error)

	SyncCatalog(ctx context.Context, c *domain.Catalog) error
	EnsureSeedAdmin(ctx context.Context, u *domain.User) (bool, error)
	InsertAudit(ctx context.Context, e api.AuditEntry) error

	// UpdateProfile guarda nombre, idioma, zona horaria y tenant por defecto.
	UpdateProfile(ctx context.Context, u *domain.User, now time.Time) error
	// ListUsers lista los usuarios de la plataforma con sus membresías no
	// revocadas (multi-tenant declarado: auth_platform).
	ListUsers(ctx context.Context, f UserFilter) ([]domain.PlatformUser, error)

	// Miembros y roles del tenant (dentro del tenant, RLS).
	ListMembers(ctx context.Context, tenant uuid.UUID, f MemberFilter) ([]domain.Member, error)
	CountMembers(ctx context.Context, tenant uuid.UUID, f MemberFilter) (int, error)
	// Member devuelve la membresía de userID en el tenant (cualquier estado).
	Member(ctx context.Context, tenant, userID uuid.UUID) (*domain.Member, error)
	TenantRoles(ctx context.Context, tenant uuid.UUID) ([]domain.TenantRoleRecord, error)
	InviteMember(ctx context.Context, in NewInvitation) (*InviteResult, error)
	// RevokeMember quita la membresía no revocada de userID (false si no hay).
	RevokeMember(ctx context.Context, tenant, userID uuid.UUID, now time.Time, ev MemberEvent) (bool, error)
	// ReplaceAssignments sustituye las asignaciones con bloqueo optimista:
	// domain.ErrNotFound si no hay membresía no revocada; ErrMemberVersion
	// (con el miembro actual) si la versión no coincide.
	ReplaceAssignments(ctx context.Context, tenant, userID uuid.UUID, expect int, as []domain.Assignment, now time.Time,
		ev MemberEvent) (*domain.Member, error)
	// InvitationByHash busca una invitación por el hash del token (canje
	// público, sin tenant: auth_platform).
	InvitationByHash(ctx context.Context, hash []byte) (*domain.Invitation, error)
	// AcceptInvitation consume la invitación (un uso), activa la membresía y,
	// si PasswordHash no está vacío, activa al usuario pendiente.
	AcceptInvitation(ctx context.Context, in AcceptedInvitation) error
	// ReissueInvitation sustituye las invitaciones sin usar de la membresía
	// invitada de UserID por una nueva (dentro del tenant, RLS):
	// domain.ErrNotFound si no hay membresía no revocada; ErrNotInvited si no
	// está invitada o la cuenta no puede aceptarla.
	ReissueInvitation(ctx context.Context, in ReissuedInvitation) (*ReissueResult, error)
}

// ReissuedInvitation es un enlace nuevo para una membresía invitada.
type ReissuedInvitation struct {
	TenantID  uuid.UUID
	UserID    uuid.UUID
	TokenHash []byte
	ExpiresAt time.Time
	CreatedBy uuid.UUID
	Now       time.Time
}

// ReissueResult es la membresía cuyo enlace se renovó.
type ReissueResult struct {
	MembershipID uuid.UUID
	Email        string
	// Replaced es el número de invitaciones sin usar invalidadas.
	Replaced int
}

// UserFilter filtra GET /platform/users (keyset por created_at, id).
type UserFilter struct {
	Q            string
	AfterCreated *time.Time
	AfterID      uuid.UUID
	Limit        int
}

// MemberFilter filtra GET /members (keyset por created_at, id). Status
// vacío lista las membresías no revocadas.
type MemberFilter struct {
	Q            string
	Status       string
	AfterCreated *time.Time
	AfterID      uuid.UUID
	Limit        int
}

// MemberEvent construye el evento de una membresía cambiada a partir del
// estado nuevo y de las asignaciones anteriores.
type MemberEvent func(m domain.Member, previous []domain.Assignment) OutboxEvent

// NewInvitation es la invitación de un email a un tenant con sus roles
// iniciales. Si el email no es de un usuario se crea pendiente; si la persona
// ya es miembro (activa o invitada) no se cambian sus roles.
type NewInvitation struct {
	TenantID    uuid.UUID
	Email       string
	Assignments []domain.Assignment
	GrantedBy   uuid.UUID
	// TokenHash y ExpiresAt se usan solo si la membresía queda invitada y la
	// cuenta puede aceptarla (pendiente o activa); las invitaciones anteriores
	// sin usar se sustituyen.
	TokenHash []byte
	ExpiresAt time.Time
	Now       time.Time
	Event     MemberEvent
}

// InviteResult describe lo que hizo InviteMember (para auditoría; la
// respuesta HTTP es la misma en todos los casos).
type InviteResult struct {
	MembershipID uuid.UUID
	UserID       uuid.UUID
	Status       string
	UserCreated  bool
	// AlreadyMember: la persona ya era miembro activa o invitada.
	AlreadyMember bool
	// TokenStored: se guardó una invitación nueva con TokenHash.
	TokenStored bool
}

// AcceptedInvitation es el canje de una invitación.
type AcceptedInvitation struct {
	Invitation   domain.Invitation
	PasswordHash string
	DisplayName  string
	Now          time.Time
	Event        MemberEvent
}

// Errores del repositorio de miembros.
var (
	// ErrMemberVersion: la versión de la membresía no coincide (If-Match).
	ErrMemberVersion = errors.New("auth: membership version changed")
	// ErrInvitationUsed: la invitación ya se usó, caducó o su membresía cambió.
	ErrInvitationUsed = errors.New("auth: invitation not usable")
	// ErrNotInvited: la membresía no está invitada (o la cuenta no puede
	// aceptar una invitación).
	ErrNotInvited = errors.New("auth: membership not invited")
	// ErrUnknownRole: algún rol no es visible en el tenant.
	ErrUnknownRole = errors.New("auth: unknown role")
)

// MemberVersionError lleva el miembro actual de un ErrMemberVersion.
type MemberVersionError struct{ Current *domain.Member }

func (e *MemberVersionError) Error() string { return ErrMemberVersion.Error() }

// Unwrap permite errors.Is(err, ErrMemberVersion).
func (e *MemberVersionError) Unwrap() error { return ErrMemberVersion }

// InvitationSender entrega el enlace de una invitación a la persona invitada
// (p. ej. por email). El token solo existe en memoria durante la entrega; un
// error no debe incluir el token ni el enlace.
type InvitationSender interface {
	SendInvitation(ctx context.Context, inv InvitationDelivery) error
}

// InvitationDelivery es lo que necesita el canal de entrega.
type InvitationDelivery struct {
	Email      string
	TenantID   uuid.UUID
	TenantName string
	Token      string
	// URL es el enlace de aceptación (<HORUS_PUBLIC_BASE_URL>/invitation#token=…).
	URL       string
	ExpiresAt time.Time
}
