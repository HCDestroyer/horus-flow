// Package pcapread lee capturas pcap y pcapng (opcionalmente .gz) y extrae
// los datagramas UDP (Ethernet/VLAN, IPv4/IPv6, raw IP o Linux SLL). Sirve
// para reproducir fixtures de flujos contra el collector (tests y replay).
package pcapread

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"
)

// Datagram es un datagrama UDP de la captura.
type Datagram struct {
	Time    time.Time
	Src     netip.AddrPort
	Dst     netip.AddrPort
	Payload []byte
}

// ReadFile lee todos los datagramas UDP de path.
func ReadFile(path string) ([]Datagram, error) {
	f, err := os.Open(path) //nolint:gosec // ruta de fixture
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var r io.Reader = bufio.NewReader(f)
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, err
		}
		r = gz
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

const hfsimMagic = "HORUSFLOWSIM"

// parseHFSim lee el formato del simulador (tools/flowsim/internal/capture):
// cabecera "HORUSFLOWSIM" | versión u16 | flags u16; registros tiempo u64 ns |
// familia u8 | IP origen | puerto u16 | IP destino | puerto u16 | longitud u32 | carga.
func parseHFSim(b []byte) ([]Datagram, error) {
	b = b[len(hfsimMagic):]
	if len(b) < 4 {
		return nil, errors.New("pcapread: short hfsim header")
	}
	b = b[4:]
	var out []Datagram
	for len(b) > 0 {
		if len(b) < 9 {
			return out, errors.New("pcapread: truncated hfsim record")
		}
		ns, fam := binary.BigEndian.Uint64(b), b[8]
		b = b[9:]
		alen := 4
		if fam == 6 {
			alen = 16
		} else if fam != 4 {
			return out, fmt.Errorf("pcapread: hfsim family %d", fam)
		}
		if len(b) < 2*(alen+2)+4 {
			return out, errors.New("pcapread: truncated hfsim record")
		}
		src, _ := netip.AddrFromSlice(b[:alen])
		sp := binary.BigEndian.Uint16(b[alen:])
		b = b[alen+2:]
		dst, _ := netip.AddrFromSlice(b[:alen])
		dp := binary.BigEndian.Uint16(b[alen:])
		b = b[alen+2:]
		n := int(binary.BigEndian.Uint32(b))
		b = b[4:]
		if n > len(b) {
			return out, errors.New("pcapread: truncated hfsim payload")
		}
		out = append(out, Datagram{Time: time.Unix(0, int64(ns)).UTC(), //nolint:gosec // ns Unix
			Src: netip.AddrPortFrom(src, sp), Dst: netip.AddrPortFrom(dst, dp), Payload: bytes.Clone(b[:n])})
		b = b[n:]
	}
	return out, nil
}

// Parse decodifica una captura completa en memoria (pcap, pcapng o hfsim).
func Parse(b []byte) ([]Datagram, error) {
	if bytes.HasPrefix(b, []byte(hfsimMagic)) {
		return parseHFSim(b)
	}
	if len(b) < 24 {
		return nil, errors.New("pcapread: file too short")
	}
	switch binary.LittleEndian.Uint32(b) {
	case 0x0a0d0d0a:
		return parsePcapng(b)
	case 0xa1b2c3d4, 0xa1b23c4d:
		return parsePcap(b, binary.LittleEndian, binary.LittleEndian.Uint32(b) == 0xa1b23c4d)
	case 0xd4c3b2a1, 0x4d3cb2a1:
		return parsePcap(b, binary.BigEndian, binary.LittleEndian.Uint32(b) == 0x4d3cb2a1)
	}
	return nil, errors.New("pcapread: unknown capture format")
}

func parsePcap(b []byte, bo binary.ByteOrder, nano bool) ([]Datagram, error) {
	link := bo.Uint32(b[20:])
	b = b[24:]
	var out []Datagram
	for len(b) >= 16 {
		sec, frac, capLen := bo.Uint32(b), bo.Uint32(b[4:]), int(bo.Uint32(b[8:]))
		if capLen > len(b)-16 {
			return out, errors.New("pcapread: truncated record")
		}
		ns := int64(frac) * 1000
		if nano {
			ns = int64(frac)
		}
		if d, ok := udp(link, b[16:16+capLen]); ok {
			d.Time = time.Unix(int64(sec), ns).UTC()
			out = append(out, d)
		}
		b = b[16+capLen:]
	}
	return out, nil
}

