// Package flowpb codifica y decodifica en Protobuf binario los mensajes de
// telemetría del dominio flows (contrato C4,
// packages/protobuf/horus/events/flows/v1/flows.proto): FlowBatch,
// ClientActivitySummary y TrafficSummary.
//
// Se escribe a mano sobre protowire (sin código generado) para que el camino
// caliente del collector y del ingester no reserve memoria por campo y para
// no depender de la generación de código de packages/protobuf (pendiente de
// `make generate`). El test de contrato compara números y nombres de campo
// con el .proto; el formato en el cable es el mismo que produciría protoc-gen-go,
// así que otros módulos pueden usar el código generado.
package flowpb

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

// Content types de las cabeceras NATS (docs/events.md §5.2).
const (
	ContentTypeFlowBatch       = "application/x-protobuf; proto=horus.events.flows.v1.FlowBatch"
	ContentTypeActivitySummary = "application/x-protobuf; proto=horus.events.flows.v1.ClientActivitySummary"
	ContentTypeTrafficSummary  = "application/x-protobuf; proto=horus.events.flows.v1.TrafficSummary"
)

// FlowRecord es el registro canónico normalizado (traffic-model.md §3).
// bytes/packets van sin escalar; el ingester aplica SamplingRate.
type FlowRecord struct {
	ExporterIP          netip.Addr // 1
	ObservationDomainID uint32     // 2
	FlowStart           time.Time  // 3
	TS                  time.Time  // 4 (fin del flujo)
	SrcIP               netip.Addr // 5
	DstIP               netip.Addr // 6
	SrcPort             uint16     // 7
	DstPort             uint16     // 8
	Protocol            uint8      // 9
	TCPFlags            uint8      // 10
	ICMPTypeCode        uint16     // 11
	Bytes               uint64     // 12
	Packets             uint64     // 13
	InputIfIndex        uint32     // 14
	OutputIfIndex       uint32     // 15
	FlowDirection       string     // 16 ingress | egress | unknown
	SrcAS               uint32     // 17
	DstAS               uint32     // 18
	NextHop             netip.Addr // 19 (opcional: inválida = ausente)
	VlanID              uint32     // 20
	PostNATSrcIP        netip.Addr // 21 (opcional)
	PostNATSrcPort      uint16     // 22 (opcional, presente si HasPostNATSrcPort)
	HasPostNATSrcPort   bool
	SamplingRate        uint32 // 23 (opcional: 0 = ausente)
	FlowSource          string // 24 netflow_v5 | netflow_v9 | ipfix
	BatchID             string // 25
	PostNATDstIP        netip.Addr // 26 (opcional)
	PostNATDstPort      uint16     // 27 (opcional, presente si HasPostNATDstPort)
	HasPostNATDstPort   bool
}

// FlowBatch es un lote de un router (= un tenant).
type FlowBatch struct {
	BatchID      string     // 1
	CollectorID  string     // 2
	TenantID     string     // 3
	RouterID     string     // 4
	ExporterIP   netip.Addr // 5
	SamplingRate uint32     // 6
	ReceivedFrom time.Time  // 7
	ReceivedTo   time.Time  // 8
	Records      []FlowRecord
}

// ActiveClient es la actividad horaria de una clave de cliente.
type ActiveClient struct {
	Address  string    // 1
	LastSeen time.Time // 2
	BytesEst uint64    // 3
	Flows    uint32    // 4
}

// ClientActivitySummary es el resumen horario de un realm.
type ClientActivitySummary struct {
	BatchID string    // 1
	RealmID string    // 2
	Hour    time.Time // 3
	Part    int32     // 4
	Parts   int32     // 5
	Clients []ActiveClient
}

// TrafficSummary es el resumen de 10 s de un nodo (SiteID vacío = ISP).
type TrafficSummary struct {
	BatchID         string    // 1
	SiteID          string    // 2 (opcional)
	WindowFrom      time.Time // 3
	WindowTo        time.Time // 4
	DownBps         float64   // 5
	UpBps           float64   // 6
	FlowsPerSecond  float64   // 7
	ActiveCustomers int32     // 8
	Partial         bool      // 9
}

// ---------------------------------------------------------------- codificación

func appendString(b []byte, num protowire.Number, s string) []byte {
	if s == "" {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendString(b, s)
}

// appendOptString codifica un campo optional aunque esté vacío.
func appendOptString(b []byte, num protowire.Number, s string) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendString(b, s)
}

func appendAddr(b []byte, num protowire.Number, a netip.Addr) []byte {
	if !a.IsValid() {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, a.Unmap().AppendTo(nil))
}

