package asnsources

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
)

// Constantes de MRT (RFC 6396) y BGP (RFC 4271).
const (
	mrtTableDumpV2 = 13

	subPeerIndexTable  = 1
	subRIBIPv4Unicast  = 2
	subRIBIPv6Unicast  = 4
	subRIBIPv4AddPath  = 8  // RFC 8050
	subRIBIPv6AddPath  = 10 // RFC 8050
	bgpAttrASPath      = 2
	bgpAttrFlagExtLen  = 0x10
	asPathSegSet       = 1
	asPathSegSequence  = 2
	mrtHeaderLen       = 12
	maxMRTRecordLength = 16 << 20
)

var errTruncated = errors.New("registro MRT truncado")

// parseMRT interpreta un volcado de tabla BGP en MRT TABLE_DUMP_V2 (RIB de
// RIPE RIS "bview" o de RouteViews) y obtiene, por prefijo, el ASN de origen
// (último ASN del AS_PATH). Si los pares discrepan (MOAS) gana el origen más
// visto; a igualdad, el menor ASN. Exige un PEER_INDEX_TABLE previo y
// registros completos.
func parseMRT(r io.Reader, src datasets.Source) (*Result, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	res := &Result{}
	origins := map[netip.Prefix]map[uint32]int{}
	var order []netip.Prefix
	sawPeerIndex := false
	hdr := make([]byte, mrtHeaderLen)
	var body []byte
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("%w: cabecera MRT: %v", ErrCorrupt, err)
		}
		typ := binary.BigEndian.Uint16(hdr[4:6])
		sub := binary.BigEndian.Uint16(hdr[6:8])
		length := binary.BigEndian.Uint32(hdr[8:12])
		if length > maxMRTRecordLength {
			return nil, fmt.Errorf("%w: longitud de registro MRT imposible (%d)", ErrCorrupt, length)
		}
		if cap(body) < int(length) {
			body = make([]byte, length)
		}
		body = body[:length]
		if _, err := io.ReadFull(br, body); err != nil {
			return nil, fmt.Errorf("%w: %v (%v)", ErrCorrupt, errTruncated, err)
		}
		if typ != mrtTableDumpV2 {
			res.Ignored++
			continue
		}
		switch sub {
		case subPeerIndexTable:
			sawPeerIndex = true
		case subRIBIPv4Unicast, subRIBIPv6Unicast, subRIBIPv4AddPath, subRIBIPv6AddPath:
			if !sawPeerIndex {
				return nil, fmt.Errorf("%w: RIB sin PEER_INDEX_TABLE previo", ErrCorrupt)
			}
			res.Lines++
			p, seen, err := parseRIBEntry(body, sub)
			if err != nil {
				res.Invalid++
				continue
			}
			if !routable(p) || len(seen) == 0 {
				res.Ignored++
				continue
			}
			m, ok := origins[p]
			if !ok {
				m = map[uint32]int{}
				origins[p] = m
				order = append(order, p)
			}
			for _, o := range seen {
				m[o]++
			}
		default:
			res.Ignored++
		}
	}
	if !sawPeerIndex {
		return nil, fmt.Errorf("%w: no es un volcado TABLE_DUMP_V2 (sin PEER_INDEX_TABLE)", ErrCorrupt)
	}
	for _, p := range order {
		best, bestN := uint32(0), 0
		for o, n := range origins[p] {
			if n > bestN || (n == bestN && o < best) {
				best, bestN = o, n
			}
		}
		res.Routes = append(res.Routes, asn.Entry{Prefix: p, Route: asn.Route{ASN: best, Source: src.ID}})
	}
	return res, nil
}

// parseRIBEntry decodifica un registro RIB_*_UNICAST y devuelve el prefijo y
// el ASN de origen de cada entrada (par) que lo tenga determinado.
func parseRIBEntry(b []byte, sub uint16) (netip.Prefix, []uint32, error) {
	addPath := sub == subRIBIPv4AddPath || sub == subRIBIPv6AddPath
	is4 := sub == subRIBIPv4Unicast || sub == subRIBIPv4AddPath
	if len(b) < 5 {
		return netip.Prefix{}, nil, errTruncated
	}
	bits := int(b[4])
	width := 128
	if is4 {
		width = 32
	}
	n := (bits + 7) / 8
	if bits > width || len(b) < 5+n+2 {
		return netip.Prefix{}, nil, errTruncated
	}
	var raw [16]byte
	copy(raw[:], b[5:5+n])
	var a netip.Addr
	if is4 {
		a = netip.AddrFrom4([4]byte(raw[:4]))
	} else {
		a = netip.AddrFrom16(raw)
	}
	p := netip.PrefixFrom(a, bits).Masked()
	b = b[5+n:]
	count := int(binary.BigEndian.Uint16(b[:2]))
	b = b[2:]
	var origins []uint32
	for range count {
		fixed := 2 + 4 // peer index + originated time
		if addPath {
			fixed += 4
		}
		if len(b) < fixed+2 {
			return p, nil, errTruncated
		}
		attrLen := int(binary.BigEndian.Uint16(b[fixed : fixed+2]))
		b = b[fixed+2:]
		if len(b) < attrLen {
			return p, nil, errTruncated
		}
		o, ok, err := originFromAttrs(b[:attrLen])
		if err != nil {
			return p, nil, err
		}
		if ok {
			origins = append(origins, o) // un voto por cada par que lo anuncia
		}
		b = b[attrLen:]
	}
	if len(b) != 0 {
		return p, nil, errors.New("bytes sobrantes en el registro RIB")
	}
	return p, origins, nil
}

// originFromAttrs busca AS_PATH (ASN de 4 bytes en TABLE_DUMP_V2) y devuelve
// el último ASN de la última secuencia; un AS_SET final solo vale si tiene un
// único miembro.
func originFromAttrs(b []byte) (uint32, bool, error) {
	for len(b) > 0 {
		if len(b) < 3 {
			return 0, false, errTruncated
		}
		flags, code := b[0], b[1]
		var l, off int
		if flags&bgpAttrFlagExtLen != 0 {
			if len(b) < 4 {
				return 0, false, errTruncated
			}
			l, off = int(binary.BigEndian.Uint16(b[2:4])), 4
		} else {
			l, off = int(b[2]), 3
		}
		if len(b) < off+l {
			return 0, false, errTruncated
		}
		val := b[off : off+l]
		b = b[off+l:]
		if code != bgpAttrASPath {
			continue
		}
		var (
			origin uint32
			ok     bool
		)
		for len(val) > 0 {
			if len(val) < 2 {
				return 0, false, errTruncated
			}
			segType, segLen := val[0], int(val[1])
			if len(val) < 2+4*segLen {
				return 0, false, errTruncated
			}
			if segLen > 0 {
				last := binary.BigEndian.Uint32(val[2+4*(segLen-1):])
				switch {
				case segType == asPathSegSequence:
					origin, ok = last, true
				case segType == asPathSegSet && segLen == 1:
					origin, ok = last, true
				default:
					ok = false // origen ambiguo (AS_SET agregado)
				}
			}
			val = val[2+4*segLen:]
		}
		return origin, ok && origin != 0, nil
	}
	return 0, false, nil
}
