// Package app contiene los casos de uso del módulo wireguard: túnel por
// router (IP /32 de la IPAM de plataforma), script de alta con token de
// enrolamiento de un uso, script inverso, enrolamiento público de la clave
// pública, estado deseado del hub (wg-agent) y estado observado.
package app

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/wireguard/internal/domain"
)

// Errores del repositorio.
var (
	ErrTokenInvalid = errors.New("wireguard: enrollment token invalid")
	ErrKeyInUse     = errors.New("wireguard: public key in use")
	ErrBadKey       = errors.New("wireguard: invalid public key")
	ErrPeerExists   = errors.New("wireguard: router already has a peer")
	// ErrVersionChanged: If-Match no coincide con la versión del hub.
	ErrVersionChanged = errors.New("wireguard: hub version changed")
	// ErrPoolOverlap: el rango se solapa con otro (EXCLUDE de ip_pool).
	ErrPoolOverlap = errors.New("wireguard: pool overlaps an existing range")
	// ErrPoolInUse: el rango tiene direcciones asignadas o en cuarentena.
	ErrPoolInUse = errors.New("wireguard: pool has allocations")
	// ErrLastPool: el hub se quedaría sin rangos.
	ErrLastPool = errors.New("wireguard: last pool of the hub")
	// ErrPoolNotFound: el rango no existe en ese hub.
	ErrPoolNotFound = errors.New("wireguard: pool not found")
	// ErrHubNameInUse: otro hub ya tiene ese nombre.
	ErrHubNameInUse = errors.New("wireguard: hub name in use")
)

// Hub es el servidor WireGuard de plataforma.
type Hub struct {
	ID           uuid.UUID
	Name         string
	Endpoint     string
	ListenPort   int
	PublicKey    string
	ServicesCIDR netip.Prefix
	Pools        []netip.Prefix
	// PoolInfo son los rangos con su id, alta y ocupación (mismo orden que Pools).
	PoolInfo  []Pool
	MTU       int
	Keepalive int
	Version   int
	// Overrides son los campos editados desde la consola (domain.Field*).
	Overrides      []string
	Status         string
	DesiredVersion int64
	AppliedVersion int64
	LastReportAt   *time.Time
	// Ocupación (listado de plataforma).
	AddressesTotal int
	AddressesUsed  int
	PeersActive    int
	Tenants        int
}

// Pool es un rango de túneles del hub.
type Pool struct {
	ID        uuid.UUID
	CIDR      netip.Prefix
	CreatedAt time.Time
	// Total: /32 asignables a routers; Used: asignaciones no liberadas.
	Total int
	Used  int
}

// HubPatch son los campos editables del hub (nil = sin cambio).
type HubPatch struct {
	Name      *string `json:"name"`
	Endpoint  *string `json:"endpoint"`
	MTU       *int    `json:"mtu"`
	Keepalive *int    `json:"persistent_keepalive_seconds"`
}

// EnvIgnored es un valor de HORUS_WG_* que el arranque no aplicó porque el
// campo se editó desde la consola.
type EnvIgnored struct {
	Field   string
	Env     string
	Current string
}

// PeerQuery filtra el listado de peers.
type PeerQuery struct {
	RouterID       *uuid.UUID
	Status         string
	HandshakeState string
	SortDesc       bool
	// Keyset por (last_handshake_at NULLS FIRST, id).
	AfterKey *time.Time
	AfterNil bool
	AfterID  uuid.UUID
	Limit    int
}

// Observed es un peer observado por el agente.
type Observed struct {
	PublicKey     string
	Endpoint      *string
	LastHandshake *time.Time
	RxBytes       uint64
	TxBytes       uint64
}

// TunnelObserved es un túnel ipsec/gre observado por tunnel-agent.
type TunnelObserved struct {
	PeerID uuid.UUID
	// State: established|connecting|down (ipsec) o up|down (gre).
	State        string
	Alive        bool
	LastRekeyAt  *time.Time
	LastPacketAt *time.Time
	Endpoint     *string
	RxBytes      uint64
	TxBytes      uint64
	Error        *string
}

// Transition es un cambio de estado detectado al aplicar un reporte.
type Transition struct {
	Peer      domain.Peer
	Activated bool
	// Handshake: "", "stale" o "recovered".
	Handshake string
}

// PeerEvents construye los eventos de un cambio de peer.
type PeerEvents func(p *domain.Peer) []outbox.Event