func appendVarint(b []byte, num protowire.Number, v uint64) []byte {
	if v == 0 {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func appendOptVarint(b []byte, num protowire.Number, v uint64) []byte {
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func appendBool(b []byte, num protowire.Number, v bool) []byte {
	if !v {
		return b
	}
	return appendVarint(b, num, 1)
}

func appendDouble(b []byte, num protowire.Number, v float64) []byte {
	if v == 0 {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.Fixed64Type)
	return protowire.AppendFixed64(b, math.Float64bits(v))
}

// appendTimestamp codifica google.protobuf.Timestamp (cero = ausente).
func appendTimestamp(b []byte, num protowire.Number, t time.Time) []byte {
	if t.IsZero() {
		return b
	}
	var m []byte
	if s := t.Unix(); s != 0 {
		m = protowire.AppendTag(m, 1, protowire.VarintType)
		m = protowire.AppendVarint(m, uint64(s)) //nolint:gosec // int64 en complemento a dos, como protobuf
	}
	if n := t.Nanosecond(); n != 0 {
		m = protowire.AppendTag(m, 2, protowire.VarintType)
		m = protowire.AppendVarint(m, uint64(n))
	}
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, m)
}

func appendMessage(b []byte, num protowire.Number, m []byte) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, m)
}

// AppendRecord codifica r (sin la etiqueta del campo contenedor).
func AppendRecord(b []byte, r *FlowRecord) []byte {
	b = appendAddr(b, 1, r.ExporterIP)
	b = appendVarint(b, 2, uint64(r.ObservationDomainID))
	b = appendTimestamp(b, 3, r.FlowStart)
	b = appendTimestamp(b, 4, r.TS)
	b = appendAddr(b, 5, r.SrcIP)
	b = appendAddr(b, 6, r.DstIP)
	b = appendVarint(b, 7, uint64(r.SrcPort))
	b = appendVarint(b, 8, uint64(r.DstPort))
	b = appendVarint(b, 9, uint64(r.Protocol))
	b = appendVarint(b, 10, uint64(r.TCPFlags))
	b = appendVarint(b, 11, uint64(r.ICMPTypeCode))
	b = appendVarint(b, 12, r.Bytes)
	b = appendVarint(b, 13, r.Packets)
	b = appendVarint(b, 14, uint64(r.InputIfIndex))
	b = appendVarint(b, 15, uint64(r.OutputIfIndex))
	b = appendString(b, 16, r.FlowDirection)
	b = appendVarint(b, 17, uint64(r.SrcAS))
	b = appendVarint(b, 18, uint64(r.DstAS))
	b = appendAddr(b, 19, r.NextHop)
	b = appendVarint(b, 20, uint64(r.VlanID))
	b = appendAddr(b, 21, r.PostNATSrcIP)
	if r.HasPostNATSrcPort {
		b = appendOptVarint(b, 22, uint64(r.PostNATSrcPort))
	}
	if r.SamplingRate != 0 {
		b = appendOptVarint(b, 23, uint64(r.SamplingRate))
	}
	b = appendString(b, 24, r.FlowSource)
	b = appendString(b, 25, r.BatchID)
	b = appendAddr(b, 26, r.PostNATDstIP)
	if r.HasPostNATDstPort {
		b = appendOptVarint(b, 27, uint64(r.PostNATDstPort))
	}
	return b
}

// Marshal codifica el lote.
func (fb *FlowBatch) Marshal() []byte {
	b := make([]byte, 0, 128+len(fb.Records)*96)
	b = appendString(b, 1, fb.BatchID)
	b = appendString(b, 2, fb.CollectorID)
	b = appendString(b, 3, fb.TenantID)
	b = appendString(b, 4, fb.RouterID)
	b = appendAddr(b, 5, fb.ExporterIP)
	b = appendVarint(b, 6, uint64(fb.SamplingRate))
	b = appendTimestamp(b, 7, fb.ReceivedFrom)
	b = appendTimestamp(b, 8, fb.ReceivedTo)
	var rec []byte
	for i := range fb.Records {
		rec = AppendRecord(rec[:0], &fb.Records[i])
		b = appendMessage(b, 9, rec)
	}
	return b
}

// RecordSize devuelve el tamaño codificado de r dentro de un lote (con etiqueta).
func RecordSize(r *FlowRecord) int {
	n := len(AppendRecord(nil, r))
	return protowire.SizeTag(9) + protowire.SizeBytes(n)
}

