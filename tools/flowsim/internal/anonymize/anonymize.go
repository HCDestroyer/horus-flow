// Package anonymize reescribe capturas pcap/pcapng de NetFlow v9/IPFIX para
// poder versionarlas como fixtures sin datos de clientes (I0-12):
//
//   - IPs privadas (RFC 1918 y CGNAT) → la red destino (10.20.0.0/16 por
//     defecto) conservando el último octeto y la estructura de /24: cada /24
//     original va a un /24 distinto elegido por HMAC, nunca a uno que aparezca
//     en el original.
//   - IPs públicas del NAT (postNATSourceIPv4Address de las subidas con src
//     privada) → las IPs indicadas (192.0.2.10-12), por orden de frecuencia.
//   - Exportador (origen UDP de los datagramas) → la IP indicada.
//   - Resto de IPv4 públicas → HMAC a 192.0.2.0/24, 198.51.100.0/24 y
//     203.0.113.0/24 (no inyectivo si hay más IPs que direcciones).
//   - IPv6 → 2001:db8::/32 conservando la estructura de /64; enlace local
//     conserva fe80::/64.
//   - MAC unicast → MAC administradas localmente por HMAC.
//   - Se conservan 0.0.0.0/8, loopback, multicast, broadcast, puertos,
//     contadores, tiempos, plantillas y secuencias.
//
// El mapeo es determinista para una clave HMAC dada y consistente en todo el
// fichero (cabeceras Ethernet/IP y campos de los registros). La clave debe ser
// secreta: con ella y un diccionario de IPs públicas se podría invertir el
// mapeo. Falla en cerrado: se descartan las tramas que no son NetFlow/IPFIX
// sobre UDP y los datagramas con sets de datos sin plantilla conocida.
package anonymize

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
)

// Tipos de enlace admitidos.
const (
	linkEthernet = 1
	linkRaw      = 101
	linkSLL      = 113
	linkIPv4     = 228
	linkIPv6     = 229
)

// packet es la vista de una trama UDP sobre IP con los desplazamientos de
// cada campo identificativo.
type packet struct {
	data     []byte
	macOffs  []int
	ipOff    int
	v6       bool
	srcOff   int
	dstOff   int
	udpOff   int
	payload  []byte
	src, dst netip.Addr
}

func parsePacket(f capture.Frame) (*packet, bool) {
	p := &packet{data: f.Data}
	b := f.Data
	switch f.LinkType {
	case linkEthernet:
		if len(b) < 14 {
			return nil, false
		}
		p.macOffs = []int{0, 6}
		off := 12
		et := binary.BigEndian.Uint16(b[off:])
		off += 2
		for (et == 0x8100 || et == 0x88a8) && len(b) >= off+4 {
			et = binary.BigEndian.Uint16(b[off+2:])
			off += 4
		}
		if et != 0x0800 && et != 0x86dd {
			return nil, false
		}
		p.ipOff = off
	case linkSLL:
		if len(b) < 16 {
			return nil, false
		}
		if binary.BigEndian.Uint16(b[4:]) == 6 {
			p.macOffs = []int{6}
		}
		p.ipOff = 16
	case linkRaw, linkIPv4, linkIPv6:
	default:
		return nil, false
	}
	ip := b[p.ipOff:]
	if len(ip) < 1 {
		return nil, false
	}
	var l4 []byte
	switch ip[0] >> 4 {
	case 4:
		if len(ip) < 20 {
			return nil, false
		}
		ihl := int(ip[0]&0x0f) * 4
		total := int(binary.BigEndian.Uint16(ip[2:]))
		if ihl < 20 || total < ihl || len(ip) < total || ip[9] != 17 || binary.BigEndian.Uint16(ip[6:])&0x3fff != 0 {
			return nil, false
		}
		p.srcOff, p.dstOff = p.ipOff+12, p.ipOff+16
		p.src = netip.AddrFrom4([4]byte(ip[12:16]))
		p.dst = netip.AddrFrom4([4]byte(ip[16:20]))
		p.udpOff = p.ipOff + ihl
		l4 = ip[ihl:total]
	case 6:
		if len(ip) < 40 || ip[6] != 17 {
			return nil, false
		}
		plen := int(binary.BigEndian.Uint16(ip[4:]))
		if len(ip) < 40+plen {
			return nil, false
		}
		p.v6 = true
		p.srcOff, p.dstOff = p.ipOff+8, p.ipOff+24
		p.src = netip.AddrFrom16([16]byte(ip[8:24]))
		p.dst = netip.AddrFrom16([16]byte(ip[24:40]))
		p.udpOff = p.ipOff + 40
		l4 = ip[40 : 40+plen]
	default:
		return nil, false
	}
	if len(l4) < 8 {
		return nil, false
	}
	ulen := int(binary.BigEndian.Uint16(l4[4:]))
	if ulen < 8 || ulen > len(l4) {
		return nil, false
	}
	p.payload = b[p.udpOff+8 : p.udpOff+ulen]
	return p, true
}

