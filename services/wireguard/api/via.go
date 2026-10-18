// Package api es el contrato público en proceso del módulo wireguard
// (ADR-0025 §1): lo que otros módulos pueden pedir al hub de túneles cuando
// está en el mismo proceso, registrado en module.Services.
//
// ViaPaths lo usa devices al generar el alta de un switch vía router
// (ADR-0032): asigna la dirección Horus del equipo (IPAM de túneles), la
// enruta por el túnel del router del sitio y genera la configuración que el
// administrador pega en ese router (NAT 1:1 y cortafuegos mínimo). Entre
// procesos falta el RPC equivalente; devices responde 503 si wireguard no
// es local.
package api

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// ServiceViaPaths → ViaPaths.
const ServiceViaPaths = "wireguard.ViaPaths"

// Errores de ViaPaths.
var (
	// ErrViaRouterInvalid: el router no existe en el tenant, no es un router
	// o no tiene túnel con Horus (wireguard, ipsec o gre).
	ErrViaRouterInvalid = errors.New("wireguard: via router invalid")
	// ErrViaTargetInUse: otro equipo ya usa esa IP privada a través del
	// mismo router.
	ErrViaTargetInUse = errors.New("wireguard: private address already mapped through this router")
	// ErrViaPoolExhausted: no quedan direcciones libres en los rangos del hub.
	ErrViaPoolExhausted = errors.New("wireguard: tunnel pool exhausted")
)

// ViaSubnet es una red de gestión declarada en el router del sitio.
type ViaSubnet struct {
	ID     uuid.UUID
	Prefix netip.Prefix
}

// ViaPathRequest pide el camino de un equipo a través de un router.
type ViaPathRequest struct {
	TenantID   uuid.UUID
	DeviceID   uuid.UUID
	DeviceName string
	// RouterID: router del sitio (con túnel) por el que Horus alcanza al equipo.
	RouterID uuid.UUID
	// Target: IP privada de gestión del equipo.
	Target netip.Addr
	// Subnet: red de gestión declarada en el router que contiene Target
	// (nil = dirección Horus /32 propia).
	Subnet *ViaSubnet
	// SFlow: el equipo exporta sFlow (la configuración del router abre
	// UDP 6343 del equipo hacia el colector).
	SFlow bool
}

// RouterConfig es la configuración que el administrador pega en el router
// del sitio (Horus nunca escribe en el router).
type RouterConfig struct {
	// Language: routeros | vrp.
	Language string
	Title    string
	Config   string
	Notes    []string
	Warnings []string
}

// ViaPath es el camino resuelto.
type ViaPath struct {
	// Address: dirección Horus del equipo (identidad de SNMP y sFlow).
	Address netip.Addr
	// Block: bloque Horus de la red de gestión (nil sin red declarada).
	Block *netip.Prefix
	// Transport y ProtectionLevel del túnel del router (el equipo los hereda
	// en el tramo hub ↔ router).
	Transport       string
	ProtectionLevel string
	RouterName      string
	// PollerAddress: IP con la que Horus consulta SNMP (la de servicios del
	// hub); CollectorAddress/CollectorPort: destino de sFlow.
	PollerAddress    netip.Addr
	ServicesCIDR     netip.Prefix
	CollectorAddress netip.Addr
	CollectorPort    int
	RouterConfig     RouterConfig
	// Tunnel: estado del túnel del router (peer.status del hub).
	TunnelStatus string
	At           time.Time
}

// ViaPaths es el contrato.
type ViaPaths interface {
	// EnsureViaPath asigna (o reutiliza) la dirección Horus del equipo, la
	// enruta por el túnel del router y devuelve la configuración del router.
	EnsureViaPath(ctx context.Context, req ViaPathRequest) (ViaPath, error)
}
