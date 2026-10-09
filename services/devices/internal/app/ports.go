// Package app contiene los casos de uso del inventario por ISP: nodos,
// router principal (uno por nodo, RouterOS 7.x con aviso si < 7.12) y
// prefijos de clientes sin solapes por realm, con eventos C4 por outbox.
// Toda operación recibe el tenant del token (TenantScope implícito en
// pgdb.TenantID) y el alcance de los permisos del principal.
package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/devices/internal/domain"
)

// Errores del repositorio (traducidos por el servicio a códigos del contrato).
var (
	ErrCodeTaken      = errors.New("devices: code taken")
	ErrHostnameTaken  = errors.New("devices: hostname taken")
	ErrPrimaryExists  = errors.New("devices: primary router exists")
	ErrSiteNotEmpty   = errors.New("devices: site not empty")
	ErrOverlap        = errors.New("devices: client prefix overlap")
	ErrRefNotFound    = errors.New("devices: referenced entity not found")
	ErrVersionChanged = errors.New("devices: version changed")
)

// ListQuery es un listado keyset.
type ListQuery struct {
	Q       string
	SortCol string // name | created_at
	Desc    bool
	// AfterKey/AfterID: última clave vista (texto de name o RFC 3339 de created_at).
	AfterKey string
	AfterID  uuid.UUID
	Limit    int
	// Sites limita a esos nodos (alcance del permiso); nil = todos.
	Sites []uuid.UUID
	// Filtros específicos.
	Kind            string
	ParentID        *uuid.UUID
	SiteIDs         []uuid.UUID
	OnboardingState string
	IsPrimary       *bool
	Role            string
	SiteID          uuid.UUID
}

// Events construye los eventos de un cambio con el estado final.
type Events[T any] func(v *T) []outbox.Event

// Store es el repositorio PostgreSQL del módulo (siempre dentro del tenant).
type Store interface {
	ListSites(ctx context.Context, t pgdb.TenantID, q ListQuery) ([]domain.Site, error)
	CountSites(ctx context.Context, t pgdb.TenantID, q ListQuery) (int, error)
	GetSite(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Site, error)
	CreateSite(ctx context.Context, t pgdb.TenantID, s *domain.Site, ev Events[domain.Site]) error
	UpdateSite(ctx context.Context, t pgdb.TenantID, s *domain.Site, expect int, ev Events[domain.Site]) error
	DeleteSite(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int, ev func(*domain.Site, []domain.ClientPrefix) []outbox.Event) error

	ListRouters(ctx context.Context, t pgdb.TenantID, q ListQuery) ([]domain.Router, error)
	CountRouters(ctx context.Context, t pgdb.TenantID, q ListQuery) (int, error)
	GetRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Router, error)
	CreateRouter(ctx context.Context, t pgdb.TenantID, r *domain.Router, ev Events[domain.Router]) error
	UpdateRouter(ctx context.Context, t pgdb.TenantID, r *domain.Router, expect int, ev Events[domain.Router]) error
	DeleteRouter(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int, ev Events[domain.Router]) error

	ListPrefixes(ctx context.Context, t pgdb.TenantID, q ListQuery) ([]domain.ClientPrefix, error)
	GetPrefix(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.ClientPrefix, error)
	CreatePrefix(ctx context.Context, t pgdb.TenantID, p *domain.ClientPrefix, ev Events[domain.ClientPrefix]) error
	UpdatePrefix(ctx context.Context, t pgdb.TenantID, p *domain.ClientPrefix, expect int, ev Events[domain.ClientPrefix]) error
	DeletePrefix(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int, ev Events[domain.ClientPrefix]) error
}
