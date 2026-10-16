package pcapread

import (
	"encoding/binary"
	"time"
)

// Rebase devuelve una copia de los datagramas con los tiempos de flujo
// desplazados para que la captura empiece en start (redondeado al segundo):
// la marca de captura de cada datagrama, la hora de exportación de la cabecera
// (NetFlow v5/v9 e IPFIX) y, en IPFIX, los campos de tiempo absolutos de los
// registros (systemInitTimeMilliseconds, flowStart/End en s y ms,
// observationTime en s y ms). Los tiempos relativos al arranque (sysUpTime)
// no cambian: siguen siendo relativos a un arranque desplazado igual.
//
// Sirve para reproducir capturas reales antiguas: sin desplazar, sus flujos
// caen fuera del TTL de flows_raw (7 días) y ClickHouse los descarta.
func Rebase(ds []Datagram, start time.Time) []Datagram {
	out := make([]Datagram, len(ds))
	if len(ds) == 0 {
		return out
	}
	delta := start.Truncate(time.Second).Sub(ds[0].Time.Truncate(time.Second))
	sec, ms := int64(delta/time.Second), delta.Milliseconds()
	tpl := map[tplKey][]tplField{}
	for i, d := range ds {
		p := append([]byte(nil), d.Payload...)
		rebasePayload(p, d, sec, ms, tpl)
		out[i] = Datagram{Time: d.Time.Add(delta), Src: d.Src, Dst: d.Dst, Payload: p}
	}
	return out
}

type tplKey struct {
	src    string
	domain uint32
	id     uint16
}

type tplField struct {
	ie     uint16 // 0 para campos de empresa (no se tocan)
	length uint16 // 65535 = longitud variable
}

func addU32(b []byte, d int64) {
	binary.BigEndian.PutUint32(b, uint32(int64(binary.BigEndian.Uint32(b))+d)) //nolint:gosec // tiempo Unix en 32 bits
}

func addU64(b []byte, d int64) {
	binary.BigEndian.PutUint64(b, uint64(int64(binary.BigEndian.Uint64(b))+d)) //nolint:gosec // ms Unix
}

func rebasePayload(p []byte, d Datagram, sec, ms int64, tpl map[tplKey][]tplField) {
	if len(p) < 4 {
		return
	}
	switch binary.BigEndian.Uint16(p) {
	case 5, 9: // NetFlow v5 / v9: unix_secs en la cabecera; los registros usan sysUpTime.
		if len(p) >= 12 {
			addU32(p[8:12], sec)
		}
	case 10:
		rebaseIPFIX(p, d.Src.Addr().String(), sec, ms, tpl)
	}
}

// rebaseIPFIX recorre los conjuntos de un mensaje IPFIX (RFC 7011): aprende
// las plantillas y desplaza los campos de tiempo absolutos de los registros.
func rebaseIPFIX(p []byte, src string, sec, ms int64, tpl map[tplKey][]tplField) {
	if len(p) < 16 {
		return
	}
	total := int(binary.BigEndian.Uint16(p[2:4]))
	if total > len(p) {
		total = len(p)
	}
	addU32(p[4:8], sec)
	domain := binary.BigEndian.Uint32(p[12:16])
	for off := 16; off+4 <= total; {
		setID := binary.BigEndian.Uint16(p[off:])
		setLen := int(binary.BigEndian.Uint16(p[off+2:]))
		if setLen < 4 || off+setLen > total {
			return
		}
		body := p[off+4 : off+setLen]
		switch {
		case setID == 2 || setID == 3:
			learnTemplates(body, setID == 3, tplKey{src: src, domain: domain}, tpl)
		case setID >= 256:
			if fields, ok := tpl[tplKey{src: src, domain: domain, id: setID}]; ok {
				shiftRecords(body, fields, sec, ms)
			}
		}
		off += setLen
	}
}

func learnTemplates(b []byte, options bool, key tplKey, tpl map[tplKey][]tplField) {
	for len(b) >= 4 {
		id := binary.BigEndian.Uint16(b)
		count := int(binary.BigEndian.Uint16(b[2:]))
		b = b[4:]
		if options {
			if len(b) < 2 {
				return
			}
			b = b[2:] // scope field count: los campos de ámbito van en la misma lista
		}
		if id < 256 || count == 0 {
			return // relleno o retirada de plantilla
		}
		fields := make([]tplField, 0, count)
		for i := 0; i < count; i++ {
			if len(b) < 4 {
				return
			}
			ie := binary.BigEndian.Uint16(b)
			f := tplField{ie: ie & 0x7fff, length: binary.BigEndian.Uint16(b[2:])}
			b = b[4:]
			if ie&0x8000 != 0 { // número de empresa
				if len(b) < 4 {
					return
				}
				b = b[4:]
				f.ie = 0
			}
			fields = append(fields, f)
		}
		key.id = id
		tpl[key] = fields
	}
}

func shiftRecords(b []byte, fields []tplField, sec, ms int64) {
	for len(b) > 0 {
		off := 0
		for _, f := range fields {
			n := int(f.length)
			if f.length == 65535 {
				if off >= len(b) {
					return
				}
				n = int(b[off])
				off++
				if n == 255 {
					if off+2 > len(b) {
						return
					}
					n = int(binary.BigEndian.Uint16(b[off:]))
					off += 2
				}
			}
			if off+n > len(b) {
				return // relleno al final del conjunto
			}
			v := b[off : off+n]
			switch {
			case n == 4 && (f.ie == 150 || f.ie == 151 || f.ie == 322):
				addU32(v, sec)
			case n == 8 && (f.ie == 152 || f.ie == 153 || f.ie == 160 || f.ie == 323):
				addU64(v, ms)
			}
			off += n
		}
		if off == 0 {
			return
		}
		b = b[off:]
	}
}