// Inventory son las direcciones y MAC que aparecen en una captura (cabeceras
// y campos de los registros) y lo necesario para remapearla.
type Inventory struct {
	IPs       map[netip.Addr]bool
	MACs      map[[6]byte]bool
	Datagrams int
	Records   int
	// NATIPs cuenta, por IP pública, las subidas con src privada cuyo
	// postNATSourceIPv4Address es esa IP.
	NATIPs map[netip.Addr]int
	// Exporters cuenta datagramas por IP de origen.
	Exporters map[netip.Addr]int
	templates map[tmplKey]template
	conflicts int
}

func newInventory() *Inventory {
	return &Inventory{
		IPs: map[netip.Addr]bool{}, MACs: map[[6]byte]bool{}, NATIPs: map[netip.Addr]int{},
		Exporters: map[netip.Addr]int{}, templates: map[tmplKey]template{},
	}
}

func (inv *Inventory) addIP(a netip.Addr) {
	inv.IPs[a.Unmap()] = true
}

// Collect inventaría una captura. Las plantillas se toman de toda la captura
// (así se pueden leer los datos anteriores al primer reenvío de plantilla);
// si una plantilla se redefine con otros campos, devuelve error.
func Collect(frames []capture.Frame) (*Inventory, error) {
	inv := newInventory()
	type flowPkt struct {
		p *packet
		m *message
	}
	var pkts []flowPkt
	for _, f := range frames {
		p, ok := parsePacket(f)
		if !ok {
			continue
		}
		m, err := parseMessage(p.payload)
		if err != nil {
			continue
		}
		pkts = append(pkts, flowPkt{p, m})
		for _, s := range m.sets {
			if !isTemplateSet(m.version, s.id) && !isOptionsSet(m.version, s.id) {
				continue
			}
			ts, err := parseTemplates(m.version, s.id, s.body)
			if err != nil {
				return nil, fmt.Errorf("plantillas de %s: %w", p.src, err)
			}
			for id, t := range ts {
				k := tmplKey{p.src, m.domain, id}
				if old, ok := inv.templates[k]; ok && !old.equal(t) {
					return nil, fmt.Errorf("la plantilla %d de %s se redefine con otros campos: no se puede anonimizar de forma segura", id, p.src)
				}
				inv.templates[k] = t
			}
		}
	}
	for _, fp := range pkts {
		p, m := fp.p, fp.m
		inv.Datagrams++
		inv.Exporters[p.src]++
		inv.addIP(p.src)
		inv.addIP(p.dst)
		for _, off := range p.macOffs {
			inv.MACs[[6]byte(p.data[off:off+6])] = true
		}
		for _, s := range m.sets {
			if s.id < 256 {
				continue
			}
			t, ok := inv.templates[tmplKey{p.src, m.domain, s.id}]
			if !ok {
				continue
			}
			n, _ := walkRecords(t, s.body, func(vals [][]byte) {
				var src, post netip.Addr
				for i, f := range t.fields {
					v := vals[i]
					switch {
					case f.enterprise:
					case v4IEs[f.id] && len(v) == 4:
						a := netip.AddrFrom4([4]byte(v))
						inv.addIP(a)
						switch f.id {
						case 8:
							src = a
						case 225:
							post = a
						}
					case v6IEs[f.id] && len(v) == 16:
						inv.addIP(netip.AddrFrom16([16]byte(v)))
					case macIEs[f.id] && len(v) == 6:
						inv.MACs[[6]byte(v)] = true
					}
				}
				if src.IsValid() && post.IsValid() && post != src && privateV4(src) && !privateV4(post) && !keepV4(post) {
					inv.NATIPs[post]++
				}
			})
			inv.Records += n
		}
	}
	return inv, nil
}

