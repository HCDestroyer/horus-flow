// Package export codifica registros de flujo en datagramas NetFlow v9 o IPFIX
// con el comportamiento de exportación de RouterOS 7: plantillas reenviadas
// cada N paquetes o cada T segundos (v9-template-refresh / v9-template-timeout),
// números de secuencia por dominio de observación y datagramas limitados al
// tamaño máximo configurado (MTU del túnel WireGuard).
package export

import (
	"encoding/binary"
	"hash/fnv"
	"net/netip"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
)

// Config parametriza un exportador.
type Config struct {
	Protocol          flow.Protocol
	ObservationDomain uint32
	// Boot es el instante de arranque del router (sys-init-time / base de
	// sysUptime).
	Boot time.Time
	// TemplateRefresh es el nº de paquetes entre reenvíos de plantilla
	// (RouterOS v9-template-refresh, defecto 20).
	TemplateRefresh int
	// TemplateTimeout reenvía la plantilla por tiempo (v9-template-timeout).
	TemplateTimeout time.Duration
	// MaxDatagram es el tamaño máximo de la carga UDP en bytes.
	MaxDatagram int
	Templates   flow.TemplateOptions
}

// Stats acumula lo emitido por un exportador.
type Stats struct {
	Datagrams     uint64
	TemplateSends uint64 // datagramas que llevan el set de plantillas
	DataRecords   uint64
}

// Exporter mantiene el estado de un exportador (un router / dominio).
type Exporter struct {
	cfg          Config
	v4, v6       flow.Template
	routerOS     bool // perfil routeros7: post-NAT copia los valores previos
	seq          uint32
	sinceTmpl    int
	lastTmpl     time.Time
	sentTemplate bool
	stats        Stats
}

const (
	v9HeaderLen    = 20
	ipfixHeaderLen = 16
	setHeaderLen   = 4
	minDatagram    = 512
)

// New crea un exportador. Aplica valores por defecto de RouterOS.
func New(cfg Config) *Exporter {
	if cfg.TemplateRefresh <= 0 {
		cfg.TemplateRefresh = 20
	}
	if cfg.TemplateTimeout <= 0 {
		cfg.TemplateTimeout = time.Minute
	}
	if cfg.MaxDatagram < minDatagram {
		cfg.MaxDatagram = 1392
	}
	v4, v6 := flow.Templates(cfg.Protocol, cfg.Templates)
	return &Exporter{
		cfg: cfg, v4: v4, v6: v6,
		routerOS: cfg.Templates.EffectiveProfile(cfg.Protocol) == flow.ProfileRouterOS7,
	}
}

// Stats devuelve los contadores acumulados.
func (e *Exporter) Stats() Stats { return e.stats }

// TemplatesInUse devuelve las plantillas IPv4 e IPv6 anunciadas.
func (e *Exporter) TemplatesInUse() (v4, v6 flow.Template) { return e.v4, e.v6 }

func (e *Exporter) templateDue(now time.Time) bool {
	return !e.sentTemplate || e.sinceTmpl >= e.cfg.TemplateRefresh ||
		now.Sub(e.lastTmpl) >= e.cfg.TemplateTimeout
}

// Export codifica los registros que expiran en `now` y devuelve los
// datagramas resultantes. Sin registros, emite un datagrama solo de
// plantillas si toca reenviarlas por tiempo (como hace RouterOS).
// Los registros IPv4 van antes que los IPv6 (orden estable).
func (e *Exporter) Export(now time.Time, recs []flow.Record) [][]byte {
	if len(recs) == 0 {
		if e.templateDue(now) {
			b := e.newDatagram(now)
			b.addTemplates()
			return [][]byte{e.finish(b, now)}
		}
		return nil
	}
	ordered := make([]*flow.Record, 0, len(recs))
	for i := range recs {
		if !recs[i].IsV6() {
			ordered = append(ordered, &recs[i])
		}
	}
	for i := range recs {
		if recs[i].IsV6() {
			ordered = append(ordered, &recs[i])
		}
	}

	var out [][]byte
	b := e.newDatagram(now)
	for _, r := range ordered {
		t := &e.v4
		if r.IsV6() {
			t = &e.v6
		}
		need := t.RecordLen()
		if !b.inSet || b.curSet != t.ID {
			need += setHeaderLen + 3 // cabecera de set + posible relleno
		}
		if len(b.buf)+need > e.cfg.MaxDatagram && b.records > 0 {
			out = append(out, e.finish(b, now))
			b = e.newDatagram(now)
		}
		b.addRecord(t, r)
	}
	out = append(out, e.finish(b, now))
	return out
}

