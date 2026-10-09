package capture

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"
)

// Tipos de enlace pcap admitidos.
const (
	linkEthernet = 1
	linkRawBSD   = 12
	linkRawBSD2  = 14
	linkRaw      = 101
	linkSLL      = 113
	linkIPv4     = 228
	linkIPv6     = 229
)

type pcapWriter struct {
	w *bufio.Writer
}

func (p *pcapWriter) header() error {
	h := make([]byte, 0, 24)
	h = binary.LittleEndian.AppendUint32(h, 0xa1b23c4d) // nanosegundos
	h = binary.LittleEndian.AppendUint16(h, 2)
	h = binary.LittleEndian.AppendUint16(h, 4)
	h = binary.LittleEndian.AppendUint32(h, 0) // thiszone
	h = binary.LittleEndian.AppendUint32(h, 0) // sigfigs
	h = binary.LittleEndian.AppendUint32(h, 65535+48)
	h = binary.LittleEndian.AppendUint32(h, linkRaw)
	if _, err := p.w.Write(h); err != nil {
		return fmt.Errorf("pcap: cabecera: %w", err)
	}
	return nil
}

func (p *pcapWriter) Write(d Datagram) error {
	pkt, err := buildIPUDP(d)
	if err != nil {
		return err
	}
	ts := d.Time.UnixNano()
	rh := make([]byte, 0, 16)
	rh = binary.LittleEndian.AppendUint32(rh, uint32(ts/1e9))
	rh = binary.LittleEndian.AppendUint32(rh, uint32(ts%1e9))
	rh = binary.LittleEndian.AppendUint32(rh, uint32(len(pkt)))
	rh = binary.LittleEndian.AppendUint32(rh, uint32(len(pkt)))
	if _, err := p.w.Write(rh); err != nil {
		return fmt.Errorf("pcap: %w", err)
	}
	if _, err := p.w.Write(pkt); err != nil {
		return fmt.Errorf("pcap: %w", err)
	}
	return nil
}

func (p *pcapWriter) Flush() error {
	if err := p.w.Flush(); err != nil {
		return fmt.Errorf("pcap: %w", err)
	}
	return nil
}

func checksumAdd(sum uint32, b []byte) uint32 {
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	return sum
}

