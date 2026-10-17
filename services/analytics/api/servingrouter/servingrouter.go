// Package servingrouter es el contrato estrecho, en proceso, con el que la
// consulta 360 de integraciones (devices, ADR-0030) pregunta qué router lleva
// de verdad el tráfico de un cliente: el router_id de sus flujos más recientes.
//
// La consulta va a flows.flows_raw filtrando por todo el prefijo de su clave
// de ordenación (tenant_id, site_id, realm_id, client_ip) y una ventana
// corta de ts: ClickHouse lee unos pocos gránulos. Más allá de la retención
// de flows_raw (7 días) no hay respuesta y quien llama usa el router
// principal del nodo.
//
// Las direcciones son datos personales: no se registran.
package servingrouter

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// ServiceServingRouter → Reader en module.Services.
const ServiceServingRouter = "analytics.ServingRouter"

// DefaultLookback es la ventana hacia atrás desde At en la que se buscan
// flujos del cliente.
const DefaultLookback = 24 * time.Hour

// ErrUnavailable: ClickHouse caído o sin configurar.
var ErrUnavailable = errors.New("servingrouter: analytics unavailable")

// Query identifica al cliente (nodo, realm y su dirección o prefijo
// canónico) y el instante de la consulta.
type Query struct {
	SiteID  uuid.UUID
	RealmID uuid.UUID
	Prefix  netip.Prefix
	At      time.Time
	// Lookback: 0 = DefaultLookback.
	Lookback time.Duration
}

// Serving es el router de los flujos más recientes del cliente.
type Serving struct {
	RouterID uuid.UUID
	// LastFlowAt es el flujo más reciente del cliente por ese router.
	LastFlowAt time.Time
}

// Reader resuelve el router que sirve a un cliente (nil, nil si no hay
// flujos del cliente en la ventana).
type Reader interface {
	ServingRouter(ctx context.Context, tenant uuid.UUID, q Query) (*Serving, error)
}

// Range devuelve las direcciones IPv6 (IPv4 mapeada) inicial y final del
// prefijo, en la forma de la columna client_ip.
func Range(p netip.Prefix) (lo, hi netip.Addr) {
	p = p.Masked()
	a := p.Addr()
	bits := p.Bits()
	if a.Is4() {
		bits += 96
	}
	b := netip.AddrFrom16(a.As16()).As16()
	h := b
	for i := bits; i < 128; i++ {
		h[i/8] |= 1 << (7 - uint(i%8))
	}
	return netip.AddrFrom16(b), netip.AddrFrom16(h)
}