// Marshal codifica el resumen.
func (s *ClientActivitySummary) Marshal() []byte {
	var b []byte
	b = appendString(b, 1, s.BatchID)
	b = appendString(b, 2, s.RealmID)
	b = appendTimestamp(b, 3, s.Hour)
	b = appendVarint(b, 4, uint64(uint32(s.Part)))  //nolint:gosec // int32 de protobuf
	b = appendVarint(b, 5, uint64(uint32(s.Parts))) //nolint:gosec // int32 de protobuf
	var c []byte
	for i := range s.Clients {
		c = c[:0]
		ac := &s.Clients[i]
		c = appendString(c, 1, ac.Address)
		c = appendTimestamp(c, 2, ac.LastSeen)
		c = appendVarint(c, 3, ac.BytesEst)
		c = appendVarint(c, 4, uint64(ac.Flows))
		b = appendMessage(b, 6, c)
	}
	return b
}

// ActiveClientSize es el tamaño aproximado de un cliente codificado.
func ActiveClientSize(ac *ActiveClient) int {
	return 48 + len(ac.Address)
}

// Marshal codifica el resumen de tráfico.
func (s *TrafficSummary) Marshal() []byte {
	var b []byte
	b = appendString(b, 1, s.BatchID)
	if s.SiteID != "" {
		b = appendOptString(b, 2, s.SiteID)
	}
	b = appendTimestamp(b, 3, s.WindowFrom)
	b = appendTimestamp(b, 4, s.WindowTo)
	b = appendDouble(b, 5, s.DownBps)
	b = appendDouble(b, 6, s.UpBps)
	b = appendDouble(b, 7, s.FlowsPerSecond)
	b = appendVarint(b, 8, uint64(uint32(s.ActiveCustomers))) //nolint:gosec // int32 de protobuf
	b = appendBool(b, 9, s.Partial)
	return b
}

// ---------------------------------------------------------------- decodificación

// ErrMalformed indica un mensaje Protobuf inválido.
var ErrMalformed = errors.New("flowpb: malformed message")

type field struct {
	num protowire.Number
	typ protowire.Type
	v   uint64 // varint / fixed
	b   []byte // bytes
}

// each recorre los campos de b.
func each(b []byte, fn func(f field) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return fmt.Errorf("%w: tag: %v", ErrMalformed, protowire.ParseError(n))
		}
		b = b[n:]
		f := field{num: num, typ: typ}
		switch typ {
		case protowire.VarintType:
			f.v, n = protowire.ConsumeVarint(b)
		case protowire.Fixed64Type:
			f.v, n = protowire.ConsumeFixed64(b)
		case protowire.Fixed32Type:
			var v32 uint32
			v32, n = protowire.ConsumeFixed32(b)
			f.v = uint64(v32)
		case protowire.BytesType:
			f.b, n = protowire.ConsumeBytes(b)
		default:
			n = protowire.ConsumeFieldValue(num, typ, b)
		}
		if n < 0 {
			return fmt.Errorf("%w: field %d: %v", ErrMalformed, num, protowire.ParseError(n))
		}
		b = b[n:]
		if err := fn(f); err != nil {
			return err
		}
	}
	return nil
}

func parseAddr(b []byte) (netip.Addr, error) {
	if len(b) == 0 {
		return netip.Addr{}, nil
	}
	a, err := netip.ParseAddr(string(b))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: ip %q", ErrMalformed, b)
	}
	return a, nil
}

