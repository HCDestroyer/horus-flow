package anonymize

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

// IEs que transportan direcciones o MAC y se reescriben (IANA IPFIX; los
// mismos IDs en NetFlow v9).
var (
	v4IEs = map[uint16]bool{
		8: true, 12: true, 15: true, 18: true, 44: true, 45: true, 130: true, 225: true, 226: true,
	}
	v6IEs = map[uint16]bool{
		27: true, 28: true, 62: true, 63: true, 131: true, 281: true, 282: true,
	}
	macIEs = map[uint16]bool{56: true, 57: true, 80: true, 81: true, 365: true, 414: true}
)

const varLen = 0xffff

type field struct {
	id         uint16
	length     uint16
	enterprise bool
}

type template struct {
	fields []field
	// scope: plantilla de opciones (solo informativo).
	options bool
}

func (t template) equal(o template) bool {
	if len(t.fields) != len(o.fields) || t.options != o.options {
		return false
	}
	for i := range t.fields {
		if t.fields[i] != o.fields[i] {
			return false
		}
	}
	return true
}

// tmplKey identifica una plantilla: exportador, dominio de observación e ID.
type tmplKey struct {
	exporter netip.Addr
	domain   uint32
	id       uint16
}

// message es la vista de un datagrama NetFlow v9 / IPFIX.
type message struct {
	version uint16
	domain  uint32
	sets    []set
}

type set struct {
	id   uint16
	body []byte // vista sobre el datagrama (se reescribe en su sitio)
}

var errNotFlow = errors.New("no es NetFlow v9 ni IPFIX")

func parseMessage(p []byte) (*message, error) {
	if len(p) < 4 {
		return nil, errNotFlow
	}
	m := &message{version: binary.BigEndian.Uint16(p)}
	var rest []byte
	switch m.version {
	case 10:
		if len(p) < 16 {
			return nil, errors.New("IPFIX: cabecera incompleta")
		}
		l := int(binary.BigEndian.Uint16(p[2:]))
		if l < 16 || l > len(p) {
			return nil, fmt.Errorf("IPFIX: longitud %d inválida", l)
		}
		m.domain = binary.BigEndian.Uint32(p[12:])
		rest = p[16:l]
	case 9:
		if len(p) < 20 {
			return nil, errors.New("v9: cabecera incompleta")
		}
		m.domain = binary.BigEndian.Uint32(p[16:])
		rest = p[20:]
	default:
		return nil, errNotFlow
	}
	for len(rest) >= 4 {
		id, l := binary.BigEndian.Uint16(rest), int(binary.BigEndian.Uint16(rest[2:]))
		if l < 4 || l > len(rest) {
			return nil, fmt.Errorf("set %d de longitud %d inválida", id, l)
		}
		m.sets = append(m.sets, set{id: id, body: rest[4:l]})
		rest = rest[l:]
	}
	if len(rest) != 0 && m.version == 10 {
		return nil, errors.New("IPFIX: bytes sobrantes tras el último set")
	}
	return m, nil
}

func isTemplateSet(version, id uint16) bool {
	return (version == 10 && id == 2) || (version == 9 && id == 0)
}

func isOptionsSet(version, id uint16) bool {
	return (version == 10 && id == 3) || (version == 9 && id == 1)
}

// parseTemplates lee las plantillas (de datos u opciones) de un set.
func parseTemplates(version uint16, id uint16, b []byte) (map[uint16]template, error) {
	out := map[uint16]template{}
	opts := isOptionsSet(version, id)
	for len(b) >= 4 {
		tid := binary.BigEndian.Uint16(b)
		if tid == 0 && !opts {
			break // relleno
		}
		var n int
		switch {
		case opts && version == 10:
			if len(b) < 6 {
				return nil, errors.New("plantilla de opciones truncada")
			}
			n = int(binary.BigEndian.Uint16(b[2:]))
			b = b[6:]
		case opts:
			if len(b) < 6 {
				return nil, errors.New("plantilla de opciones truncada")
			}
			n = (int(binary.BigEndian.Uint16(b[2:])) + int(binary.BigEndian.Uint16(b[4:]))) / 4
			b = b[6:]
		default:
			n = int(binary.BigEndian.Uint16(b[2:]))
			b = b[4:]
		}
		if tid < 256 {
			return nil, fmt.Errorf("plantilla con ID %d reservado", tid)
		}
		t := template{options: opts}
		for i := 0; i < n; i++ {
			if len(b) < 4 {
				return nil, fmt.Errorf("plantilla %d truncada", tid)
			}
			f := field{id: binary.BigEndian.Uint16(b), length: binary.BigEndian.Uint16(b[2:])}
			b = b[4:]
			if version == 10 && f.id&0x8000 != 0 {
				if len(b) < 4 {
					return nil, fmt.Errorf("plantilla %d truncada", tid)
				}
				f.id &= 0x7fff
				f.enterprise = true
				b = b[4:]
			}
			t.fields = append(t.fields, f)
		}
		out[tid] = t
		if opts && version == 9 {
			break // v9: una plantilla de opciones por FlowSet; el resto es relleno
		}
	}
	return out, nil
}

// walkRecords recorre los registros de un set de datos y llama a fn con los
// valores de cada uno (vistas modificables, en el orden de la plantilla).
// Devuelve el nº de registros; los bytes finales que no forman un registro
// son relleno.
func walkRecords(t template, b []byte, fn func(vals [][]byte)) (int, error) {
	fixed := 0
	variable := false
	for _, f := range t.fields {
		if f.length == varLen {
			variable = true
		} else {
			fixed += int(f.length)
		}
	}
	if fixed == 0 && !variable {
		return 0, errors.New("plantilla de longitud cero")
	}
	n := 0
	vals := make([][]byte, len(t.fields))
	for len(b) >= max(fixed, 1) {
		rest := b
		for i, f := range t.fields {
			l := int(f.length)
			if f.length == varLen {
				if len(rest) < 1 {
					return n, nil
				}
				l = int(rest[0])
				rest = rest[1:]
				if l == 255 {
					if len(rest) < 2 {
						return n, nil
					}
					l = int(binary.BigEndian.Uint16(rest))
					rest = rest[2:]
				}
			}
			if len(rest) < l {
				if variable {
					return n, nil // relleno al final del set
				}
				return n, errors.New("registro truncado")
			}
			vals[i] = rest[:l]
			rest = rest[l:]
		}
		fn(vals)
		n++
		b = rest
	}
	return n, nil
}
