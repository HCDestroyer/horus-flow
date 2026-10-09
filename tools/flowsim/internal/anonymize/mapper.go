package anonymize

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"
)

// Rangos de destino (RFC 5737 y RFC 3849).
var (
	docV4 = []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
	}
	docV6         = netip.MustParsePrefix("2001:db8::/32")
	linkLocalV4   = netip.MustParsePrefix("169.254.0.0/16")
	cgnat         = netip.MustParsePrefix("100.64.0.0/10")
	thisNetV4     = netip.MustParsePrefix("0.0.0.0/8")
	reservedV4    = netip.MustParsePrefix("240.0.0.0/4")
	v4MappedV6    = netip.MustParsePrefix("::ffff:0:0/96")
	linkLocalV6   = netip.MustParsePrefix("fe80::/10")
	broadcastMAC  = [6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	zeroMAC       = [6]byte{}
	defaultClient = netip.MustParsePrefix("10.20.0.0/16")
)

// keepV4 son las IPv4 que no identifican a nadie y se conservan: 0.0.0.0/8,
// loopback, multicast, 240/4 y el broadcast.
func keepV4(a netip.Addr) bool {
	return thisNetV4.Contains(a) || a.IsLoopback() || a.IsMulticast() || reservedV4.Contains(a)
}

// privateV4 son las IPv4 de clientes o de la red interna del ISP.
func privateV4(a netip.Addr) bool { return a.IsPrivate() || cgnat.Contains(a) }

// keepMAC son las MAC no identificativas: cero, broadcast y multicast.
func keepMAC(m [6]byte) bool { return m == zeroMAC || m == broadcastMAC || m[0]&1 != 0 }

// mapper hace los remapeos deterministas (HMAC-SHA256 con la clave) y
// consistentes: la misma entrada siempre da la misma salida en todo el
// fichero (cabeceras y registros).
type mapper struct {
	key       []byte
	clientNet netip.Prefix // /16 destino de las privadas (/24 conservadas)
	fixed     map[netip.Addr]netip.Addr
	p24       map[[3]byte]byte // /24 privada original → tercer octeto destino
	ll        map[netip.Addr]netip.Addr
	pool      []netip.Addr // direcciones de documentación disponibles
	mac       map[[6]byte][6]byte
}

func (m *mapper) sum(parts ...[]byte) []byte {
	h := hmac.New(sha256.New, m.key)
	for _, p := range parts {
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(p))) //nolint:gosec // trozos cortos
		_, _ = h.Write(l[:])
		_, _ = h.Write(p)
	}
	return h.Sum(nil)
}

// newMapper prepara las tablas a partir del inventario completo de la
// captura original (para que el resultado no dependa del orden de llegada y
// ninguna dirección destino coincida con una original).
func newMapper(key []byte, clientNet netip.Prefix, fixed map[netip.Addr]netip.Addr, inv *Inventory) (*mapper, error) {
	m := &mapper{key: key, clientNet: clientNet.Masked(), fixed: fixed, p24: map[[3]byte]byte{},
		ll: map[netip.Addr]netip.Addr{}, mac: map[[6]byte][6]byte{}}
	if !m.clientNet.Addr().Is4() || m.clientNet.Bits() != 16 {
		return nil, fmt.Errorf("la red de clientes destino debe ser un /16 IPv4 (es %s)", clientNet)
	}
	reserved := map[netip.Addr]bool{}
	for _, v := range fixed {
		reserved[v] = true
	}
	// /24 privadas → 10.20.k.0/24. Se evitan los k cuyo /24 aparece en el
	// original (no se puede generar ninguna dirección original).
	used := map[byte]bool{}
	var nets [][3]byte
	for a := range inv.IPs {
		if a.Is4() && privateV4(a) && fixed[a] == (netip.Addr{}) {
			b := a.As4()
			k := [3]byte{b[0], b[1], b[2]}
			if !slices.Contains(nets, k) {
				nets = append(nets, k)
			}
		}
		if a.Is4() && m.clientNet.Contains(a) {
			used[a.As4()[2]] = true
		}
	}
	for v := range reserved {
		if v.Is4() && m.clientNet.Contains(v) {
			used[v.As4()[2]] = true
		}
	}
	slices.SortFunc(nets, func(a, b [3]byte) int { return slices.Compare(a[:], b[:]) })
	if len(nets) > 256-len(used) {
		return nil, fmt.Errorf("%d redes /24 privadas no caben en %s", len(nets), m.clientNet)
	}
	for _, n := range nets {
		k := m.sum([]byte("p24"), n[:])[0]
		for used[k] {
			k++
		}
		used[k] = true
		m.p24[n] = k
	}
	// Enlace local IPv4: 169.254.x.y por HMAC, sin repetir ni coincidir con
	// originales.
	var lls []netip.Addr
	for a := range inv.IPs {
		if a.Is4() && linkLocalV4.Contains(a) {
			lls = append(lls, a)
		}
	}
	slices.SortFunc(lls, netip.Addr.Compare)
	taken := map[netip.Addr]bool{}
	for _, a := range lls {
		taken[a] = true
	}
	for _, a := range lls {
		h := binary.BigEndian.Uint16(m.sum([]byte("ll4"), a.AsSlice()))
		for {
			c := netip.AddrFrom4([4]byte{169, 254, byte(h >> 8), byte(h)})
			if !taken[c] && byte(h) != 0 && byte(h) != 255 {
				taken[c] = true
				m.ll[a] = c
				break
			}
			h++
		}
	}
	// Pool de documentación: sin .0/.255 ni las IPs fijas (NAT).
	for _, p := range docV4 {
		for a := p.Addr(); p.Contains(a); a = a.Next() {
			if b := a.As4(); b[3] != 0 && b[3] != 255 && !reserved[a] && !inv.IPs[a] {
				m.pool = append(m.pool, a)
			}
		}
	}
	return m, nil
}

