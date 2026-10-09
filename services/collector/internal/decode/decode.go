// Package decode decodifica datagramas IPFIX (RFC 7011) y NetFlow v9
// (RFC 3954) leyendo las plantillas que anuncia cada exportador: no asume
// orden ni contenido de campos (docs/vendors/mikrotik.md §2,
// docs/traffic-model.md §3). Es independiente del codificador de flowsim y
// del decodificador del verificador (goflow2), así el test dorado compara
// tres implementaciones.
//
// Los conjuntos de datos que llegan antes que su plantilla se retienen un
// tiempo y un volumen acotados y se decodifican al llegar la plantilla, o
// se descartan y se cuentan (I1-03 criterio 3). Nada aquí puede entrar en
// pánico con datos arbitrarios (fuzzing).
package decode

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

// Versiones soportadas.
const (
	VersionV9    = 9
	VersionIPFIX = 10
)

// Elementos de información que se interpretan (IANA IPFIX).
const (
	ieOctetDeltaCount         = 1
	iePacketDeltaCount        = 2
	ieProtocol                = 4
	ieTCPFlags                = 6
	ieSrcPort                 = 7
	ieSrcIPv4                 = 8
	ieIngressIf               = 10
	ieDstPort                 = 11
	ieDstIPv4                 = 12
	ieEgressIf                = 14
	ieNextHopV4               = 15
	ieSrcAS                   = 16
	ieDstAS                   = 17
	ieFlowEndSysUpTime        = 21
	ieFlowStartSysUpTime      = 22
	ieSrcIPv6                 = 27
	ieDstIPv6                 = 28
	ieICMPTypeCodeV4          = 32
	ieSamplingInterval        = 34
	ieVlanID                  = 58
	ieFlowDirection           = 61
	ieNextHopV6               = 62
	ieICMPTypeCodeV6          = 139
	ieFlowStartSeconds        = 150
	ieFlowEndSeconds          = 151
	ieFlowStartMilliseconds   = 152
	ieFlowEndMilliseconds     = 153
	ieSysInitTimeMs           = 160
	ieICMPTypeV4              = 176
	ieICMPCodeV4              = 177
	ieICMPTypeV6              = 178
	ieICMPCodeV6              = 179
	iePostNATSrcIPv4          = 225
	iePostNATDstIPv4          = 226
	iePostNAPTSrcPort         = 227
	iePostNAPTDstPort         = 228
	ieSamplingPacketInterval  = 305
	ieSamplingPacketSpace     = 306
	varLen                    = 0xffff
	maxFieldsPerTemplate      = 512
	defaultPendingTTL         = 30 * time.Second
	defaultPendingMaxSets     = 256
)

// Errores de datagrama (el llamador los cuenta como malformados).
var (
	ErrShort       = errors.New("decode: datagram too short")
	ErrVersion     = errors.New("decode: unsupported version")
	ErrMalformed   = errors.New("decode: malformed datagram")
	ErrUnsupported = errors.New("decode: unsupported field")
)

// Field es un campo de plantilla.
type Field struct {
	Type       uint16
	Length     uint16
	Enterprise uint32
}

// Template es una plantilla de datos u opciones.
type Template struct {
	ID      uint16
	Fields  []Field
	Scope   int // nº de campos de ámbito (plantillas de opciones)
	Options bool
	minLen  int
}

func (t *Template) computeMin() {
	t.minLen = 0
	for _, f := range t.Fields {
		if f.Length == varLen {
			t.minLen++
		} else {
			t.minLen += int(f.Length)
		}
	}
}

// Header es la cabecera del datagrama.
type Header struct {
	Version    uint16
	Sequence   uint32
	ODID       uint32 // Observation Domain ID (IPFIX) o Source ID (v9)
	ExportTime time.Time
	SysUptime  uint32 // solo v9 (ms)
}

// Result es lo decodificado de un datagrama.
type Result struct {
	Header Header
	// Records son los flujos decodificados (incluidos los de conjuntos
	// retenidos que se han podido decodificar al llegar su plantilla).
	Records []flowpb.FlowRecord
	// DataRecords son los registros de datos de este datagrama (para la
	// secuencia IPFIX, que cuenta registros de datos).
	DataRecords int
	// Templates anunciadas en el datagrama (datos y opciones).
	Templates int
	// Pending son conjuntos de datos retenidos a la espera de plantilla.
	Pending int
	// DroppedNoTemplate son conjuntos descartados (caducados o sin hueco).
	DroppedNoTemplate int
	// OptionsRecords son registros de opciones (p. ej. muestreo).
	OptionsRecords int
}

