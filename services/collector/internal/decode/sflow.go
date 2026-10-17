package decode

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

// Decodificador sFlow v5 (especificación sflow.org "sFlow Version 5", julio
// de 2004; docs/vendors/huawei.md §7.3). sFlow no exporta flujos sino
// muestras de paquetes: cada flow_sample (formato 1) o flow_sample_expanded
// (3) con un registro sampled_header (1) se convierte en un FlowRecord de un
// paquete con los bytes de la trama original y la tasa de muestreo de la
// muestra (SamplingRate); el ingester multiplica bytes y paquetes por esa
// tasa, igual que con NetFlow/IPFIX muestreado. Si no hay cabecera se usan
// sampled_ipv4 (3) / sampled_ipv6 (4). Las counter samples (2 y 4) no
// generan flujos: se cuentan como registros de opciones (las interfaces ya
// se leen por SNMP). Los formatos de empresa distinta de 0 se ignoran. Nada
// aquí puede entrar en pánico con datos arbitrarios (fuzzing).

// VersionSFlow es la versión interna con la que se marca un datagrama sFlow
// v5 en Header.Version (el campo de versión de sFlow es de 32 bits y vale 5;
// se usa un valor que no choca con NetFlow v5/v9 ni con IPFIX).
const VersionSFlow uint16 = 0x5F05

// FlowSourceSFlow es flow_source de los registros sFlow (ClickHouse sflow_v5).
const FlowSourceSFlow = "sflow_v5"

const (
	sflowVersion           = 5
	sflowFlowSample        = 1
	sflowCounterSample     = 2
	sflowFlowSampleExp     = 3
	sflowCounterSampleExp  = 4
	sflowRecRawHeader      = 1
	sflowRecIPv4           = 3
	sflowRecIPv6           = 4
	sflowRecExtSwitch      = 1001
	sflowRecExtRouter      = 1002
	sflowHdrEthernet       = 1
	sflowHdrIPv4           = 11
	sflowHdrIPv6           = 12
	sflowMaxSamples        = 1024
	sflowMaxRecords        = 64
	sflowIfFormatMask      = 0xC0000000
	sflowIfValueMask       = 0x3FFFFFFF
	etherTypeIPv4          = 0x0800
	etherTypeIPv6          = 0x86DD
	etherTypeVLAN          = 0x8100
	etherTypeQinQ          = 0x88A8
	etherTypeMPLS          = 0x8847
	ipProtoICMP            = 1
	ipProtoTCP             = 6
	ipProtoUDP             = 17
	ipProtoICMPv6          = 58
	ipv6HopByHop           = 0
	ipv6Routing            = 43
	ipv6Fragment           = 44
	ipv6DestOpts           = 60
	maxVLANTags            = 2
	maxMPLSLabels          = 8
	maxIPv6ExtensionHeader = 4
)

// sreader lee enteros XDR (big endian, alineados a 4) sin salirse del búfer.
type sreader struct {
	b   []byte
	bad bool
}