type builder struct {
	e         *Exporter
	buf       []byte
	setStart  int
	curSet    uint16
	inSet     bool
	records   int // registros de datos
	tmplRecs  int // registros de plantilla
	hasTmpl   bool
	headerLen int
}

func (e *Exporter) newDatagram(now time.Time) *builder {
	hl := ipfixHeaderLen
	if e.cfg.Protocol == flow.V9 {
		hl = v9HeaderLen
	}
	b := &builder{e: e, buf: make([]byte, hl, e.cfg.MaxDatagram), headerLen: hl}
	if e.templateDue(now) {
		b.addTemplates()
	}
	return b
}

func (b *builder) closeSet() {
	if !b.inSet {
		return
	}
	if b.e.cfg.Protocol == flow.V9 {
		// NetFlow v9 rellena los FlowSets a múltiplos de 4 bytes (RFC 3954 §5.3).
		for (len(b.buf)-b.setStart)%4 != 0 {
			b.buf = append(b.buf, 0)
		}
	}
	binary.BigEndian.PutUint16(b.buf[b.setStart+2:], uint16(len(b.buf)-b.setStart))
	b.curSet, b.inSet = 0, false
}

func (b *builder) openSet(id uint16) {
	b.closeSet()
	b.setStart = len(b.buf)
	b.buf = binary.BigEndian.AppendUint16(b.buf, id)
	b.buf = append(b.buf, 0, 0)
	b.curSet, b.inSet = id, true
}

func (b *builder) addTemplates() {
	if b.hasTmpl {
		return
	}
	setID := uint16(2) // IPFIX Template Set
	if b.e.cfg.Protocol == flow.V9 {
		setID = 0 // NetFlow v9 Template FlowSet
	}
	b.openSet(setID)
	for _, t := range []flow.Template{b.e.v4, b.e.v6} {
		b.buf = binary.BigEndian.AppendUint16(b.buf, t.ID)
		b.buf = binary.BigEndian.AppendUint16(b.buf, uint16(len(t.Fields)))
		for _, f := range t.Fields {
			b.buf = binary.BigEndian.AppendUint16(b.buf, f.ID)
			b.buf = binary.BigEndian.AppendUint16(b.buf, f.Len)
		}
		b.tmplRecs++
	}
	b.closeSet()
	b.hasTmpl = true
}

func (b *builder) addRecord(t *flow.Template, r *flow.Record) {
	if !b.inSet || b.curSet != t.ID {
		b.openSet(t.ID)
	}
	b.buf = encodeRecord(b.buf, t, r, b.e.cfg.Boot, b.e.routerOS)
	b.records++
}

func (e *Exporter) finish(b *builder, now time.Time) []byte {
	b.closeSet()
	buf := b.buf
	nowMs := now.UnixMilli()
	switch e.cfg.Protocol {
	case flow.V9:
		binary.BigEndian.PutUint16(buf[0:], 9)
		binary.BigEndian.PutUint16(buf[2:], uint16(b.records+b.tmplRecs))
		binary.BigEndian.PutUint32(buf[4:], uint32(nowMs-e.cfg.Boot.UnixMilli()))
		binary.BigEndian.PutUint32(buf[8:], uint32(now.Unix()))
		binary.BigEndian.PutUint32(buf[12:], e.seq)
		binary.BigEndian.PutUint32(buf[16:], e.cfg.ObservationDomain)
		e.seq++ // v9: contador de paquetes exportados
	default:
		binary.BigEndian.PutUint16(buf[0:], 10)
		binary.BigEndian.PutUint16(buf[2:], uint16(len(buf)))
		binary.BigEndian.PutUint32(buf[4:], uint32(now.Unix()))
		binary.BigEndian.PutUint32(buf[8:], e.seq)
		binary.BigEndian.PutUint32(buf[12:], e.cfg.ObservationDomain)
		e.seq += uint32(b.records) // IPFIX: registros de datos enviados antes
	}
	if b.hasTmpl {
		e.sentTemplate = true
		e.sinceTmpl = 0
		e.lastTmpl = now
		e.stats.TemplateSends++
	}
	e.sinceTmpl++
	e.stats.Datagrams++
	e.stats.DataRecords += uint64(b.records)
	return buf
}

