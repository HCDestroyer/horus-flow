package domain

import (
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// Switches vía router del sitio (ADR-0032). Un equipo de monitoreo con IP de
// gestión privada se alcanza por el túnel de un router del sitio: recibe una
// DIRECCIÓN HORUS de la IPAM de túneles (única en la plataforma) y el router
// hace NAT 1:1 (netmap) entre esa dirección y la IP privada. El hub nunca ve
// ni enruta IP privadas de los ISP, así que las repetidas (192.168.88.2 en
// dos ISP, o en dos sitios del mismo ISP) no chocan.

// Tipos de asignación vía router.
const (
	ViaKindDevice = "device"
	ViaKindSubnet = "subnet"
)

// Límites de una red de gestión declarada (bloque Horus del mismo tamaño).
const (
	ViaSubnetMinBits = 22
	ViaSubnetMaxBits = 30
)

// Códigos de error del contrato.
const (
	// CodeViaRouterInvalid: el router elegido no existe, no es un router o no
	// tiene túnel con Horus (wireguard, ipsec o gre).
	CodeViaRouterInvalid = "VIA_ROUTER_INVALID"
	// CodeViaTargetInUse: otro equipo ya usa esa IP privada a través del
	// mismo router.
	CodeViaTargetInUse = "MANAGEMENT_ADDRESS_IN_USE"
)

// Errores del repositorio.
var (
	// ErrViaTargetInUse: la IP privada ya es de otro equipo por ese router.
	ErrViaTargetInUse = errors.New("wireguard: private address already mapped through this router")
	// ErrViaSubnetOverlap: la red se solapa con otra declarada en el router.
	ErrViaSubnetOverlap = errors.New("wireguard: management subnet overlaps another one of the router")
)

// ViaMapping es la dirección (o bloque) Horus de un equipo o red de gestión
// alcanzados a través del túnel de un router.
type ViaMapping struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	ServerID uuid.UUID
	// RouterID: router del sitio cuyo túnel lleva el tráfico.
	RouterID uuid.UUID
	Kind     string
	// DeviceID: el switch (kind device).
	DeviceID *uuid.UUID
	// SubnetID: la red de gestión declarada (kind subnet, o el switch que cae
	// dentro de ella).
	SubnetID *uuid.UUID
	// ParentID: el bloque de la red que contiene a este switch.
	ParentID *uuid.UUID
	// Target: IP privada (/32) o red de gestión; Mapped: dirección o bloque
	// Horus del mismo tamaño.
	Target     netip.Prefix
	Mapped     netip.Prefix
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ReleasedAt *time.Time
	Version    int
}

// Routed indica si la asignación se enruta por sí misma en el hub (las de
// primer nivel; un switch dentro de un bloque viaja con el bloque).
func (m *ViaMapping) Routed() bool { return m.ParentID == nil }

// MapInto calcula la dirección Horus de addr dentro de un bloque: mismo
// desplazamiento en mapped que addr en target (netmap 1:1).
func MapInto(target, mapped netip.Prefix, addr netip.Addr) (netip.Addr, bool) {
	if !target.Contains(addr) || target.Bits() != mapped.Bits() || !addr.Is4() || !mapped.Addr().Is4() {
		return netip.Addr{}, false
	}
	off := u32(addr) - u32(target.Masked().Addr())
	return a32(u32(mapped.Masked().Addr()) + off), true
}

func u32(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func a32(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// NextFreeBlock devuelve el primer bloque alineado de tamaño /bits dentro de
// pool sin ninguna dirección usada (asignada o en cuarentena), sin tocar la
// red de servicios ni la red/broadcast del pool. bits = 32 equivale a
// NextFree.
func NextFreeBlock(pool, services netip.Prefix, used map[netip.Addr]bool, bits int) (netip.Prefix, error) {
	if bits == 32 {
		a, err := NextFree(pool, services, used)
		if err != nil {
			return netip.Prefix{}, err
		}
		return netip.PrefixFrom(a, 32), nil
	}
	if !pool.IsValid() || !pool.Addr().Is4() || bits <= pool.Bits() || bits > 32 {
		return netip.Prefix{}, ErrPoolExhausted
	}
	size := uint32(1) << (32 - bits)
	first, last := u32(pool.Masked().Addr()), u32(lastAddr(pool))
	for base := first; base+size-1 <= last && base >= first; base += size {
		blk := netip.PrefixFrom(a32(base), bits)
		if base == first || base+size-1 == last || blk.Overlaps(services) {
			continue // ni la red ni el broadcast del pool, ni la red de servicios
		}
		free := true
		for a := base; a < base+size; a++ {
			if used[a32(a)] {
				free = false
				break
			}
		}
		if free {
			return blk, nil
		}
	}
	return netip.Prefix{}, ErrPoolExhausted
}
