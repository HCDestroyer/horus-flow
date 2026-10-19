package domain

import (
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// Switches vía router del sitio (ADR-0032, docs/tunnels.md §10). Un switch
// con IP de gestión privada se alcanza a través del túnel de un router del
// sitio (wireguard, ipsec o gre): Horus le asigna una dirección propia (de la
// IPAM de túneles, la gestiona wireguard) y el router hace NAT 1:1 entre esa
// dirección y la IP privada. El switch no tiene túnel propio.

// ConnectionViaRouter es el modo de un switch alcanzado vía router.
const ConnectionViaRouter = "via_router"

// SwitchConnectionModes son los modos de un switch: direct (Horus está en la
// misma red de gestión) o via_router (Horus en otra red: lo habitual).
var SwitchConnectionModes = []string{ConnectionDirect, ConnectionViaRouter}

// Códigos de error de los switches vía router.
const (
	// CodeViaRouterInvalid: el router elegido no existe en el ISP, no es un
	// router o no tiene túnel con Horus.
	CodeViaRouterInvalid = "VIA_ROUTER_INVALID"
	// CodeRouterHasViaSwitches: el router lleva switches vía router; no se
	// puede dar de baja ni pasar a modo directo sin moverlos antes.
	CodeRouterHasViaSwitches = "ROUTER_HAS_VIA_SWITCHES"
	// Redes de gestión declaradas en un router del sitio.
	CodeManagementSubnetNotFound = "MANAGEMENT_SUBNET_NOT_FOUND"
	CodeManagementSubnetOverlap  = "MANAGEMENT_SUBNET_OVERLAP"
	CodeManagementSubnetInUse    = "MANAGEMENT_SUBNET_IN_USE"
)

// Límites de una red de gestión declarada (bloque Horus del mismo tamaño).
const (
	ManagementSubnetMinBits = 22
	ManagementSubnetMaxBits = 30
	// MaxManagementSubnets por router.
	MaxManagementSubnets = 16
)

// IsViaRouter indica si el equipo se alcanza vía router.
func (r *Router) IsViaRouter() bool { return r.ConnectionMode == ConnectionViaRouter }

// ManagementSubnet es una red de gestión declarada en un router del sitio:
// los switches vía ese router con IP dentro de ella comparten una sola regla
// NAT (bloque Horus del mismo tamaño).
type ManagementSubnet struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	RouterID  uuid.UUID
	Prefix    netip.Prefix
	CreatedAt time.Time
	CreatedBy string
}

// ValidManagementSubnet acepta una red IPv4 canónica privada (RFC 1918 o
// CGNAT) entre /22 y /30.
func ValidManagementSubnet(p netip.Prefix) bool {
	return p.IsValid() && p.Addr().Is4() && p.Masked() == p && p.Bits() >= ManagementSubnetMinBits &&
		p.Bits() <= ManagementSubnetMaxBits && IsPrivate(p)
}

// RouterRef resume el router del sitio de un switch vía router (lectura; no
// se guarda): la consola muestra «vía router X por WireGuard/IPsec/GRE» y el
// nivel de protección heredado de su túnel.
type RouterRef struct {
	ID              uuid.UUID
	Name            string
	ConnectionMode  string
	ProtectionLevel string
}
