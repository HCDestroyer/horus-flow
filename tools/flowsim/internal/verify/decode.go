// Package verify decodifica datagramas NetFlow v9/IPFIX con una librería
// independiente del codificador del simulador (goflow2) y comprueba lo
// decodificado contra expected.json: transporte (secuencias, plantillas),
// conteos, atribución de clientes y señales por escenario.
package verify

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/netsampler/goflow2/v2/decoders/netflow"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
)

// PacketInfo resume la cabecera y los sets de un datagrama.
type PacketInfo struct {
	Version         uint16
	Sequence        uint32
	ObservationID   uint32
	ExportTime      time.Time
	SysUptime       uint32 // solo v9
	TemplateRecords int
	DataRecords     int
	// MissingTemplate cuenta sets de datos sin plantilla conocida.
	MissingTemplate int
}

// Decoder decodifica datagramas manteniendo las plantillas por exportador.
type Decoder struct {
	templates map[string]netflow.NetFlowTemplateSystem
}

// NewDecoder crea un decodificador vacío.
func NewDecoder() *Decoder {
	return &Decoder{templates: map[string]netflow.NetFlowTemplateSystem{}}
}

// Decode decodifica un datagrama del exportador identificado por source.
func (d *Decoder) Decode(source string, payload []byte) (PacketInfo, []flow.Record, error) {
	var info PacketInfo
	if err := precheck(payload); err != nil {
		return info, nil, err
	}
	ts := d.templates[source]
	if ts == nil {
		ts = netflow.CreateTemplateSystem()
		d.templates[source] = ts
	}
	var v9 netflow.NFv9Packet
	var ipfix netflow.IPFIXPacket
	err := netflow.DecodeMessageVersion(bytes.NewBuffer(payload), ts, &v9, &ipfix)
	var sets []any
	var boot int64 // ms Unix; solo v9
	switch binary.BigEndian.Uint16(payload) {
	case 9:
		info.Version, info.Sequence, info.ObservationID = 9, v9.SequenceNumber, v9.SourceId
		info.ExportTime = time.Unix(int64(v9.UnixSeconds), 0).UTC()
		info.SysUptime = v9.SystemUptime
		boot = int64(v9.UnixSeconds)*1000 - int64(v9.SystemUptime)
		sets = v9.FlowSets
	default:
		info.Version, info.Sequence, info.ObservationID = 10, ipfix.SequenceNumber, ipfix.ObservationDomainId
		info.ExportTime = time.Unix(int64(ipfix.ExportTime), 0).UTC()
		sets = ipfix.FlowSets
	}
	var recs []flow.Record
	for _, s := range sets {
		switch fs := s.(type) {
		case netflow.TemplateFlowSet:
			info.TemplateRecords += len(fs.Records)
		case netflow.DataFlowSet:
			for _, dr := range fs.Records {
				recs = append(recs, toRecord(dr.Values, boot))
			}
			info.DataRecords += len(fs.Records)
		case netflow.RawFlowSet:
			info.MissingTemplate++
		}
	}
	if err != nil && !errors.Is(err, netflow.ErrorTemplateNotFound) {
		return info, recs, fmt.Errorf("goflow2: %w", err)
	}
	return info, recs, nil
}