func parseTimestamp(b []byte) (time.Time, error) {
	var sec, nsec int64
	err := each(b, func(f field) error {
		switch f.num {
		case 1:
			sec = int64(f.v) //nolint:gosec // int64 en complemento a dos
		case 2:
			nsec = int64(f.v) //nolint:gosec // nanos < 1e9
		}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, nsec).UTC(), nil
}

// UnmarshalRecord decodifica un FlowRecord.
func UnmarshalRecord(b []byte, r *FlowRecord) error {
	*r = FlowRecord{}
	return each(b, func(f field) error {
		var err error
		switch f.num {
		case 1:
			r.ExporterIP, err = parseAddr(f.b)
		case 2:
			r.ObservationDomainID = uint32(f.v) //nolint:gosec // uint32 de protobuf
		case 3:
			r.FlowStart, err = parseTimestamp(f.b)
		case 4:
			r.TS, err = parseTimestamp(f.b)
		case 5:
			r.SrcIP, err = parseAddr(f.b)
		case 6:
			r.DstIP, err = parseAddr(f.b)
		case 7:
			r.SrcPort = uint16(f.v) //nolint:gosec // puerto
		case 8:
			r.DstPort = uint16(f.v) //nolint:gosec // puerto
		case 9:
			r.Protocol = uint8(f.v) //nolint:gosec // protocolo IP
		case 10:
			r.TCPFlags = uint8(f.v) //nolint:gosec // flags
		case 11:
			r.ICMPTypeCode = uint16(f.v) //nolint:gosec // tipo/código
		case 12:
			r.Bytes = f.v
		case 13:
			r.Packets = f.v
		case 14:
			r.InputIfIndex = uint32(f.v) //nolint:gosec // ifIndex
		case 15:
			r.OutputIfIndex = uint32(f.v) //nolint:gosec // ifIndex
		case 16:
			r.FlowDirection = string(f.b)
		case 17:
			r.SrcAS = uint32(f.v) //nolint:gosec // ASN
		case 18:
			r.DstAS = uint32(f.v) //nolint:gosec // ASN
		case 19:
			r.NextHop, err = parseAddr(f.b)
		case 20:
			r.VlanID = uint32(f.v) //nolint:gosec // VLAN
		case 21:
			r.PostNATSrcIP, err = parseAddr(f.b)
		case 22:
			r.PostNATSrcPort, r.HasPostNATSrcPort = uint16(f.v), true //nolint:gosec // puerto
		case 23:
			r.SamplingRate = uint32(f.v) //nolint:gosec // tasa
		case 24:
			r.FlowSource = string(f.b)
		case 25:
			r.BatchID = string(f.b)
		case 26:
			r.PostNATDstIP, err = parseAddr(f.b)
		case 27:
			r.PostNATDstPort, r.HasPostNATDstPort = uint16(f.v), true //nolint:gosec // puerto
		}
		return err
	})
}

// UnmarshalFlowBatch decodifica un lote.
func UnmarshalFlowBatch(b []byte) (*FlowBatch, error) {
	fb := &FlowBatch{}
	err := each(b, func(f field) error {
		var err error
		switch f.num {
		case 1:
			fb.BatchID = string(f.b)
		case 2:
			fb.CollectorID = string(f.b)
		case 3:
			fb.TenantID = string(f.b)
		case 4:
			fb.RouterID = string(f.b)
		case 5:
			fb.ExporterIP, err = parseAddr(f.b)
		case 6:
			fb.SamplingRate = uint32(f.v) //nolint:gosec // tasa
		case 7:
			fb.ReceivedFrom, err = parseTimestamp(f.b)
		case 8:
			fb.ReceivedTo, err = parseTimestamp(f.b)
		case 9:
			fb.Records = append(fb.Records, FlowRecord{})
			err = UnmarshalRecord(f.b, &fb.Records[len(fb.Records)-1])
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return fb, nil
}

// UnmarshalClientActivitySummary decodifica un resumen horario.
func UnmarshalClientActivitySummary(b []byte) (*ClientActivitySummary, error) {
	s := &ClientActivitySummary{}
	err := each(b, func(f field) error {
		var err error
		switch f.num {
		case 1:
			s.BatchID = string(f.b)
		case 2:
			s.RealmID = string(f.b)
		case 3:
			s.Hour, err = parseTimestamp(f.b)
		case 4:
			s.Part = int32(f.v) //nolint:gosec // int32 de protobuf
		case 5:
			s.Parts = int32(f.v) //nolint:gosec // int32 de protobuf
		case 6:
			var ac ActiveClient
			err = each(f.b, func(g field) error {
				var err error
				switch g.num {
				case 1:
					ac.Address = string(g.b)
				case 2:
					ac.LastSeen, err = parseTimestamp(g.b)
				case 3:
					ac.BytesEst = g.v
				case 4:
					ac.Flows = uint32(g.v) //nolint:gosec // contador
				}
				return err
			})
			s.Clients = append(s.Clients, ac)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// UnmarshalTrafficSummary decodifica un resumen de tráfico.
func UnmarshalTrafficSummary(b []byte) (*TrafficSummary, error) {
	s := &TrafficSummary{}
	err := each(b, func(f field) error {
		var err error
		switch f.num {
		case 1:
			s.BatchID = string(f.b)
		case 2:
			s.SiteID = string(f.b)
		case 3:
			s.WindowFrom, err = parseTimestamp(f.b)
		case 4:
			s.WindowTo, err = parseTimestamp(f.b)
		case 5:
			s.DownBps = math.Float64frombits(f.v)
		case 6:
			s.UpBps = math.Float64frombits(f.v)
		case 7:
			s.FlowsPerSecond = math.Float64frombits(f.v)
		case 8:
			s.ActiveCustomers = int32(f.v) //nolint:gosec // int32 de protobuf
		case 9:
			s.Partial = f.v != 0
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}
