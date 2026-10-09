package app

import (
	"context"

	"github.com/hcdestroyer/horus-flow/services/wgagent/internal/domain"
)

// Device es la interfaz WireGuard del hub. Implementaciones: el adaptador
// kernel (wgctrl sobre netlink, o el socket UAPI de wireguard-go) y uno en
// memoria para tests.
type Device interface {
	// Configure fija la clave privada y el puerto de escucha (sin tocar peers).
	Configure(ctx context.Context, privateKey string, listenPort int) error
	// Peers devuelve los peers observados (con handshake y contadores).
	Peers(ctx context.Context) ([]domain.Peer, error)
	// Apply añade o actualiza upsert (allowed-ips reemplazadas) y borra remove.
	Apply(ctx context.Context, upsert []domain.Peer, remove []string) error
}
