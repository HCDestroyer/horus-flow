package app

import (
	"context"
	"net/netip"
	"time"

	"github.com/hcdestroyer/horus-flow/services/tunnelagent/internal/domain"
)

// LinkState es una interfaz de túnel observada en el host (hxfrm*/hgre*).
type LinkState struct {
	Transport string
	Index     uint32
	Up        bool
	RxBytes   uint64
	TxBytes   uint64
	RxPackets uint64
}

// Binding liga la /32 de un router a la interfaz por la que debe llegar
// (antisuplantación: lo que entra por hxfrm7 solo puede venir de la /32 del
// túnel 7; la regla de iptables la pone horus-tunnel, el agente mantiene el
// conjunto).
type Binding struct {
	Address   netip.Addr
	Interface string
}

// Links gestiona las interfaces XFRM y GRE del hub, sus rutas y los
// conjuntos (ipset) que usa el cortafuegos. Implementaciones: netlink
// (adapters/linux) y memoria (adapters/memdev, tests).
type Links interface {
	// List devuelve las interfaces del agente con sus contadores.
	List(ctx context.Context) ([]LinkState, error)
	// Ensure crea o corrige la interfaz del túnel (XFRM con if_id = índice,
	// o GRE con local/remoto/clave), la levanta con su MTU y deja la ruta
	// /32 del router por ella (origen preferido: la IP de servicios del hub).
	// No toca una interfaz que ya está como se pide (sin cortes).
	Ensure(ctx context.Context, t domain.Tunnel) error
	// Remove borra la interfaz (y con ella su ruta).
	Remove(ctx context.Context, transport string, index uint32) error
	// SyncFilters deja exactamente esos miembros en los conjuntos del
	// cortafuegos: IP públicas de los peers GRE y ligaduras /32 ↔ interfaz.
	SyncFilters(ctx context.Context, grePeers []netip.Addr, bindings []Binding) error
}

// SA es el estado de la IKE SA de una conexión.
type SA struct {
	// State: established | connecting | down.
	State          string
	ChildInstalled bool
	EstablishedAt  time.Time
	LastRekeyAt    time.Time
	Endpoint       string
	RxBytes        uint64
	TxBytes        uint64
}

// IKE es el demonio IKEv2 (charon de strongSwan por vici). La PSK solo vive
// en su memoria (load-shared), nunca en disco.
type IKE interface {
	// Load carga (o reemplaza) la conexión y la PSK del túnel.
	Load(ctx context.Context, t domain.Tunnel) error
	// Unload descarga la conexión y la PSK y cierra sus SA.
	Unload(ctx context.Context, index uint32) error
	// Conns devuelve los índices de las conexiones horus-* cargadas.
	Conns(ctx context.Context) ([]uint32, error)
	// SAs devuelve el estado de las SA por índice.
	SAs(ctx context.Context) (map[uint32]SA, error)
}