func beUint(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

func addrOf(b []byte) netip.Addr {
	switch len(b) {
	case 4:
		return netip.AddrFrom4([4]byte(b))
	case 16:
		return netip.AddrFrom16([16]byte(b))
	}
	return netip.Addr{}
}

func toRecord(values []netflow.DataField, boot int64) flow.Record {
	var r flow.Record
	var start, end, startAbs, endAbs int64
	var hasStart, hasEnd, hasAbsStart, hasAbsEnd bool
	sysInit := boot
	for _, v := range values {
		if v.PenProvided {
			continue
		}
		b, ok := v.Value.([]byte)
		if !ok {
			continue
		}
		u := beUint(b)
		switch v.Type {
		case flow.IEOctetDeltaCount:
			r.Bytes = u
		case flow.IEPacketDeltaCount:
			r.Packets = u
		case flow.IEProtocol:
			r.Proto = uint8(u)
		case flow.IEToS:
			r.ToS = uint8(u)
		case flow.IETCPFlags:
			r.TCPFlags = uint8(u)
		case flow.IESrcPort:
			r.SrcPort = uint16(u)
		case flow.IEDstPort:
			r.DstPort = uint16(u)
		case flow.IESrcIPv4, flow.IESrcIPv6:
			r.SrcIP = addrOf(b)
		case flow.IEDstIPv4, flow.IEDstIPv6:
			r.DstIP = addrOf(b)
		case flow.IENextHopV4, flow.IENextHopV6:
			if !allZero(b) {
				r.NextHop = addrOf(b)
			}
		case flow.IESrcMaskV4, flow.IESrcMaskV6:
			r.SrcMask = uint8(u)
		case flow.IEDstMaskV4, flow.IEDstMaskV6:
			r.DstMask = uint8(u)
		case flow.IEIngressIf:
			r.InIf = uint32(u)
		case flow.IEEgressIf:
			r.OutIf = uint32(u)
		case flow.IEFlowLabelV6:
			r.FlowLabel = uint32(u)
		case flow.IEICMPTypeCodeV4, flow.IEICMPTypeCodeV6:
			r.ICMPTypeCode = uint16(u)
		case flow.IEIPTTL: // RouterOS 7: un solo TTL (IE 192)
			r.MinTTL, r.MaxTTL = uint8(u), uint8(u)
		case flow.IEICMPTypeV4, flow.IEICMPTypeV6:
			r.ICMPTypeCode = r.ICMPTypeCode&0x00ff | uint16(u)<<8
		case flow.IEICMPCodeV4, flow.IEICMPCodeV6:
			r.ICMPTypeCode = r.ICMPTypeCode&0xff00 | uint16(u&0xff)
		case flow.IEPostSrcMAC:
			if len(b) == 6 {
				r.PostSrcMAC = [6]byte(b)
			}
		case flow.IEPostDstMAC:
			if len(b) == 6 {
				r.PostDstMAC = [6]byte(b)
			}
		case flow.IEMinTTL:
			r.MinTTL = uint8(u)
		case flow.IEMaxTTL:
			r.MaxTTL = uint8(u)
		case flow.IESrcMAC:
			if len(b) == 6 {
				r.SrcMAC = [6]byte(b)
			}
		case flow.IEDstMAC:
			if len(b) == 6 {
				r.DstMAC = [6]byte(b)
			}
		case flow.IEFlowStartSysUpTime:
			start, hasStart = int64(u), true
		case flow.IEFlowEndSysUpTime:
			end, hasEnd = int64(u), true
		case flow.IEFlowStartMilliseconds:
			startAbs, hasAbsStart = int64(u), true
		case flow.IEFlowEndMilliseconds:
			endAbs, hasAbsEnd = int64(u), true
		case flow.IESysInitTimeMs:
			sysInit = int64(u)
		case flow.IEPostNATSrcIPv4:
			r.PostNATSrc = addrOf(b)
		case flow.IEPostNATDstIPv4:
			r.PostNATDst = addrOf(b)
		case flow.IEPostNAPTSrcPort:
			r.PostNATSrcPort = uint16(u)
		case flow.IEPostNAPTDstPort:
			r.PostNATDstPort = uint16(u)
		}
	}
	switch {
	case hasAbsStart:
		r.Start = time.UnixMilli(startAbs).UTC()
	case hasStart && sysInit != 0:
		r.Start = time.UnixMilli(sysInit + start).UTC()
	}
	switch {
	case hasAbsEnd:
		r.End = time.UnixMilli(endAbs).UTC()
	case hasEnd && sysInit != 0:
		r.End = time.UnixMilli(sysInit + end).UTC()
	}
	return r
}

// precheck valida la estructura de sets antes de entregar el datagrama a
// goflow2 (que no tolera plantillas de longitud cero).
func precheck(p []byte) error {
	if len(p) < 4 {
		return errors.New("datagrama demasiado corto")
	}
	ver := binary.BigEndian.Uint16(p)
	hl := 16
	switch ver {
	case 9:
		hl = 20
	case 10:
		if l := int(binary.BigEndian.Uint16(p[2:])); l < hl || l > len(p) {
			return fmt.Errorf("IPFIX: longitud de mensaje %d inválida", l)
		}
	default:
		return fmt.Errorf("versión %d no soportada (solo NetFlow v9 e IPFIX)", ver)
	}
	if len(p) < hl {
		return errors.New("cabecera incompleta")
	}
	rest := p[hl:]
	if ver == 10 {
		rest = p[hl:binary.BigEndian.Uint16(p[2:])]
	}
	for len(rest) >= 4 {
		id, l := binary.BigEndian.Uint16(rest), int(binary.BigEndian.Uint16(rest[2:]))
		if l < 4 || l > len(rest) {
			return fmt.Errorf("set %d con longitud %d inválida", id, l)
		}
		body := rest[4:l]
		if (ver == 9 && id == 0) || (ver == 10 && id == 2) {
			if err := checkTemplates(body, ver == 10); err != nil {
				return err
			}
		}
		if (ver == 9 && id == 1) || (ver == 10 && id == 3) {
			if err := checkOptionTemplates(body, ver == 10); err != nil {
				return err
			}
		}
		if id > 3 && id < 256 {
			return fmt.Errorf("ID de set %d reservado", id)
		}
		rest = rest[l:]
	}
	return nil
}

func checkTemplates(b []byte, ipfix bool) error {
	for len(b) >= 4 {
		tid, n := binary.BigEndian.Uint16(b), int(binary.BigEndian.Uint16(b[2:]))
		b = b[4:]
		if tid < 256 {
			return fmt.Errorf("plantilla con ID %d reservado", tid)
		}
		total := 0
		for i := 0; i < n; i++ {
			if len(b) < 4 {
				return fmt.Errorf("plantilla %d truncada", tid)
			}
			typ, l := binary.BigEndian.Uint16(b), int(binary.BigEndian.Uint16(b[2:]))
			b = b[4:]
			if ipfix && typ&0x8000 != 0 {
				if len(b) < 4 {
					return fmt.Errorf("plantilla %d truncada", tid)
				}
				b = b[4:]
			}
			if l == 0xffff {
				l = 1
			}
			total += l
		}
		if total == 0 {
			return fmt.Errorf("plantilla %d de longitud cero", tid)
		}
	}
	return nil
}

func checkOptionTemplates(b []byte, ipfix bool) error {
	for len(b) >= 6 {
		tid := binary.BigEndian.Uint16(b)
		var nfields int
		if ipfix {
			nfields = int(binary.BigEndian.Uint16(b[2:]))
			b = b[6:]
		} else {
			nfields = (int(binary.BigEndian.Uint16(b[2:])) + int(binary.BigEndian.Uint16(b[4:]))) / 4
			b = b[6:]
		}
		total := 0
		for i := 0; i < nfields; i++ {
			if len(b) < 4 {
				return fmt.Errorf("plantilla de opciones %d truncada", tid)
			}
			typ, l := binary.BigEndian.Uint16(b), int(binary.BigEndian.Uint16(b[2:]))
			b = b[4:]
			if ipfix && typ&0x8000 != 0 {
				if len(b) < 4 {
					return fmt.Errorf("plantilla de opciones %d truncada", tid)
				}
				b = b[4:]
			}
			if l == 0xffff {
				l = 1
			}
			total += l
		}
		if total == 0 {
			return fmt.Errorf("plantilla de opciones %d de longitud cero", tid)
		}
		if !ipfix {
			return nil // v9: el resto es relleno
		}
	}
	return nil
}