func parsePcapng(b []byte) ([]Datagram, error) {
	type iface struct {
		link  uint32
		units float64 // unidades por segundo
	}
	var bo binary.ByteOrder = binary.LittleEndian
	var ifaces []iface
	var out []Datagram
	for len(b) >= 12 {
		typ := bo.Uint32(b)
		if typ == 0x0a0d0d0a { // SHB: fija el orden de bytes
			if binary.LittleEndian.Uint32(b[8:]) == 0x1a2b3c4d {
				bo = binary.LittleEndian
			} else {
				bo = binary.BigEndian
			}
			ifaces = nil
		}
		l := int(bo.Uint32(b[4:]))
		if l < 12 || l > len(b) || l%4 != 0 {
			return out, fmt.Errorf("pcapread: bad block length %d", l)
		}
		body := b[8 : l-4]
		switch typ {
		case 1: // IDB
			if len(body) < 8 {
				return out, errors.New("pcapread: short IDB")
			}
			ifc := iface{link: uint32(bo.Uint16(body)), units: 1e6}
			opts := body[8:]
			for len(opts) >= 4 {
				code, ol := bo.Uint16(opts), int(bo.Uint16(opts[2:]))
				if code == 0 || 4+ol > len(opts) {
					break
				}
				if code == 9 && ol >= 1 { // if_tsresol
					v := opts[4]
					if v&0x80 != 0 {
						ifc.units = float64(uint64(1) << (v & 0x7f))
					} else {
						u := 1.0
						for range v {
							u *= 10
						}
						ifc.units = u
					}
				}
				opts = opts[4+(ol+3)&^3:]
			}
			ifaces = append(ifaces, ifc)
		case 6: // EPB
			if len(body) < 20 {
				return out, errors.New("pcapread: short EPB")
			}
			id := int(bo.Uint32(body))
			ts := uint64(bo.Uint32(body[4:]))<<32 | uint64(bo.Uint32(body[8:]))
			capLen := int(bo.Uint32(body[12:]))
			if id >= len(ifaces) || 20+capLen > len(body) {
				return out, errors.New("pcapread: bad EPB")
			}
			if d, ok := udp(ifaces[id].link, body[20:20+capLen]); ok {
				sec := float64(ts) / ifaces[id].units
				d.Time = time.Unix(0, int64(sec*1e9)).UTC()
				out = append(out, d)
			}
		case 3: // SPB
			if len(ifaces) > 0 && len(body) >= 4 {
				n := min(int(bo.Uint32(body)), len(body)-4)
				if d, ok := udp(ifaces[0].link, body[4:4+n]); ok {
					out = append(out, d)
				}
			}
		}
		b = b[l:]
	}
	return out, nil
}

// udp extrae el datagrama UDP de una trama.
func udp(link uint32, f []byte) (Datagram, bool) {
	var ip []byte
	switch link {
	case 1: // Ethernet
		if len(f) < 14 {
			return Datagram{}, false
		}
		et, off := binary.BigEndian.Uint16(f[12:]), 14
		for (et == 0x8100 || et == 0x88a8) && len(f) >= off+4 {
			et, off = binary.BigEndian.Uint16(f[off+2:]), off+4
		}
		if et != 0x0800 && et != 0x86dd {
			return Datagram{}, false
		}
		ip = f[off:]
	case 101, 12, 14: // raw IP
		ip = f
	case 113: // Linux SLL
		if len(f) < 16 {
			return Datagram{}, false
		}
		ip = f[16:]
	default:
		return Datagram{}, false
	}
	if len(ip) < 1 {
		return Datagram{}, false
	}
	var src, dst netip.Addr
	var l4 []byte
	switch ip[0] >> 4 {
	case 4:
		if len(ip) < 20 {
			return Datagram{}, false
		}
		ihl := int(ip[0]&0x0f) * 4
		tot := int(binary.BigEndian.Uint16(ip[2:]))
		if ip[9] != 17 || ihl < 20 || tot < ihl || tot > len(ip) {
			return Datagram{}, false
		}
		src, dst = netip.AddrFrom4([4]byte(ip[12:16])), netip.AddrFrom4([4]byte(ip[16:20]))
		l4 = ip[ihl:tot]
	case 6:
		if len(ip) < 40 || ip[6] != 17 {
			return Datagram{}, false
		}
		pl := int(binary.BigEndian.Uint16(ip[4:]))
		if 40+pl > len(ip) {
			return Datagram{}, false
		}
		src, dst = netip.AddrFrom16([16]byte(ip[8:24])), netip.AddrFrom16([16]byte(ip[24:40]))
		l4 = ip[40 : 40+pl]
	default:
		return Datagram{}, false
	}
	if len(l4) < 8 {
		return Datagram{}, false
	}
	ul := int(binary.BigEndian.Uint16(l4[4:]))
	if ul < 8 || ul > len(l4) {
		return Datagram{}, false
	}
	return Datagram{
		Src:     netip.AddrPortFrom(src, binary.BigEndian.Uint16(l4)),
		Dst:     netip.AddrPortFrom(dst, binary.BigEndian.Uint16(l4[2:])),
		Payload: bytes.Clone(l4[8:ul]),
	}, true
}
