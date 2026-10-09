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
)

// Hub es el servidor WireGuard de plataforma.
type Hub struct {
	ID             uuid.UUID
	Name           string
	Endpoint       string
	ListenPort     int
	PublicKey      string
	ServicesCIDR   netip.Prefix
	Pools          []netip.Prefix
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
	// EnsureHub da de alta (o actualiza) el hub y sus rangos de túneles.
	EnsureHub(ctx context.Context, h Hub) error
	GetHub(ctx context.Context, id uuid.UUID) (*Hub, error)
	ListHubs(ctx context.Context) ([]Hub, error)

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
}