func checksumFold(sum uint32) uint16 {
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// buildIPUDP construye un paquete IPv4/IPv6 + UDP con checksums válidos.
func buildIPUDP(d Datagram) ([]byte, error) {
	src, dst := d.Src.Addr().Unmap(), d.Dst.Addr().Unmap()
	if src.Is4() != dst.Is4() {
		return nil, errors.New("pcap: origen y destino de familias distintas")
	}
	udpLen := 8 + len(d.Payload)
	if udpLen > 65535 {
		return nil, fmt.Errorf("pcap: datagrama de %d bytes demasiado grande", len(d.Payload))
	}
	udp := make([]byte, 8, udpLen)
	binary.BigEndian.PutUint16(udp[0:], d.Src.Port())
	binary.BigEndian.PutUint16(udp[2:], d.Dst.Port())
	binary.BigEndian.PutUint16(udp[4:], uint16(udpLen))
	udp = append(udp, d.Payload...)

	var sum uint32
	if src.Is4() {
		s, t := src.As4(), dst.As4()
		sum = checksumAdd(sum, s[:])
		sum = checksumAdd(sum, t[:])
		sum += 17 + uint32(udpLen)
	} else {
		s, t := src.As16(), dst.As16()
		sum = checksumAdd(sum, s[:])
		sum = checksumAdd(sum, t[:])
		sum += uint32(udpLen) + 17
	}
	cs := checksumFold(checksumAdd(sum, udp))
	if cs == 0 {
		cs = 0xffff
	}
	binary.BigEndian.PutUint16(udp[6:], cs)

	if src.Is4() {
		ip := make([]byte, 20, 20+udpLen)
		ip[0] = 0x45
		binary.BigEndian.PutUint16(ip[2:], uint16(20+udpLen))
		binary.BigEndian.PutUint16(ip[6:], 0x4000) // DF
		ip[8] = 64
		ip[9] = 17
		s, t := src.As4(), dst.As4()
		copy(ip[12:16], s[:])
		copy(ip[16:20], t[:])
		binary.BigEndian.PutUint16(ip[10:], checksumFold(checksumAdd(0, ip)))
		return append(ip, udp...), nil
	}
	ip := make([]byte, 40, 40+udpLen)
	ip[0] = 0x60
	binary.BigEndian.PutUint16(ip[4:], uint16(udpLen))
	ip[6] = 17
	ip[7] = 64
	s, t := src.As16(), dst.As16()
	copy(ip[8:24], s[:])
	copy(ip[24:40], t[:])
	return append(ip, udp...), nil
}

func readPcap(r *bufio.Reader, fn func(Frame) error) error {
	var gh [24]byte
	if _, err := io.ReadFull(r, gh[:]); err != nil {
		return fmt.Errorf("pcap: cabecera: %w", err)
	}
	var bo binary.ByteOrder
	nano := false
	switch binary.LittleEndian.Uint32(gh[:4]) {
	case 0xa1b2c3d4:
		bo = binary.LittleEndian
	case 0xa1b23c4d:
		bo, nano = binary.LittleEndian, true
	case 0xd4c3b2a1:
		bo = binary.BigEndian
	case 0x4d3cb2a1:
		bo, nano = binary.BigEndian, true
	default:
		return errors.New("capture: formato desconocido (ni hfsim, ni pcap, ni pcapng)")
	}
	link := bo.Uint32(gh[20:24]) & 0x0fffffff
	for {
		var rh [16]byte
		if _, err := io.ReadFull(r, rh[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("pcap: registro: %w", err)
		}
		sec, frac := bo.Uint32(rh[0:]), bo.Uint32(rh[4:])
		incl := bo.Uint32(rh[8:])
		if incl > 1<<18 {
			return fmt.Errorf("pcap: registro de %d bytes inválido", incl)
		}
		data := make([]byte, incl)
		if _, err := io.ReadFull(r, data); err != nil {
			return fmt.Errorf("pcap: datos: %w", err)
		}
		ns := int64(frac)
		if !nano {
			ns *= 1000
		}
		if err := fn(Frame{Time: time.Unix(int64(sec), ns).UTC(), LinkType: link, Data: data}); err != nil {
			return err
		}
	}
}

// ParseFrame extrae el datagrama UDP de una trama (Ethernet con 802.1Q, SLL o
// IP en bruto). Devuelve false si no es UDP sobre IP sin fragmentar.
func ParseFrame(f Frame) (Datagram, bool) {
	d, ok := parseFrame(f.LinkType, f.Data)
	d.Time = f.Time
	return d, ok
}

func parseFrame(link uint32, b []byte) (Datagram, bool) {
	switch link {
	case linkEthernet:
		if len(b) < 14 {
			return Datagram{}, false
		}
		et := binary.BigEndian.Uint16(b[12:])
		b = b[14:]
		for (et == 0x8100 || et == 0x88a8) && len(b) >= 4 {
			et = binary.BigEndian.Uint16(b[2:])
			b = b[4:]
		}
		if et != 0x0800 && et != 0x86dd {
			return Datagram{}, false
		}
	case linkSLL:
		if len(b) < 16 {
			return Datagram{}, false
		}
		b = b[16:]
	case linkRaw, linkRawBSD, linkRawBSD2, linkIPv4, linkIPv6:
	default:
		return Datagram{}, false
	}
	return parseIPUDP(b)
}

func parseIPUDP(b []byte) (Datagram, bool) {
	if len(b) < 1 {
		return Datagram{}, false
	}
	var src, dst netip.Addr
	var l4 []byte
	switch b[0] >> 4 {
	case 4:
		if len(b) < 20 {
			return Datagram{}, false
		}
		ihl := int(b[0]&0x0f) * 4
		total := int(binary.BigEndian.Uint16(b[2:]))
		if ihl < 20 || total < ihl || len(b) < total || b[9] != 17 {
			return Datagram{}, false
		}
		if binary.BigEndian.Uint16(b[6:])&0x3fff != 0 { // fragmentado
			return Datagram{}, false
		}
		src = netip.AddrFrom4([4]byte(b[12:16]))
		dst = netip.AddrFrom4([4]byte(b[16:20]))
		l4 = b[ihl:total]
	case 6:
		if len(b) < 40 || b[6] != 17 {
			return Datagram{}, false
		}
		plen := int(binary.BigEndian.Uint16(b[4:]))
		if len(b) < 40+plen {
			return Datagram{}, false
		}
		src = netip.AddrFrom16([16]byte(b[8:24]))
		dst = netip.AddrFrom16([16]byte(b[24:40]))
		l4 = b[40 : 40+plen]
	default:
		return Datagram{}, false
	}
	if len(l4) < 8 {
		return Datagram{}, false
	}
	ulen := int(binary.BigEndian.Uint16(l4[4:]))
	if ulen < 8 || ulen > len(l4) {
		return Datagram{}, false
	}
	payload := append([]byte(nil), l4[8:ulen]...)
	return Datagram{
		Src:     netip.AddrPortFrom(src, binary.BigEndian.Uint16(l4[0:])),
		Dst:     netip.AddrPortFrom(dst, binary.BigEndian.Uint16(l4[2:])),
		Payload: payload,
	}, true
}