// Store es el repositorio PostgreSQL del módulo.
type Store interface {
	ViaStore

	// EnsureHub da de alta (o actualiza) el hub y sus rangos de túneles
	// desde la configuración. No sobrescribe los campos editados desde la
	// consola (los devuelve si el entorno trae otro valor) ni borra rangos.
	EnsureHub(ctx context.Context, h Hub) ([]EnvIgnored, error)
	GetHub(ctx context.Context, id uuid.UUID) (*Hub, error)
	ListHubs(ctx context.Context) ([]Hub, error)
	// UpdateHub aplica patch si la versión es expect (ErrVersionChanged si
	// no; domain.ErrNotFound si no existe), marca los campos como editados
	// desde la consola y sube la versión.
	UpdateHub(ctx context.Context, id uuid.UUID, expect int, patch HubPatch) (*Hub, error)
	// AddPool añade un rango al hub (ErrPoolOverlap si se solapa con otro de
	// cualquier hub), sube la versión del hub y la deseada del agente.
	AddPool(ctx context.Context, hubID uuid.UUID, cidr netip.Prefix) (*Pool, error)
	// DeletePool quita un rango del hub: domain.ErrNotFound si no es suyo,
	// ErrPoolInUse con asignaciones vivas o liberadas después de
	// quarantineSince, ErrLastPool si es el último. Devuelve el rango borrado.
	DeletePool(ctx context.Context, hubID, poolID uuid.UUID, quarantineSince time.Time, guard func(Pool) error) (*Pool, error)

	// ActivePeer devuelve el peer vivo del router (domain.ErrNotFound si no).
	ActivePeer(ctx context.Context, t pgdb.TenantID, routerID uuid.UUID) (*domain.Peer, error)
	// CreatePeer asigna la primera /32 libre de los rangos del hub (única en
	// la plataforma, cuarentena 24 h) y crea el peer awaiting_enrollment.
	CreatePeer(ctx context.Context, t pgdb.TenantID, p *domain.Peer, ev PeerEvents) error
	// RevokePeer revoca el peer, libera su IP, revoca sus tokens y sube la
	// versión deseada del hub si tenía clave.
	RevokePeer(ctx context.Context, t pgdb.TenantID, id uuid.UUID, reason string, at time.Time, ev PeerEvents) error
	GetPeer(ctx context.Context, t pgdb.TenantID, id uuid.UUID) (*domain.Peer, error)
	ListPeers(ctx context.Context, t pgdb.TenantID, q PeerQuery) ([]domain.Peer, error)
	// AllPeers devuelve los peers vivos de todos los tenants (plataforma).
	AllPeers(ctx context.Context) ([]domain.Peer, error)

	// IssueToken revoca los tokens pendientes del router e inserta tok.
	IssueToken(ctx context.Context, t pgdb.TenantID, tok *domain.Token) error
	// RevokeToken revoca un token pendiente (domain.ErrNotFound si no hay).
	RevokeToken(ctx context.Context, t pgdb.TenantID, id uuid.UUID, at time.Time) error
	// TokenHash devuelve el hash de un token del ISP (domain.ErrNotFound si
	// no existe); el alta manual lo usa para registrar la clave con Enroll.
	TokenHash(ctx context.Context, t pgdb.TenantID, id uuid.UUID) ([]byte, error)
	// Enroll consume el token de hash y registra publicKey en su peer
	// (plataforma: la petición no trae tenant). ErrTokenInvalid sin
	// distinguir el motivo; ErrKeyInUse/ErrBadKey cuentan como fallo del token.
	Enroll(ctx context.Context, hash []byte, publicKey string, from netip.Addr, at time.Time,
		ev func(p *domain.Peer, tok *domain.Token) []outbox.Event) (*domain.Peer, error)

	// Emit escribe eventos sueltos en la outbox (auditoría sin auth local).
	Emit(ctx context.Context, evs []outbox.Event) error

	// DesiredState devuelve la versión deseada y los peers con clave del hub.
	DesiredState(ctx context.Context, hubID uuid.UUID) (int64, []domain.Peer, error)
	// ApplyReport guarda el estado observado y devuelve las transiciones
	// (activación y handshake stale/recovered), emitiendo sus eventos.
	ApplyReport(ctx context.Context, hubID uuid.UUID, applied int64, up bool, at time.Time, obs []Observed,
		ev func(tr Transition) []outbox.Event) (desired int64, out []Transition, err error)

	// MarkTunnelConfigured pasa un túnel ipsec/gre de awaiting_enrollment a
	// pending_handshake (configuración generada) y sube la versión deseada
	// de tunnel-agent (PSK nueva).
	MarkTunnelConfigured(ctx context.Context, t pgdb.TenantID, id uuid.UUID, at time.Time, ev PeerEvents) (*domain.Peer, error)
	// TunnelDesiredState devuelve la versión deseada de tunnel-agent y sus
	// túneles configurados.
	TunnelDesiredState(ctx context.Context, hubID uuid.UUID) (int64, []domain.Peer, error)
	// ApplyTunnelReport guarda el estado observado por tunnel-agent y
	// devuelve las transiciones, emitiendo sus eventos.
	ApplyTunnelReport(ctx context.Context, hubID uuid.UUID, applied int64, ikeUp bool, at time.Time, obs []TunnelObserved,
		ev func(tr Transition) []outbox.Event) (desired int64, out []Transition, err error)
}
