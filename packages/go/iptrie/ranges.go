package iptrie

import (
	"fmt"
	"net/netip"
)

// RangeToPrefixes descompone el rango cerrado [first, last] en la lista mínima
// de prefijos CIDR que lo cubren exactamente. Ambas direcciones deben ser de la
// misma familia y first <= last.
func RangeToPrefixes(first, last netip.Addr) ([]netip.Prefix, error) {
	first, last = first.Unmap(), last.Unmap()
	if !first.IsValid() || !last.IsValid() || first.Is4() != last.Is4() {
		return nil, fmt.Errorf("iptrie: rango inválido %s-%s", first, last)
	}
	if last.Less(first) {
		return nil, fmt.Errorf("iptrie: rango invertido %s-%s", first, last)
	}
	is4 := first.Is4()
	width := 128
	if is4 {
		width = 32
	}
	lo, hi := fromAddr(first), fromAddr(last)
	var out []netip.Prefix
	for {
		// Bloque más grande alineado en lo que no se pasa de hi.
		h := lo.trailingZeros()
		if h > width {
			h = width
		}
		for h > 0 && hi.less(lo.or(hostMask(width, width-h))) {
			h--
		}
		out = append(out, netip.PrefixFrom(lo.addr(is4), width-h))
		end := lo.or(hostMask(width, width-h))
		if end == hi {
			return out, nil
		}
		lo = end.addOne()
	}
}

// PrefixLast devuelve la última dirección de p.
func PrefixLast(p netip.Prefix) netip.Addr {
	p = p.Masked()
	is4 := p.Addr().Is4()
	width := 128
	if is4 {
		width = 32
	}
	return fromAddr(p.Addr()).or(hostMask(width, p.Bits())).addr(is4)
}

// RangeFromCount devuelve los prefijos que cubren n direcciones a partir de
// start (n > 0, solo IPv4).
func RangeFromCount(start netip.Addr, n uint64) ([]netip.Prefix, error) {
	start = start.Unmap()
	if !start.Is4() || n == 0 || n > 1<<32 {
		return nil, fmt.Errorf("iptrie: bloque inválido %s+%d", start, n)
	}
	lo := fromAddr(start)
	if lo.lo+n-1 > 0xFFFFFFFF {
		return nil, fmt.Errorf("iptrie: bloque %s+%d excede el espacio IPv4", start, n)
	}
	return RangeToPrefixes(start, u128{lo: lo.lo + n - 1}.addr(true))
}