// V4 remapea una IPv4.
func (m *mapper) V4(a netip.Addr) netip.Addr {
	if v, ok := m.fixed[a]; ok {
		return v
	}
	switch {
	case keepV4(a):
		return a
	case linkLocalV4.Contains(a):
		if v, ok := m.ll[a]; ok {
			return v
		}
		return netip.AddrFrom4([4]byte{169, 254, 255, 254})
	case privateV4(a):
		b := a.As4()
		k, ok := m.p24[[3]byte{b[0], b[1], b[2]}]
		if !ok { // no inventariada: nunca debería pasar
			k = m.sum([]byte("p24"), b[:3])[0]
		}
		c := m.clientNet.Addr().As4()
		return netip.AddrFrom4([4]byte{c[0], c[1], k, b[3]})
	}
	// Pública: HMAC a los rangos de documentación. No es inyectiva si hay más
	// IPs públicas que direcciones del pool (las colisiones se documentan).
	h := binary.BigEndian.Uint64(m.sum([]byte("pub4"), a.AsSlice()))
	return m.pool[h%uint64(len(m.pool))]
}

// V6 remapea una IPv6: global/ULA → 2001:db8::/32 conservando la estructura
// de /64 (prefijo por HMAC del /64 e IID por HMAC de la dirección); enlace
// local conserva fe80::/64 con el IID remapeado; se conservan ::, ::1 y
// multicast; IPv4-mapped remapea la IPv4.
func (m *mapper) V6(a netip.Addr) netip.Addr {
	if v, ok := m.fixed[a]; ok {
		return v
	}
	switch {
	case a.IsUnspecified() || a.IsLoopback() || a.IsMulticast():
		return a
	case v4MappedV6.Contains(a):
		v := m.V4(a.Unmap()).As4()
		return netip.AddrFrom16([16]byte{10: 0xff, 11: 0xff, 12: v[0], 13: v[1], 14: v[2], 15: v[3]})
	}
	b := a.As16()
	iid := m.sum([]byte("iid6"), b[:])
	var out [16]byte
	if linkLocalV6.Contains(a) {
		out[0], out[1] = 0xfe, 0x80
	} else {
		d := docV6.Addr().As16()
		copy(out[:4], d[:4])
		copy(out[4:8], m.sum([]byte("net64"), b[:8])[:4])
	}
	copy(out[8:], iid[:8])
	return netip.AddrFrom16(out)
}

// Addr remapea una dirección de cualquier familia.
func (m *mapper) Addr(a netip.Addr) netip.Addr {
	if a.Is4() {
		return m.V4(a)
	}
	return m.V6(a)
}

// MAC remapea una MAC unicast a una administrada localmente.
func (m *mapper) MAC(x [6]byte) [6]byte {
	if keepMAC(x) {
		return x
	}
	if v, ok := m.mac[x]; ok {
		return v
	}
	h := m.sum([]byte("mac"), x[:])
	var v [6]byte
	copy(v[:], h[:6])
	v[0] = v[0]&^0x01 | 0x02 // unicast, administrada localmente
	m.mac[x] = v
	return v
}
