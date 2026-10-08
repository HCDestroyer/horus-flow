package sim

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net/netip"
)

// service es un destino conocido con prefijos reales y bien publicados, para
// que el enriquecimiento por ASN/servicio (I1-07) tenga con qué trabajar.
type service struct {
	v4, v6 []netip.Prefix
}

var services = map[string]service{
	"google":     {v4: ps("142.250.0.0/15", "172.217.0.0/16"), v6: ps("2a00:1450:4000::/37")},
	"meta":       {v4: ps("157.240.0.0/16", "31.13.64.0/18"), v6: ps("2a03:2880::/32")},
	"netflix":    {v4: ps("45.57.0.0/17", "198.38.96.0/19"), v6: ps("2a00:86c0::/32")},
	"cloudflare": {v4: ps("104.16.0.0/13", "172.64.0.0/13"), v6: ps("2606:4700::/32")},
	"akamai":     {v4: ps("23.32.0.0/11", "2.16.0.0/13"), v6: ps("2a02:26f0::/29")},
	"amazon":     {v4: ps("52.84.0.0/15", "54.230.0.0/16"), v6: ps("2600:9000::/28")},
	"microsoft":  {v4: ps("52.96.0.0/14", "40.96.0.0/13"), v6: ps("2603:1000::/25")},
	"apple":      {v4: ps("17.0.0.0/8"), v6: ps("2620:149::/32")},
	"s3":         {v4: ps("52.216.0.0/15")},
	"zoom":       {v4: ps("170.114.0.0/16")},
	"ntp":        {v4: ps("162.159.200.0/24"), v6: ps("2606:4700:f1::/48")},
	"publicdns":  {v4: ps("1.1.1.0/24", "8.8.8.0/24"), v6: ps("2606:4700:4700::/48")},
}

func ps(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, x := range s {
		out[i] = netip.MustParsePrefix(x)
	}
	return out
}

// reserved son rangos que nunca se usan como IP remota "de Internet".
var reserved = ps(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/3",
)

// randomPublicV4 devuelve una IPv4 pública aleatoria fuera de rangos reservados.
func randomPublicV4(r *rand.Rand) netip.Addr {
	for {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], r.Uint32())
		a := netip.AddrFrom4(b)
		if b[3] == 0 || b[3] == 255 || containsAny(reserved, a) {
			continue
		}
		return a
	}
}

// randomIn devuelve una dirección aleatoria dentro del prefijo (nunca la
// dirección de red).
func randomIn(r *rand.Rand, p netip.Prefix) netip.Addr {
	p = p.Masked()
	base := p.Addr()
	if base.Is4() {
		b := base.As4()
		host := 32 - p.Bits()
		if host < 2 {
			return base
		}
		v := binary.BigEndian.Uint32(b[:]) | (r.Uint32N(uint32(1)<<host-2) + 1)
		binary.BigEndian.PutUint32(b[:], v)
		return netip.AddrFrom4(b)
	}
	b := base.As16()
	var rb [16]byte
	binary.BigEndian.PutUint64(rb[:8], r.Uint64())
	binary.BigEndian.PutUint64(rb[8:], r.Uint64())
	for i := range b {
		nb := p.Bits() - i*8
		var m byte
		switch {
		case nb >= 8:
			m = 0xff
		case nb > 0:
			m = byte(0xff << (8 - nb))
		}
		b[i] = b[i]&m | rb[i]&^m
	}
	b[15] |= 1
	return netip.AddrFrom16(b)
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

// v4Allocator reparte IPv4 de unos prefijos de forma secuencial, saltando la
// red, la .1 (pasarela), el broadcast y las subredes de otros roles.
type v4Allocator struct {
	prefixes []netip.Prefix
	skip     []netip.Prefix
	pi       int
	cur      netip.Addr
}

func (a *v4Allocator) next() (netip.Addr, error) {
	for a.pi < len(a.prefixes) {
		p := a.prefixes[a.pi].Masked()
		if !a.cur.IsValid() {
			a.cur = p.Addr().Next() // .1 es la pasarela del router
		}
		for {
			a.cur = a.cur.Next()
			c := a.cur
			if !p.Contains(c) || !p.Contains(c.Next()) {
				break // fin del prefijo (la última es broadcast)
			}
			b := c.As4()
			if b[3] == 0 || b[3] == 255 || containsAny(a.skip, c) {
				continue
			}
			return c, nil
		}
		a.pi++
		a.cur = netip.Addr{}
	}
	return netip.Addr{}, fmt.Errorf("no quedan IPv4 libres en %v", a.prefixes)
}

// v6Allocator reparte prefijos delegados de longitud clientLen.
type v6Allocator struct {
	prefixes  []netip.Prefix
	clientLen int
	pi        int
	idx       uint64
}

func (a *v6Allocator) next() (netip.Prefix, error) {
	for a.pi < len(a.prefixes) {
		p := a.prefixes[a.pi].Masked()
		b := p.Addr().As16()
		hi := binary.BigEndian.Uint64(b[:8])
		hi += a.idx << (64 - a.clientLen)
		binary.BigEndian.PutUint64(b[:8], hi)
		cand := netip.PrefixFrom(netip.AddrFrom16(b), a.clientLen)
		a.idx++
		if p.Contains(cand.Addr()) {
			return cand, nil
		}
		a.pi++
		a.idx = 0
	}
	return netip.Prefix{}, fmt.Errorf("no quedan prefijos IPv6 libres en %v", a.prefixes)
}

// clientKey devuelve la clave canónica del cliente (ADR-0018): IPv4 /32 o
// IPv6 truncada a clientLen.
func clientKey(a netip.Addr, clientLen int) string {
	if a.Is4() {
		return a.String()
	}
	p, _ := a.Prefix(clientLen)
	return p.String()
}

// MAC OUI típicos: CPE residenciales y equipos empresariales.
var (
	residentialOUI = [][3]byte{{0x50, 0xc7, 0xbf}, {0x00, 0xe0, 0xfc}, {0x34, 0x4b, 0x50}, {0xfc, 0xec, 0xda}}
	commercialOUI  = [][3]byte{{0x00, 0x1b, 0x54}, {0x00, 0x0c, 0x29}, {0xe4, 0x8d, 0x8c}}
	mikrotikOUI    = [3]byte{0x4c, 0x5e, 0x0c}
)

func randomMAC(r *rand.Rand, ouis [][3]byte) [6]byte {
	o := pick(r, ouis)
	return [6]byte{o[0], o[1], o[2], byte(r.Uint32()), byte(r.Uint32()), byte(r.Uint32())}
}