// Config parametriza la anonimización.
type Config struct {
	// Key es la clave HMAC (secreta; sin ella el mapeo no es reproducible).
	Key []byte
	// ClientNet es el /16 destino de las IPs privadas (10.20.0.0/16).
	ClientNet netip.Prefix
	// Exporter es la IP que sustituye al exportador (10.255.3.17). Con
	// varios exportadores se usan las siguientes en orden.
	Exporter netip.Addr
	// NATIPs sustituyen a las IPs públicas del NAT (192.0.2.10-12).
	NATIPs []netip.Addr
	// From y Duration recortan la captura (desde el inicio + From). Duration
	// 0 = hasta el final.
	From, Duration time.Duration
	// AlignTemplate empieza el recorte en el primer datagrama que lleva
	// plantillas, para que el fichero se pueda decodificar desde el principio.
	AlignTemplate bool
	// IfName es el nombre de interfaz del pcapng de salida.
	IfName string
}

// Stats resume una anonimización.
type Stats struct {
	FramesIn       int
	FramesOut      int
	DroppedNonFlow int
	DroppedNoTmpl  int
	RecordsOut     int
	Exporters      map[string]string // original → anonimizada (solo informativo; no se publica)
	NATIPs         int
	PublicIPs      int
	PrivateIPs     int
	Private24      int
	MACs           int
	PoolSize       int
	Start, End     time.Time
}

// Anonymize reescribe las tramas y devuelve las de salida (pcapng con el
// mismo tipo de enlace).
func Anonymize(frames []capture.Frame, cfg Config) ([]capture.Frame, *Stats, error) {
	if len(cfg.Key) < 16 {
		return nil, nil, errors.New("la clave HMAC debe tener al menos 16 bytes")
	}
	if !cfg.ClientNet.IsValid() {
		cfg.ClientNet = defaultClient
	}
	if len(frames) == 0 {
		return nil, nil, errors.New("captura vacía")
	}
	link := frames[0].LinkType
	for _, f := range frames {
		if f.LinkType != link {
			return nil, nil, errors.New("la captura mezcla tipos de enlace")
		}
	}
	inv, err := Collect(frames)
	if err != nil {
		return nil, nil, err
	}
	if inv.Datagrams == 0 {
		return nil, nil, errors.New("la captura no contiene NetFlow v9 ni IPFIX sobre UDP")
	}
	fixed := map[netip.Addr]netip.Addr{}
	st := &Stats{FramesIn: len(frames), Exporters: map[string]string{}}
	exps := make([]netip.Addr, 0, len(inv.Exporters))
	for a := range inv.Exporters {
		exps = append(exps, a)
	}
	slices.SortFunc(exps, netip.Addr.Compare)
	next := cfg.Exporter
	for _, a := range exps {
		if !next.IsValid() || next.Is4() != a.Is4() {
			return nil, nil, fmt.Errorf("falta una IP de exportador de la familia de %s", a)
		}
		fixed[a] = next
		next = next.Next()
	}
	nats := make([]netip.Addr, 0, len(inv.NATIPs))
	for a := range inv.NATIPs {
		nats = append(nats, a)
	}
	sort.Slice(nats, func(i, j int) bool {
		if inv.NATIPs[nats[i]] != inv.NATIPs[nats[j]] {
			return inv.NATIPs[nats[i]] > inv.NATIPs[nats[j]]
		}
		return nats[i].Less(nats[j])
	})
	if len(nats) > len(cfg.NATIPs) {
		return nil, nil, fmt.Errorf("la captura tiene %d IPs públicas de NAT y solo hay %d de sustitución", len(nats), len(cfg.NATIPs))
	}
	for i, a := range nats {
		if _, dup := fixed[a]; dup {
			return nil, nil, fmt.Errorf("la IP %s es a la vez exportador y NAT", a)
		}
		fixed[a] = cfg.NATIPs[i]
	}
	st.NATIPs = len(nats)
	m, err := newMapper(cfg.Key, cfg.ClientNet, fixed, inv)
	if err != nil {
		return nil, nil, err
	}
	st.PoolSize = len(m.pool)
	st.Private24 = len(m.p24)
	for a := range inv.IPs {
		switch {
		case fixed[a].IsValid():
		case a.Is4() && privateV4(a):
			st.PrivateIPs++
		case a.Is4() && !keepV4(a) && !linkLocalV4.Contains(a):
			st.PublicIPs++
		}
	}
	for x := range inv.MACs {
		if !keepMAC(x) {
			st.MACs++
		}
	}

	// Ventana de recorte.
	t0 := frames[0].Time
	from := t0.Add(cfg.From)
	var to time.Time
	if cfg.Duration > 0 {
		to = from.Add(cfg.Duration)
	}
	started := !cfg.AlignTemplate
	var out []capture.Frame
	for _, f := range frames {
		if f.Time.Before(from) || (!to.IsZero() && !f.Time.Before(to)) {
			continue
		}
		data := bytes.Clone(f.Data)
		p, ok := parsePacket(capture.Frame{LinkType: f.LinkType, Data: data})
		if !ok {
			st.DroppedNonFlow++
			continue
		}
		msg, err := parseMessage(p.payload)
		if err != nil {
			st.DroppedNonFlow++
			continue
		}
		hasTmpl := false
		known := true
		for _, s := range msg.sets {
			if isTemplateSet(msg.version, s.id) {
				hasTmpl = true
			}
			if s.id >= 256 {
				if _, ok := inv.templates[tmplKey{p.src, msg.domain, s.id}]; !ok {
					known = false
				}
			}
		}
		if !started {
			if !hasTmpl {
				continue
			}
			started = true
		}
		if !known {
			st.DroppedNoTmpl++
			continue
		}
		recs, err := rewritePayload(m, inv, p, msg)
		if err != nil {
			return nil, nil, err
		}
		rewriteHeaders(m, p)
		st.RecordsOut += recs
		if st.Start.IsZero() {
			st.Start = f.Time
		}
		st.End = f.Time
		out = append(out, capture.Frame{Time: f.Time, LinkType: f.LinkType, Data: data})
	}
	st.FramesOut = len(out)
	for a, v := range fixed {
		if inv.Exporters[a] > 0 {
			st.Exporters[a.String()] = v.String()
		}
	}
	return out, st, nil
}

