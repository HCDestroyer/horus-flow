// Package domain contiene las reglas puras del módulo wireguard: IPAM del
// rango de túneles (ADR-0022 §1.2), tokens de enrolamiento (docs/api.md §2.4),
// claves públicas WireGuard y estados de peer y handshake.
package domain

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
)

// CGNAT es el rango que nunca puede usarse para túneles (ADR-0022 §1.2).
var CGNAT = netip.MustParsePrefix("100.64.0.0/10")

// Errores de la IPAM.
var (
	ErrPoolExhausted = errors.New("wireguard: tunnel ip pool exhausted")
	ErrInvalidPool   = errors.New("wireguard: invalid tunnel pool")
)

// PoolPlan es la configuración validada del rango de túneles.
type PoolPlan struct {
	Pools    []netip.Prefix
	Services netip.Prefix
	// HubAddress es la IP del hub dentro de la red de servicios (primera útil).
	HubAddress netip.Addr
}

// ValidatePools valida los rangos de túneles y la red de servicios: IPv4,
// canónicos, sin solaparse entre sí ni con 100.64.0.0/10, servicios dentro
// de un rango y al menos una /32 libre para routers.
func ValidatePools(pools []netip.Prefix, services netip.Prefix) (PoolPlan, error) {
	if len(pools) == 0 {
		return PoolPlan{}, fmt.Errorf("%w: no tunnel ranges", ErrInvalidPool)
	}
	inside := false
	for i, p := range pools {
		if !p.IsValid() || !p.Addr().Is4() || p.Masked() != p {
			return PoolPlan{}, fmt.Errorf("%w: %s must be a canonical IPv4 CIDR", ErrInvalidPool, p)
		}
		if p.Overlaps(CGNAT) {
			return PoolPlan{}, fmt.Errorf("%w: %s overlaps CGNAT 100.64.0.0/10", ErrInvalidPool, p)
		}
		if p.Bits() > 30 {
			return PoolPlan{}, fmt.Errorf("%w: %s is too small", ErrInvalidPool, p)
		}
		for _, q := range pools[:i] {
			if p.Overlaps(q) {
				return PoolPlan{}, fmt.Errorf("%w: %s overlaps %s", ErrInvalidPool, p, q)
			}
		}
		if services.IsValid() && p.Bits() <= services.Bits() && p.Contains(services.Addr()) {
			inside = true
			if p.Bits() == services.Bits() {
				return PoolPlan{}, fmt.Errorf("%w: services %s leaves no router addresses in %s", ErrInvalidPool, services, p)
			}
		}
	}
	if !services.IsValid() || !services.Addr().Is4() || services.Masked() != services || services.Bits() > 30 {
		return PoolPlan{}, fmt.Errorf("%w: services CIDR %s must be a canonical IPv4 CIDR (≤ /30)", ErrInvalidPool, services)
	}
	if !inside {
		return PoolPlan{}, fmt.Errorf("%w: services %s must be inside a tunnel range", ErrInvalidPool, services)
	}
	return PoolPlan{Pools: slices.Clone(pools), Services: services, HubAddress: services.Addr().Next()}, nil
}

// lastAddr devuelve la última dirección de p.
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Addr().As4()
	host := uint32(1)<<(32-p.Bits()) - 1
	v := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) | host
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// NextFree devuelve la primera dirección de pool libre: ni red ni broadcast,
// fuera de la red de servicios y no presente en used (asignadas o en
// cuarentena).
func NextFree(pool, services netip.Prefix, used map[netip.Addr]bool) (netip.Addr, error) {
	last := lastAddr(pool)
	for a := pool.Addr().Next(); a.IsValid() && a.Less(last); a = a.Next() {
		if services.Contains(a) {
			a = lastAddr(services) // saltar la red de servicios entera
			continue
		}
		if !used[a] {
			return a, nil
		}
	}
	return netip.Addr{}, ErrPoolExhausted
}

// Capacity es el número de /32 asignables a routers en pool.
func Capacity(pool, services netip.Prefix) int {
	n := 1<<(32-pool.Bits()) - 2
	if pool.Bits() <= services.Bits() && pool.Contains(services.Addr()) {
		n -= 1 << (32 - services.Bits())
		// la red y el broadcast de services ya no se cuentan dos veces si
		// coinciden con los del pool
		if services.Addr() == pool.Addr() {
			n++
		}
	}
	return max(n, 0)
}