func appendUint(buf []byte, v uint64, n uint16) []byte {
	switch n {
	case 1:
		return append(buf, byte(v))
	case 2:
		return binary.BigEndian.AppendUint16(buf, uint16(v))
	case 4:
		return binary.BigEndian.AppendUint32(buf, uint32(v))
	default:
		return binary.BigEndian.AppendUint64(buf, v)
	}
}

func appendAddr(buf []byte, a netip.Addr, n uint16) []byte {
	if n == 4 {
		if !a.IsValid() {
			return append(buf, 0, 0, 0, 0)
		}
		b := a.As4()
		return append(buf, b[:]...)
	}
	if !a.IsValid() {
		return append(buf, make([]byte, 16)...)
	}
	b := a.As16()
	return append(buf, b[:]...)
}

// tcpState devuelve número de secuencia, ACK y ventana plausibles y
// deterministas para un registro TCP (RouterOS exporta los del último
// paquete; el simulador no modela la sesión TCP).
func tcpState(r *flow.Record) (seq, ack uint32, win uint16) {
	h := fnv.New64a()
	sb, db := r.SrcIP.As16(), r.DstIP.As16()
	_, _ = h.Write(sb[:])
	_, _ = h.Write(db[:])
	var b [18]byte
	binary.BigEndian.PutUint16(b[0:], r.SrcPort)
	binary.BigEndian.PutUint16(b[2:], r.DstPort)
	binary.BigEndian.PutUint64(b[4:], uint64(r.Start.UnixMilli())) //nolint:gosec // fechas posteriores a 1970
	binary.BigEndian.PutUint64(b[10:], r.Packets)
	_, _ = h.Write(b[:])
	v := h.Sum64()
	seq = uint32(v)
	if r.TCPFlags&flow.ACK != 0 {
		ack = uint32(v>>32) | 1
	}
	windows := [...]uint16{65535, 64240, 63239, 35840, 29200, 15119, 2048, 501}
	win = windows[(v>>13)%uint64(len(windows))]
	if r.TCPFlags&flow.RST != 0 {
		win = 0
	}
	return seq, ack, win
}