func rewritePayload(m *mapper, inv *Inventory, p *packet, msg *message) (int, error) {
	n := 0
	for _, s := range msg.sets {
		if s.id < 256 {
			continue // plantillas intactas
		}
		t := inv.templates[tmplKey{p.src, msg.domain, s.id}]
		c, err := walkRecords(t, s.body, func(vals [][]byte) {
			for i, f := range t.fields {
				v := vals[i]
				switch {
				case f.enterprise:
				case v4IEs[f.id]:
					if len(v) == 4 {
						a := m.V4(netip.AddrFrom4([4]byte(v))).As4()
						copy(v, a[:])
					} else {
						clear(v) // longitud inesperada: se borra
					}
				case v6IEs[f.id]:
					if len(v) == 16 {
						a := m.V6(netip.AddrFrom16([16]byte(v))).As16()
						copy(v, a[:])
					} else {
						clear(v)
					}
				case macIEs[f.id]:
					if len(v) == 6 {
						x := m.MAC([6]byte(v))
						copy(v, x[:])
					} else {
						clear(v)
					}
				}
			}
		})
		if err != nil {
			return 0, fmt.Errorf("set %d de %s: %w", s.id, p.src, err)
		}
		n += c
	}
	return n, nil
}

func rewriteHeaders(m *mapper, p *packet) {
	b := p.data
	for _, off := range p.macOffs {
		x := m.MAC([6]byte(b[off : off+6]))
		copy(b[off:], x[:])
	}
	if p.v6 {
		s, d := m.V6(p.src).As16(), m.V6(p.dst).As16()
		copy(b[p.srcOff:], s[:])
		copy(b[p.dstOff:], d[:])
	} else {
		s, d := m.V4(p.src).As4(), m.V4(p.dst).As4()
		copy(b[p.srcOff:], s[:])
		copy(b[p.dstOff:], d[:])
		ihl := int(b[p.ipOff]&0x0f) * 4
		b[p.ipOff+10], b[p.ipOff+11] = 0, 0
		binary.BigEndian.PutUint16(b[p.ipOff+10:], checksum(0, b[p.ipOff:p.ipOff+ihl]))
	}
	// Checksum UDP con la pseudo-cabecera nueva (en IPv4 se respeta el 0 =
	// sin checksum).
	udp := b[p.udpOff : p.udpOff+8+len(p.payload)]
	if !p.v6 && binary.BigEndian.Uint16(udp[6:]) == 0 {
		return
	}
	udp[6], udp[7] = 0, 0
	var sum uint32
	alen := 4
	if p.v6 {
		alen = 16
	}
	sum = add(sum, b[p.srcOff:p.srcOff+alen])
	sum = add(sum, b[p.dstOff:p.dstOff+alen])
	sum += 17 + uint32(len(udp)) //nolint:gosec // datagramas < 64 KiB
	cs := checksum(sum, udp)
	if cs == 0 {
		cs = 0xffff
	}
	binary.BigEndian.PutUint16(udp[6:], cs)
}

func add(sum uint32, b []byte) uint32 {
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	return sum
}

func checksum(sum uint32, b []byte) uint16 {
	sum = add(sum, b)
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum) //nolint:gosec // plegado a 16 bits
}