func (r *sreader) u32() uint32 {
	if len(r.b) < 4 {
		r.bad = true
		r.b = nil
		return 0
	}
	v := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

func (r *sreader) bytes(n int) []byte {
	if n < 0 || len(r.b) < n {
		r.bad = true
		r.b = nil
		return nil
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}

// opaque lee un bloque de longitud n rellenado a múltiplo de 4.
func (r *sreader) opaque(n int) []byte {
	v := r.bytes(n)
	if pad := (4 - n%4) % 4; pad > 0 && !r.bad {
		r.bytes(pad)
	}
	return v
}

func (r *sreader) addr() netip.Addr {
	switch r.u32() {
	case 1:
		if b := r.bytes(4); b != nil {
			return netip.AddrFrom4([4]byte(b))
		}
	case 2:
		if b := r.bytes(16); b != nil {
			return netip.AddrFrom16([16]byte(b))
		}
	case 0:
		return netip.Addr{}
	default:
		r.bad = true
	}
	return netip.Addr{}
}

// sflowSample son los datos comunes de una flow sample.
type sflowSample struct {
	rate          uint32
	input, output uint32
}

// decodeSFlow decodifica un datagrama sFlow v5.
func (d *Decoder) decodeSFlow(p []byte) (Result, error) {
	var res Result
	r := &sreader{b: p}
	if r.u32() != sflowVersion {
		return res, fmt.Errorf("%w: sflow version", ErrVersion)
	}
	agent := r.addr()
	sub := r.u32()
	seq := r.u32()
	uptime := r.u32()
	n := r.u32()
	if r.bad || !agent.IsValid() {
		return res, fmt.Errorf("%w: sflow header", ErrMalformed)
	}
	now := d.opts.Now().UTC()
	res.Header = Header{Version: VersionSFlow, Sequence: seq, ODID: sub, ExportTime: now.Truncate(time.Second), SysUptime: uptime}
	if n > sflowMaxSamples {
		return res, fmt.Errorf("%w: %d sflow samples", ErrMalformed, n)
	}
	for i := uint32(0); i < n; i++ {
		format := r.u32()
		l := r.u32()
		body := r.opaque(int(l)) //nolint:gosec // acotado por len(r.b)
		if r.bad {
			return res, fmt.Errorf("%w: sflow sample %d", ErrMalformed, i)
		}
		if format>>12 != 0 { // empresa distinta de sFlow estándar
			continue
		}
		switch format & 0xfff {
		case sflowFlowSample, sflowFlowSampleExp:
			rec, ok, err := d.sflowFlowSample(body, format&0xfff == sflowFlowSampleExp, sub, now)
			if err != nil {
				return res, err
			}
			res.DataRecords++
			if ok {
				res.Records = append(res.Records, rec)
			}
		case sflowCounterSample, sflowCounterSampleExp:
			res.OptionsRecords++
		}
	}
	return res, nil
}

func (d *Decoder) sflowFlowSample(body []byte, expanded bool, sub uint32, now time.Time) (flowpb.FlowRecord, bool, error) {
	var rec flowpb.FlowRecord
	r := &sreader{b: body}
	var s sflowSample
	r.u32() // sequence_number
	if expanded {
		r.u32() // source_id_type
		r.u32() // source_id_index
	} else {
		r.u32() // source_id
	}
	s.rate = r.u32()
	r.u32() // sample_pool
	r.u32() // drops
	if expanded {
		inFmt, inVal, outFmt, outVal := r.u32(), r.u32(), r.u32(), r.u32()
		if inFmt == 0 {
			s.input = inVal
		}
		if outFmt == 0 {
			s.output = outVal
		}
	} else {
		in, out := r.u32(), r.u32()
		if in&sflowIfFormatMask == 0 {
			s.input = in & sflowIfValueMask
		}
		if out&sflowIfFormatMask == 0 {
			s.output = out & sflowIfValueMask
		}
	}
	nrec := r.u32()
	if r.bad || nrec > sflowMaxRecords {
		return rec, false, fmt.Errorf("%w: sflow flow sample", ErrMalformed)
	}
	ok := false
	haveHeader := false
	for i := uint32(0); i < nrec; i++ {
		format := r.u32()
		l := r.u32()
		data := r.opaque(int(l)) //nolint:gosec // acotado por len(r.b)
		if r.bad {
			return rec, false, fmt.Errorf("%w: sflow flow record %d", ErrMalformed, i)
		}
		if format>>12 != 0 {
			continue
		}
		switch format & 0xfff {
		case sflowRecRawHeader:
			if sflowRawHeader(data, &rec) {
				ok, haveHeader = true, true
			}
		case sflowRecIPv4, sflowRecIPv6:
			if !haveHeader && sflowSampledIP(data, format&0xfff == sflowRecIPv6, &rec) {
				ok = true
			}
		case sflowRecExtSwitch:
			x := &sreader{b: data}
			vlan := x.u32()
			if !x.bad && rec.VlanID == 0 && vlan > 0 && vlan < 4096 {
				rec.VlanID = vlan
			}
		case sflowRecExtRouter:
			x := &sreader{b: data}
			if a := x.addr(); !x.bad && a.IsValid() && !a.IsUnspecified() {
				rec.NextHop = a.Unmap()
			}
		}
	}
	if !ok {
		return rec, false, nil
	}
	rec.Packets = 1
	if s.rate > 0 {
		rec.SamplingRate = s.rate
	}
	rec.InputIfIndex, rec.OutputIfIndex = s.input, s.output
	// sFlow no lleva marcas de tiempo por muestra: la de llegada.
	rec.TS, rec.FlowStart = now, now
	rec.FlowSource = FlowSourceSFlow
	rec.ObservationDomainID = sub
	return rec, true, nil
}

// sflowRawHeader interpreta un sampled_header: protocolo de cabecera,
// longitud de la trama original y la cabecera capturada.
func sflowRawHeader(data []byte, rec *flowpb.FlowRecord) bool {
	r := &sreader{b: data}
	proto := r.u32()
	frameLen := r.u32()
	r.u32() // stripped
	hl := r.u32()
	hdr := r.bytes(int(hl)) //nolint:gosec // acotado por len(r.b)
	if r.bad {
		return false
	}
	switch proto {
	case sflowHdrEthernet:
		if !parseEthernet(hdr, rec) {
			return false
		}
	case sflowHdrIPv4, sflowHdrIPv6:
		if !parseIP(hdr, rec) {
			return false
		}
	default:
		return false
	}
	rec.Bytes = uint64(frameLen)
	return true
}

// sflowSampledIP interpreta sampled_ipv4 / sampled_ipv6.
func sflowSampledIP(data []byte, v6 bool, rec *flowpb.FlowRecord) bool {
	r := &sreader{b: data}
	length := r.u32()
	proto := r.u32()
	n := 4
	if v6 {
		n = 16
	}
	src, dst := r.bytes(n), r.bytes(n)
	sp, dp, flags := r.u32(), r.u32(), r.u32()
	r.u32() // tos / priority
	if r.bad {
		return false
	}
	rec.SrcIP, rec.DstIP = addr(src), addr(dst)
	rec.Protocol = uint8(proto)        //nolint:gosec // protocolo IP
	rec.SrcPort = uint16(sp)           //nolint:gosec // puerto
	rec.DstPort = uint16(dp)           //nolint:gosec // puerto
	rec.TCPFlags = uint8(flags & 0xff) //nolint:gosec // 8 bits
	rec.Bytes = uint64(length)
	return rec.SrcIP.IsValid() && rec.DstIP.IsValid()
}

// parseEthernet salta MAC, etiquetas 802.1Q/802.1ad y MPLS y sigue con IP.
func parseEthernet(b []byte, rec *flowpb.FlowRecord) bool {
	if len(b) < 14 {
		return false
	}
	et := binary.BigEndian.Uint16(b[12:])
	b = b[14:]
	for i := 0; i < maxVLANTags && (et == etherTypeVLAN || et == etherTypeQinQ); i++ {
		if len(b) < 4 {
			return false
		}
		if vid := uint32(binary.BigEndian.Uint16(b) & 0x0fff); rec.VlanID == 0 && vid != 0 {
			rec.VlanID = vid
		}
		et = binary.BigEndian.Uint16(b[2:])
		b = b[4:]
	}
	switch et {
	case etherTypeIPv4, etherTypeIPv6:
		return parseIP(b, rec)
	case etherTypeMPLS:
		for i := 0; i < maxMPLSLabels; i++ {
			if len(b) < 4 {
				return false
			}
			bottom := b[2]&0x01 != 0
			b = b[4:]
			if bottom {
				return parseIP(b, rec)
			}
		}
	}
	return false
}

// parseIP interpreta una cabecera IPv4 o IPv6 (por el nibble de versión) y
// la de transporte si está completa.
func parseIP(b []byte, rec *flowpb.FlowRecord) bool {
	if len(b) < 1 {
		return false
	}
	switch b[0] >> 4 {
	case 4:
		if len(b) < 20 {
			return false
		}
		ihl := int(b[0]&0x0f) * 4
		if ihl < 20 || len(b) < ihl {
			return false
		}
		rec.Protocol = b[9]
		rec.SrcIP = netip.AddrFrom4([4]byte(b[12:16]))
		rec.DstIP = netip.AddrFrom4([4]byte(b[16:20]))
		if binary.BigEndian.Uint16(b[6:])&0x1fff != 0 { // fragmento no inicial: sin puertos
			return true
		}
		parseTransport(b[ihl:], rec)
		return true
	case 6:
		if len(b) < 40 {
			return false
		}
		next := b[6]
		rec.SrcIP = netip.AddrFrom16([16]byte(b[8:24])).Unmap()
		rec.DstIP = netip.AddrFrom16([16]byte(b[24:40])).Unmap()
		b = b[40:]
	ext:
		for i := 0; i < maxIPv6ExtensionHeader; i++ {
			switch next {
			case ipv6HopByHop, ipv6Routing, ipv6DestOpts:
				if len(b) < 8 {
					rec.Protocol = next
					return true
				}
				l := (int(b[1]) + 1) * 8
				next = b[0]
				if len(b) < l {
					rec.Protocol = next
					return true
				}
				b = b[l:]
				continue
			case ipv6Fragment:
				if len(b) < 8 {
					rec.Protocol = next
					return true
				}
				off := binary.BigEndian.Uint16(b[2:]) &^ 0x7
				next = b[0]
				b = b[8:]
				if off != 0 {
					rec.Protocol = next
					return true
				}
				continue
			}
			break ext
		}
		rec.Protocol = next
		parseTransport(b, rec)
		return true
	}
	return false
}

func parseTransport(b []byte, rec *flowpb.FlowRecord) {
	switch rec.Protocol {
	case ipProtoTCP:
		if len(b) >= 14 {
			rec.SrcPort, rec.DstPort = binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:])
			rec.TCPFlags = b[13]
		} else if len(b) >= 4 {
			rec.SrcPort, rec.DstPort = binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:])
		}
	case ipProtoUDP:
		if len(b) >= 4 {
			rec.SrcPort, rec.DstPort = binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:])
		}
	case ipProtoICMP, ipProtoICMPv6:
		if len(b) >= 2 {
			rec.ICMPTypeCode = uint16(b[0])<<8 | uint16(b[1])
		}
	}
}