func encodeRecord(buf []byte, t *flow.Template, r *flow.Record, boot time.Time, routerOS bool) []byte {
	bootMs := boot.UnixMilli()
	postSrc, postDst := r.PostNATSrc, r.PostNATDst
	postSrcPort, postDstPort := r.PostNATSrcPort, r.PostNATDstPort
	if routerOS {
		// Sin traducción, RouterOS repite los valores previos al NAT.
		if !postSrc.IsValid() {
			postSrc, postSrcPort = r.SrcIP, r.SrcPort
		}
		if !postDst.IsValid() {
			postDst, postDstPort = r.DstIP, r.DstPort
		}
	}
	v6 := r.IsV6()
	ipHdr := uint64(20)
	if v6 {
		ipHdr = 40
	}
	totalLen := uint64(0)
	if r.Packets > 0 {
		totalLen = r.Bytes / r.Packets
	}
	var seq, ack uint32
	var win uint16
	if r.Proto == flow.ProtoTCP {
		seq, ack, win = tcpState(r)
	}
	icmp := r.Proto == flow.ProtoICMP || r.Proto == flow.ProtoICMPv6
	for _, f := range t.Fields {
		switch f.ID {
		case flow.IESysInitTimeMs:
			buf = appendUint(buf, uint64(bootMs), f.Len)
		case flow.IEFlowStartSysUpTime:
			buf = appendUint(buf, uint64(r.Start.UnixMilli()-bootMs), f.Len)
		case flow.IEFlowEndSysUpTime:
			buf = appendUint(buf, uint64(r.End.UnixMilli()-bootMs), f.Len)
		case flow.IEFlowStartMilliseconds:
			buf = appendUint(buf, uint64(r.Start.UnixMilli()), f.Len)
		case flow.IEFlowEndMilliseconds:
			buf = appendUint(buf, uint64(r.End.UnixMilli()), f.Len)
		case flow.IESrcIPv4, flow.IESrcIPv6:
			buf = appendAddr(buf, r.SrcIP, f.Len)
		case flow.IEDstIPv4, flow.IEDstIPv6:
			buf = appendAddr(buf, r.DstIP, f.Len)
		case flow.IENextHopV4, flow.IENextHopV6:
			buf = appendAddr(buf, r.NextHop, f.Len)
		case flow.IEPostNATSrcIPv4:
			buf = appendAddr(buf, postSrc, f.Len)
		case flow.IEPostNATDstIPv4:
			buf = appendAddr(buf, postDst, f.Len)
		case flow.IEPostNAPTSrcPort:
			buf = appendUint(buf, uint64(postSrcPort), f.Len)
		case flow.IEPostNAPTDstPort:
			buf = appendUint(buf, uint64(postDstPort), f.Len)
		case flow.IEIPVersion:
			ver := uint64(4)
			if v6 {
				ver = 6
			}
			buf = appendUint(buf, ver, f.Len)
		case flow.IEIPTTL:
			buf = appendUint(buf, uint64(r.MaxTTL), f.Len)
		case flow.IEIsMulticast:
			mc := uint64(0)
			if r.DstIP.IsMulticast() {
				mc = 1
			}
			buf = appendUint(buf, mc, f.Len)
		case flow.IEIPHeaderLength: // en palabras de 4 bytes
			buf = appendUint(buf, ipHdr/4, f.Len)
		case flow.IEIPTotalLength:
			buf = appendUint(buf, totalLen, f.Len)
		case flow.IEUDPMessageLength:
			ul := uint64(0)
			if r.Proto == flow.ProtoUDP && totalLen > ipHdr {
				ul = totalLen - ipHdr
			}
			buf = appendUint(buf, ul, f.Len)
		case flow.IETCPSeq:
			buf = appendUint(buf, uint64(seq), f.Len)
		case flow.IETCPAck:
			buf = appendUint(buf, uint64(ack), f.Len)
		case flow.IETCPWindow:
			buf = appendUint(buf, uint64(win), f.Len)
		case flow.IEICMPTypeV4, flow.IEICMPTypeV6:
			v := uint64(0)
			if icmp {
				v = uint64(r.ICMPTypeCode >> 8)
			}
			buf = appendUint(buf, v, f.Len)
		case flow.IEICMPCodeV4, flow.IEICMPCodeV6:
			v := uint64(0)
			if icmp {
				v = uint64(r.ICMPTypeCode & 0xff)
			}
			buf = appendUint(buf, v, f.Len)
		case flow.IEPostSrcMAC:
			buf = append(buf, r.PostSrcMAC[:]...)
		case flow.IEPostDstMAC:
			buf = append(buf, r.PostDstMAC[:]...)
		case flow.IESrcPort:
			buf = appendUint(buf, uint64(r.SrcPort), f.Len)
		case flow.IEDstPort:
			buf = appendUint(buf, uint64(r.DstPort), f.Len)
		case flow.IEProtocol:
			buf = appendUint(buf, uint64(r.Proto), f.Len)
		case flow.IETCPFlags:
			buf = appendUint(buf, uint64(r.TCPFlags), f.Len)
		case flow.IEToS:
			buf = appendUint(buf, uint64(r.ToS), f.Len)
		case flow.IEOctetDeltaCount:
			buf = appendUint(buf, r.Bytes, f.Len)
		case flow.IEPacketDeltaCount:
			buf = appendUint(buf, r.Packets, f.Len)
		case flow.IEIngressIf:
			buf = appendUint(buf, uint64(r.InIf), f.Len)
		case flow.IEEgressIf:
			buf = appendUint(buf, uint64(r.OutIf), f.Len)
		case flow.IESrcMaskV4, flow.IESrcMaskV6:
			buf = appendUint(buf, uint64(r.SrcMask), f.Len)
		case flow.IEDstMaskV4, flow.IEDstMaskV6:
			buf = appendUint(buf, uint64(r.DstMask), f.Len)
		case flow.IEFlowLabelV6:
			buf = appendUint(buf, uint64(r.FlowLabel), f.Len)
		case flow.IEICMPTypeCodeV4, flow.IEICMPTypeCodeV6:
			buf = appendUint(buf, uint64(r.ICMPTypeCode), f.Len)
		case flow.IEMinTTL:
			buf = appendUint(buf, uint64(r.MinTTL), f.Len)
		case flow.IEMaxTTL:
			buf = appendUint(buf, uint64(r.MaxTTL), f.Len)
		case flow.IESrcMAC:
			buf = append(buf, r.SrcMAC[:]...)
		case flow.IEDstMAC:
			buf = append(buf, r.DstMAC[:]...)
		default: // AS y cualquier campo sin dato: ceros
			buf = append(buf, make([]byte, f.Len)...)
		}
	}
	return buf
}
