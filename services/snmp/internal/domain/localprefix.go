package domain

import (
	"fmt"
	"net/netip"
	"slices"
)

// Granularidad de flows.site_remote_net_1d: la red remota se guarda truncada
// a /24 (IPv4) o /48 (IPv6); un prefijo local más específico se cuenta por su
// /24 o /48 completo.
const (
	LocalNetBitsV4 = 24
	LocalNetBitsV6 = 48
	// minLocalBitsV4/V6: un prefijo más amplio clasificaría como local medio
	// Internet (error de tecleo, no una LAN de IXP ni una caché).
	minLocalBitsV4 = 8
	minLocalBitsV6 = 16
)

// Avisos de la lista local por prefijos (no impiden guardar).
const (
	// WarnOverlapsClientPrefix: el prefijo se solapa con un prefijo de clientes
	// del ISP (el tráfico entre clientes no es remoto: revise si es lo que quiere).
	WarnOverlapsClientPrefix = "overlaps_client_prefix"
	// WarnCountedAsAggregate: más específico que /24 o /48: se cuenta su red
	// /24 o /48 completa.
	WarnCountedAsAggregate = "counted_as_aggregate"
)

// LocalPrefixWarning es un aviso sobre un prefijo de la lista local.
type LocalPrefixWarning struct {
	Prefix       string  `json:"prefix"`
	Code         string  `json:"code"`
	ClientPrefix *string `json:"client_prefix"`
	Detail       string  `json:"detail"`
}

// normalizeLocalPrefixes enmascara, ordena y deduplica la lista y rechaza
// prefijos demasiado amplios.
func (s *CapacitySettings) normalizeLocalPrefixes() []FieldError {
	var errs []FieldError
	if s.LocalPrefixes == nil {
		s.LocalPrefixes = []netip.Prefix{}
	}
	if len(s.LocalPrefixes) > MaxLocalPrefixes {
		errs = append(errs, FieldError{Field: "local_prefixes", Code: "TOO_MANY", Msg: fmt.Sprintf("Hasta %d prefijos locales.", MaxLocalPrefixes)})
	}
	out := make([]netip.Prefix, 0, len(s.LocalPrefixes))
	for i, p := range s.LocalPrefixes {
		field := fmt.Sprintf("local_prefixes[%d]", i)
		if !p.IsValid() {
			errs = append(errs, FieldError{Field: field, Code: "INVALID_FORMAT", Msg: "Prefijo CIDR IPv4 o IPv6 (p. ej. 190.0.2.0/24)."})
			continue
		}
		if p.Addr().Is4In6() {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		p = p.Masked()
		if (p.Addr().Is4() && p.Bits() < minLocalBitsV4) || (p.Addr().Is6() && p.Bits() < minLocalBitsV6) {
			errs = append(errs, FieldError{Field: field, Code: "INVALID_VALUE",
				Msg: fmt.Sprintf("Demasiado amplio: como mínimo /%d en IPv4 y /%d en IPv6.", minLocalBitsV4, minLocalBitsV6)})
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}
		return a.Bits() - b.Bits()
	})
	s.LocalPrefixes = slices.Compact(out)
	return errs
}

// LocalPrefixRanges devuelve, por prefijo local, las direcciones IPv6
// (IPv4 mapeada) inicial y final de su red truncada a la granularidad de
// flows.site_remote_net_1d: la consulta compara remote_net con ellas.
func LocalPrefixRanges(ps []netip.Prefix) (lo, hi []string) {
	lo, hi = make([]string, 0, len(ps)), make([]string, 0, len(ps))
	for _, p := range ps {
		a := p.Addr()
		bits := p.Bits()
		if a.Is4() {
			bits = min(bits, LocalNetBitsV4) + 96
		} else {
			bits = min(bits, LocalNetBitsV6)
		}
		b := netip.AddrFrom16(a.As16()).As16()
		for i := bits; i < 128; i++ {
			b[i/8] &^= 1 << (7 - uint(i%8))
		}
		h := b
		for i := bits; i < 128; i++ {
			h[i/8] |= 1 << (7 - uint(i%8))
		}
		lo = append(lo, netip.AddrFrom16(b).String())
		hi = append(hi, netip.AddrFrom16(h).String())
	}
	return lo, hi
}

// LocalPrefixWarnings avisa de los prefijos locales que se solapan con
// prefijos de clientes del ISP y de los que se cuentan por su /24 o /48.
func LocalPrefixWarnings(local, clients []netip.Prefix) []LocalPrefixWarning {
	out := []LocalPrefixWarning{}
	for _, p := range local {
		if (p.Addr().Is4() && p.Bits() > LocalNetBitsV4) || (p.Addr().Is6() && p.Bits() > LocalNetBitsV6) {
			agg := LocalNetBitsV4
			if p.Addr().Is6() {
				agg = LocalNetBitsV6
			}
			whole := netip.PrefixFrom(p.Addr(), agg).Masked().String()
			out = append(out, LocalPrefixWarning{Prefix: p.String(), Code: WarnCountedAsAggregate,
				Detail: fmt.Sprintf("Más específico que /%d: se cuenta todo %s.", agg, whole)})
		}
		for _, c := range clients {
			c = c.Masked()
			if c.Addr().Is4In6() {
				c = netip.PrefixFrom(c.Addr().Unmap(), c.Bits()-96)
			}
			if c.Addr().Is4() != p.Addr().Is4() || !c.Overlaps(p) {
				continue
			}
			cs := c.String()
			out = append(out, LocalPrefixWarning{Prefix: p.String(), Code: WarnOverlapsClientPrefix, ClientPrefix: &cs,
				Detail: fmt.Sprintf("Se solapa con el prefijo de clientes %s.", cs)})
			break
		}
	}
	return out
}
