// Package app contiene los casos de uso del módulo auth: login con bloqueo
// progresivo, segundo factor TOTP, sesiones revocables con refresh rotativo y
// detección de reutilización, emisión de tokens por tenant o de plataforma,
// cuenta propia, catálogo de permisos, gestión de ISP por la plataforma,
// comprobación de sesión para el gateway y auditoría con cadena de hashes.
package app

import (
	"context"
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

// NewTenant es el alta de un ISP con su primer administrador.
type NewTenant struct {
	Tenant     domain.Tenant
	AdminEmail string
	AdminRole  uuid.UUID
	Actor      Actor
	Events     func(t domain.Tenant, membershipID, userID uuid.UUID, membershipStatus string) []OutboxEvent
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
	CreateTenant(ctx context.Context, nt NewTenant) error
	UpdateTenant(ctx context.Context, t *domain.Tenant, expectVersion int, ev func(domain.Tenant) OutboxEvent) (bool, error)

	SyncCatalog(ctx context.Context, c *domain.Catalog) error
	EnsureSeedAdmin(ctx context.Context, u *domain.User) (bool, error)
	InsertAudit(ctx context.Context, e api.AuditEntry) error
}
