// Package app contiene los casos de uso del inventario por ISP: nodos,
// router principal (uno por nodo, RouterOS 7.x con aviso si < 7.12) y
// prefijos de clientes sin solapes por realm, con eventos C4 por outbox.
// Toda operación recibe el tenant del token (TenantScope implícito en
// pgdb.TenantID) y el alcance de los permisos del principal.
package app

import (
	"context"
	"errors"
	"net/netip"

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
	// ErrManagementAddressTaken: otro router verificado usa esa dirección.
	ErrManagementAddressTaken = errors.New("devices: management address taken")
	// ErrTunnelRemoteTaken: otro router usa esa IP pública (gre) o esa
	// identidad IKE (ipsec).
	ErrTunnelRemoteTaken = errors.New("devices: tunnel remote taken")
	// ErrViaAddressTaken: la dirección Horus ya es de otro equipo (no debería
	// ocurrir: la asigna la IPAM de wireguard).
	ErrViaAddressTaken = errors.New("devices: via address taken")
	// ErrSubnetOverlap: la red de gestión se solapa con otra del router.
	ErrSubnetOverlap = errors.New("devices: management subnet overlaps")
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
	// DeviceType: router | switch; vacío = todos.
	DeviceType string
	// ViaRouterID: solo los equipos alcanzados vía ese router (ADR-0032).
	ViaRouterID *uuid.UUID
	Role        string
	SiteID      uuid.UUID
}

// ViaStore es la parte del repositorio de los switches vía router y de las
// redes de gestión declaradas en los routers del sitio (ADR-0032).
type ViaStore interface {
	// CountViaDevices cuenta los equipos vivos alcanzados vía el router.
	CountViaDevices(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) (int, error)
	ListManagementSubnets(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) ([]domain.ManagementSubnet, error)
	// CreateManagementSubnet: ErrSubnetOverlap si se solapa con otra del router.
	CreateManagementSubnet(ctx context.Context, t pgdb.TenantID, s *domain.ManagementSubnet) error
	// DeleteManagementSubnet: domain.ErrNotFound si no es de ese router.
	DeleteManagementSubnet(ctx context.Context, t pgdb.TenantID, routerID, id uuid.UUID) error
	// ManagementSubnetFor devuelve la red declarada del router que contiene
	// addr (domain.ErrNotFound si ninguna).
	ManagementSubnetFor(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, addr netip.Addr) (*domain.ManagementSubnet, error)
	// ViaDevicesIn cuenta los equipos vivos vía el router con IP en p.
	ViaDevicesIn(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID, p netip.Prefix) (int, error)
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
	OverlapFinder
	UpdatePrefix(ctx context.Context, t pgdb.TenantID, p *domain.ClientPrefix, expect int, ev Events[domain.ClientPrefix]) error
	DeletePrefix(ctx context.Context, t pgdb.TenantID, id uuid.UUID, expect int, ev Events[domain.ClientPrefix]) error
}

// PrefixClash es el prefijo activo que impide declarar otro (solape en el realm).
type PrefixClash struct {
	Prefix   netip.Prefix
	Role     string
	SiteID   uuid.UUID
	SiteName string
}

// OverlapFinder localiza el prefijo con el que se solapa uno nuevo, para que
// el 409 CLIENT_PREFIX_OVERLAP diga con cuál (el más específico si hay varios).
type OverlapFinder interface {
	// OverlappingPrefix busca en el realm que le tocaría a p (privado del nodo
	// siteID o público del ISP). Sin solape → domain.ErrNotFound.
	OverlappingPrefix(ctx context.Context, t pgdb.TenantID, siteID uuid.UUID, realmKind string, p netip.Prefix) (*PrefixClash, error)
}