type tkey struct {
	src     string
	odid    uint32
	version uint16
	id      uint16
}

type pendingSet struct {
	key     tkey
	body    []byte
	hdr     Header
	arrived time.Time
}

// Options configura el decodificador.
type Options struct {
	// PendingTTL es cuánto se retiene un conjunto sin plantilla (30 s).
	PendingTTL time.Duration
	// PendingMaxSets acota los conjuntos retenidos por exportador (256).
	PendingMaxSets int
	// Now es el reloj (tests).
	Now func() time.Time
}

// Decoder mantiene las plantillas por exportador. No es seguro para uso
// concurrente: el collector usa un decodificador por trabajador y reparte
// los datagramas por exportador.
type Decoder struct {
	opts      Options
	templates map[tkey]*Template
	pending   map[string][]pendingSet
	sampling  map[string]uint32
}

// New crea un decodificador.
func New(o Options) *Decoder {
	if o.PendingTTL <= 0 {
		o.PendingTTL = defaultPendingTTL
	}
	if o.PendingMaxSets <= 0 {
		o.PendingMaxSets = defaultPendingMaxSets
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Decoder{opts: o, templates: map[tkey]*Template{}, pending: map[string][]pendingSet{}, sampling: map[string]uint32{}}
}

// Sampling devuelve la tasa de muestreo anunciada por Options Template (0 si ninguna).
func (d *Decoder) Sampling(src string) uint32 { return d.sampling[src] }

// Forget olvida las plantillas y conjuntos retenidos de un exportador.
func (d *Decoder) Forget(src string) {
	for k := range d.templates {
		if k.src == src {
			delete(d.templates, k)
		}
	}
	delete(d.pending, src)
	delete(d.sampling, src)
}

// Decode decodifica un datagrama del exportador src (su IP de origen).
func (d *Decoder) Decode(src string, p []byte) (Result, error) {
	var res Result
	if len(p) < 4 {
		return res, ErrShort
	}
	ver := binary.BigEndian.Uint16(p)
	var body []byte
	switch ver {
	case VersionIPFIX:
		if len(p) < 16 {
			return res, ErrShort
		}
		l := int(binary.BigEndian.Uint16(p[2:]))
		if l < 16 || l > len(p) {
			return res, fmt.Errorf("%w: ipfix length %d", ErrMalformed, l)
		}
		res.Header = Header{Version: ver,
			ExportTime: time.Unix(int64(binary.BigEndian.Uint32(p[4:])), 0).UTC(),
			Sequence:   binary.BigEndian.Uint32(p[8:]), ODID: binary.BigEndian.Uint32(p[12:])}
		body = p[16:l]
	case VersionV9:
		if len(p) < 20 {
			return res, ErrShort
		}
		res.Header = Header{Version: ver, SysUptime: binary.BigEndian.Uint32(p[4:]),
			ExportTime: time.Unix(int64(binary.BigEndian.Uint32(p[8:])), 0).UTC(),
			Sequence:   binary.BigEndian.Uint32(p[12:]), ODID: binary.BigEndian.Uint32(p[16:])}
		body = p[20:]
	default:
		return res, fmt.Errorf("%w: %d", ErrVersion, ver)
	}
	d.expire(src, &res)
	for len(body) > 0 {
		if len(body) < 4 {
			if allZero(body) { // relleno al final del datagrama v9
				break
			}
			return res, fmt.Errorf("%w: trailing %d bytes", ErrMalformed, len(body))
		}
		id, l := binary.BigEndian.Uint16(body), int(binary.BigEndian.Uint16(body[2:]))
		if l < 4 || l > len(body) {
			return res, fmt.Errorf("%w: set %d length %d", ErrMalformed, id, l)
		}
		set := body[4:l]
		body = body[l:]
		var err error
		switch {
		case (ver == VersionV9 && id == 0) || (ver == VersionIPFIX && id == 2):
			err = d.templateSet(src, &res, set, false)
		case (ver == VersionV9 && id == 1) || (ver == VersionIPFIX && id == 3):
			err = d.templateSet(src, &res, set, true)
		case id >= 256:
			k := tkey{src: src, odid: res.Header.ODID, version: ver, id: id}
			t := d.templates[k]
			if t == nil {
				d.hold(src, pendingSet{key: k, body: append([]byte(nil), set...), hdr: res.Header, arrived: d.opts.Now()}, &res)
				continue
			}
			n, derr := d.dataSet(src, t, set, res.Header, &res.Records)
			if t.Options {
				res.OptionsRecords += n
			} else {
				res.DataRecords += n
			}
			err = derr
		default:
			err = fmt.Errorf("%w: reserved set id %d", ErrMalformed, id)
		}
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

func (d *Decoder) hold(src string, ps pendingSet, res *Result) {
	q := d.pending[src]
	if len(q) >= d.opts.PendingMaxSets {
		res.DroppedNoTemplate++
		return
	}
	d.pending[src] = append(q, ps)
	res.Pending++
}

func (d *Decoder) expire(src string, res *Result) {
	q := d.pending[src]
	if len(q) == 0 {
		return
	}
	now := d.opts.Now()
	kept := q[:0]
	for _, ps := range q {
		if now.Sub(ps.arrived) > d.opts.PendingTTL {
			res.DroppedNoTemplate++
			continue
		}
		kept = append(kept, ps)
	}
	if len(kept) == 0 {
		delete(d.pending, src)
		return
	}
	d.pending[src] = kept
}

// release decodifica los conjuntos retenidos que esperaban la plantilla k.
func (d *Decoder) release(k tkey, t *Template, res *Result) {
	q := d.pending[k.src]
	if len(q) == 0 {
		return
	}
	kept := q[:0]
	for _, ps := range q {
		if ps.key != k {
			kept = append(kept, ps)
			continue
		}
		n, _ := d.dataSet(k.src, t, ps.body, ps.hdr, &res.Records)
		if !t.Options {
			res.DataRecords += n
		}
	}
	if len(kept) == 0 {
		delete(d.pending, k.src)
	} else {
		d.pending[k.src] = kept
	}
}

func (d *Decoder) templateSet(src string, res *Result, b []byte, options bool) error {
	ver, odid := res.Header.Version, res.Header.ODID
	for len(b) >= 4 {
		var t Template
		t.Options = options
		t.ID = binary.BigEndian.Uint16(b)
		var nfields int
		switch {
		case !options:
			nfields = int(binary.BigEndian.Uint16(b[2:]))
			b = b[4:]
		case ver == VersionIPFIX:
			if len(b) < 6 {
				return fmt.Errorf("%w: options template header", ErrMalformed)
			}
			nfields = int(binary.BigEndian.Uint16(b[2:]))
			t.Scope = int(binary.BigEndian.Uint16(b[4:]))
			b = b[6:]
		default: // v9: longitudes en bytes de ámbito y opciones
			if len(b) < 6 {
				return fmt.Errorf("%w: options template header", ErrMalformed)
			}
			sl, ol := int(binary.BigEndian.Uint16(b[2:])), int(binary.BigEndian.Uint16(b[4:]))
			if sl%4 != 0 || ol%4 != 0 {
				return fmt.Errorf("%w: v9 options template lengths", ErrMalformed)
			}
			t.Scope, nfields = sl/4, (sl+ol)/4
			b = b[6:]
		}
		if t.ID < 256 {
			if allZero(b) && t.ID == 0 { // relleno
				return nil
			}
			return fmt.Errorf("%w: template id %d", ErrMalformed, t.ID)
		}
		if nfields > maxFieldsPerTemplate {
			return fmt.Errorf("%w: %d fields", ErrMalformed, nfields)
		}
		k := tkey{src: src, odid: odid, version: ver, id: t.ID}
		if nfields == 0 && ver == VersionIPFIX { // retirada de plantilla (RFC 7011 §8.1)
			delete(d.templates, k)
			res.Templates++
			continue
		}
		t.Fields = make([]Field, 0, nfields)
		for i := 0; i < nfields; i++ {
			if len(b) < 4 {
				return fmt.Errorf("%w: template %d truncated", ErrMalformed, t.ID)
			}
			f := Field{Type: binary.BigEndian.Uint16(b), Length: binary.BigEndian.Uint16(b[2:])}
			b = b[4:]
			if ver == VersionIPFIX && f.Type&0x8000 != 0 {
				if len(b) < 4 {
					return fmt.Errorf("%w: template %d truncated", ErrMalformed, t.ID)
				}
				f.Type &= 0x7fff
				f.Enterprise = binary.BigEndian.Uint32(b)
				b = b[4:]
			}
			if ver == VersionV9 && f.Length == varLen {
				return fmt.Errorf("%w: v9 variable length", ErrMalformed)
			}
			t.Fields = append(t.Fields, f)
		}
		t.computeMin()
		if t.minLen == 0 {
			return fmt.Errorf("%w: template %d has zero length", ErrMalformed, t.ID)
		}
		tt := t
		d.templates[k] = &tt
		res.Templates++
		d.release(k, &tt, res)
		if options && ver == VersionV9 {
			return nil // v9: el resto del conjunto de opciones es relleno
		}
	}
	return nil
}

// dataSet decodifica los registros de un conjunto de datos y devuelve cuántos.
func (d *Decoder) dataSet(src string, t *Template, b []byte, h Header, out *[]flowpb.FlowRecord) (int, error) {
	n := 0
	for len(b) >= t.minLen {
		rec, rest, ok := d.record(src, t, b, h)
		if !ok {
			if allZero(b) {
				return n, nil
			}
			return n, fmt.Errorf("%w: record of template %d", ErrMalformed, t.ID)
		}
		b = rest
		n++
		if !t.Options {
			*out = append(*out, rec)
		}
	}
	return n, nil
}

func beUint(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

func addr(b []byte) netip.Addr {
	switch len(b) {
	case 4:
		return netip.AddrFrom4([4]byte(b))
	case 16:
		return netip.AddrFrom16([16]byte(b)).Unmap()
	}
	return netip.Addr{}
}

//nolint:gocyclo // un caso por elemento de información
func (d *Decoder) record(src string, t *Template, b []byte, h Header) (flowpb.FlowRecord, []byte, bool) {
	var r flowpb.FlowRecord
	var startUp, endUp, startAbs, endAbs, startSec, endSec, sysInit uint64
	var hasStartUp, hasEndUp, hasStartAbs, hasEndAbs, hasStartSec, hasEndSec, hasSysInit bool
	var icmpType, icmpCode uint16
	var hasICMPSplit bool
	var sampling uint64
	for _, f := range t.Fields {
		l := int(f.Length)
		if f.Length == varLen {
			if len(b) < 1 {
				return r, nil, false
			}
			l, b = int(b[0]), b[1:]
			if l == 255 {
				if len(b) < 2 {
					return r, nil, false
				}
				l, b = int(binary.BigEndian.Uint16(b)), b[2:]
			}
		}
		if l > len(b) {
			return r, nil, false
		}
		v := b[:l]
		b = b[l:]
		if f.Enterprise != 0 {
			continue
		}
		u := uint64(0)
		if l <= 8 {
			u = beUint(v)
		}
		switch f.Type {
		case ieOctetDeltaCount:
			r.Bytes = u
		case iePacketDeltaCount:
			r.Packets = u
		case ieProtocol:
			r.Protocol = uint8(u) //nolint:gosec // 1 byte
		case ieTCPFlags:
			r.TCPFlags = uint8(u) //nolint:gosec // los 8 bits bajos (IPFIX lo define de 16)
		case ieSrcPort:
			r.SrcPort = uint16(u) //nolint:gosec // puerto
		case ieDstPort:
			r.DstPort = uint16(u) //nolint:gosec // puerto
		case ieSrcIPv4, ieSrcIPv6:
			r.SrcIP = addr(v)
		case ieDstIPv4, ieDstIPv6:
			r.DstIP = addr(v)
		case ieNextHopV4, ieNextHopV6:
			if a := addr(v); a.IsValid() && !a.IsUnspecified() {
				r.NextHop = a
			}
		case ieIngressIf:
			r.InputIfIndex = uint32(u) //nolint:gosec // ifIndex
		case ieEgressIf:
			r.OutputIfIndex = uint32(u) //nolint:gosec // ifIndex
		case ieSrcAS:
			r.SrcAS = uint32(u) //nolint:gosec // ASN
		case ieDstAS:
			r.DstAS = uint32(u) //nolint:gosec // ASN
		case ieVlanID:
			r.VlanID = uint32(u) //nolint:gosec // VLAN
		case ieFlowDirection:
			if u == 0 {
				r.FlowDirection = "ingress"
			} else {
				r.FlowDirection = "egress"
			}
		case ieICMPTypeCodeV4, ieICMPTypeCodeV6:
			r.ICMPTypeCode = uint16(u) //nolint:gosec // tipo<<8|código
		case ieICMPTypeV4, ieICMPTypeV6:
			icmpType, hasICMPSplit = uint16(u&0xff), true
		case ieICMPCodeV4, ieICMPCodeV6:
			icmpCode, hasICMPSplit = uint16(u&0xff), true
		case ieFlowStartSysUpTime:
			startUp, hasStartUp = u, true
		case ieFlowEndSysUpTime:
			endUp, hasEndUp = u, true
		case ieFlowStartMilliseconds:
			startAbs, hasStartAbs = u, true
		case ieFlowEndMilliseconds:
			endAbs, hasEndAbs = u, true
		case ieFlowStartSeconds:
			startSec, hasStartSec = u, true
		case ieFlowEndSeconds:
			endSec, hasEndSec = u, true
		case ieSysInitTimeMs:
			sysInit, hasSysInit = u, true
		case iePostNATSrcIPv4:
			if a := addr(v); a.IsValid() && !a.IsUnspecified() {
				r.PostNATSrcIP = a
			}
		case iePostNATDstIPv4:
			if a := addr(v); a.IsValid() && !a.IsUnspecified() {
				r.PostNATDstIP = a
			}
		case iePostNAPTSrcPort:
			r.PostNATSrcPort, r.HasPostNATSrcPort = uint16(u), true //nolint:gosec // puerto
		case iePostNAPTDstPort:
			r.PostNATDstPort, r.HasPostNATDstPort = uint16(u), true //nolint:gosec // puerto
		case ieSamplingInterval, ieSamplingPacketInterval:
			sampling = u
		case ieSamplingPacketSpace:
			if u > 0 && sampling > 0 {
				sampling = (sampling + u) / sampling // intervalo + espacio sobre intervalo
			}
		}
	}
	if hasICMPSplit && r.ICMPTypeCode == 0 {
		r.ICMPTypeCode = icmpType<<8 | icmpCode
	}
	if t.Options {
		if sampling > 0 && sampling <= 1<<31 {
			d.sampling[src] = uint32(sampling) //nolint:gosec // acotado arriba
		}
		return r, b, true
	}
	if sampling > 0 && sampling <= 1<<31 {
		r.SamplingRate = uint32(sampling) //nolint:gosec // acotado arriba
	}
	// Tiempos absolutos (RFC 7011): ms > s > sysUpTime + arranque.
	var boot int64
	switch {
	case hasSysInit:
		boot = int64(sysInit) //nolint:gosec // ms Unix
	case h.Version == VersionV9:
		boot = h.ExportTime.UnixMilli() - int64(h.SysUptime)
	}
	switch {
	case hasStartAbs:
		r.FlowStart = time.UnixMilli(int64(startAbs)).UTC() //nolint:gosec // ms Unix
	case hasStartSec:
		r.FlowStart = time.Unix(int64(startSec), 0).UTC() //nolint:gosec // s Unix
	case hasStartUp && boot != 0:
		r.FlowStart = time.UnixMilli(boot + int64(startUp)).UTC() //nolint:gosec // uptime 32 bits
	}
	switch {
	case hasEndAbs:
		r.TS = time.UnixMilli(int64(endAbs)).UTC() //nolint:gosec // ms Unix
	case hasEndSec:
		r.TS = time.Unix(int64(endSec), 0).UTC() //nolint:gosec // s Unix
	case hasEndUp && boot != 0:
		r.TS = time.UnixMilli(boot + int64(endUp)).UTC() //nolint:gosec // uptime 32 bits
	}
	if r.TS.IsZero() {
		r.TS = h.ExportTime
	}
	if r.FlowStart.IsZero() {
		r.FlowStart = r.TS
	}
	if h.Version == VersionV9 {
		r.FlowSource = "netflow_v9"
	} else {
		r.FlowSource = "ipfix"
	}
	r.ObservationDomainID = h.ODID
	return r, b, true
}
