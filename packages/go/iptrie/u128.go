package iptrie

import (
	"encoding/binary"
	"math/bits"
	"net/netip"
)

// u128 es una dirección IP como entero sin signo de 128 bits. Las IPv4 usan
// solo los 32 bits bajos; cada familia vive en su propia tabla, así que no se
// mezclan.
type u128 struct{ hi, lo uint64 }

func fromAddr(a netip.Addr) u128 {
	if a.Is4() {
		b := a.As4()
		return u128{lo: uint64(binary.BigEndian.Uint32(b[:]))}
	}
	b := a.As16()
	return u128{hi: binary.BigEndian.Uint64(b[:8]), lo: binary.BigEndian.Uint64(b[8:])}
}

func (u u128) addr(is4 bool) netip.Addr {
	if is4 {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(u.lo))
		return netip.AddrFrom4(b)
	}
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], u.hi)
	binary.BigEndian.PutUint64(b[8:], u.lo)
	return netip.AddrFrom16(b)
}

func (u u128) less(v u128) bool {
	return u.hi < v.hi || (u.hi == v.hi && u.lo < v.lo)
}

func (u u128) addOne() u128 {
	lo, carry := bits.Add64(u.lo, 1, 0)
	return u128{hi: u.hi + carry, lo: lo}
}

func (u u128) or(v u128) u128 { return u128{hi: u.hi | v.hi, lo: u.lo | v.lo} }

// hostMask devuelve la máscara de host (bits a 1 fuera del prefijo) para un
// prefijo de longitud plen en una familia de width bits (32 o 128).
func hostMask(width, plen int) u128 {
	h := width - plen // bits de host
	switch {
	case h <= 0:
		return u128{}
	case h >= 128:
		return u128{hi: ^uint64(0), lo: ^uint64(0)}
	case h >= 64:
		return u128{hi: (uint64(1) << (h - 64)) - 1, lo: ^uint64(0)}
	default:
		return u128{lo: (uint64(1) << h) - 1}
	}
}

// trailingZeros cuenta los ceros finales de u (128 si u == 0).
func (u u128) trailingZeros() int {
	if u.lo != 0 {
		return bits.TrailingZeros64(u.lo)
	}
	if u.hi != 0 {
		return 64 + bits.TrailingZeros64(u.hi)
	}
	return 128
}
