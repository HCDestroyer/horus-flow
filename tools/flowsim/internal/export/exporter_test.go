package export_test

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/export"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/verify"
)

var (
	t0   = time.Date(2026, 1, 5, 18, 0, 0, 0, time.UTC)
	boot = t0.Add(-72 * time.Hour)
)

func sampleV4(i int) flow.Record {
	return flow.Record{
		Start: t0.Add(-30 * time.Second).Add(time.Duration(i) * time.Millisecond), End: t0.Add(-2 * time.Second),
		SrcIP: netip.MustParseAddr("10.20.0.10"), DstIP: netip.MustParseAddr("142.250.1.2"),
		SrcPort: uint16(40000 + i), DstPort: 443, Proto: flow.ProtoTCP, TCPFlags: flow.SYN | flow.ACK | flow.PSH,
		ToS: 0x10, Bytes: 123456, Packets: 100, InIf: 12, OutIf: 1, NextHop: netip.MustParseAddr("203.0.113.1"),
		SrcMask: 24, ICMPTypeCode: 0, MinTTL: 63, MaxTTL: 63,
		SrcMAC: [6]byte{0x50, 0xc7, 0xbf, 1, 2, 3}, DstMAC: [6]byte{0x4c, 0x5e, 0x0c, 4, 5, 6},
	}
}

func sampleV6() flow.Record {
	return flow.Record{
		Start: t0.Add(-10 * time.Second), End: t0.Add(-9 * time.Second),
		SrcIP: netip.MustParseAddr("2a00:1450:4001::5"), DstIP: netip.MustParseAddr("2001:db8:1000:3::1234"),
		SrcPort: 443, DstPort: 51000, Proto: flow.ProtoUDP, Bytes: 9000, Packets: 7, InIf: 1, OutIf: 12,
		DstMask: 64, FlowLabel: 0xabcde, MinTTL: 57, MaxTTL: 57,
	}
}

func decodeAll(t *testing.T, dgrams [][]byte) ([]verify.PacketInfo, []flow.Record) {
	t.Helper()
	d := verify.NewDecoder()
	var infos []verify.PacketInfo
	var recs []flow.Record
	for _, p := range dgrams {
		info, rs, err := d.Decode("x", p)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		infos = append(infos, info)
		recs = append(recs, rs...)
	}
	return infos, recs
}

func TestRoundTrip(t *testing.T) {
	type tc struct {
		name    string
		p       flow.Protocol
		profile flow.Profile
	}
	for _, c := range []tc{{"ipfix", flow.IPFIX, ""}, {"ipfix-legacy", flow.IPFIX, flow.ProfileLegacy}, {"v9", flow.V9, ""}} {
		p := c.p
		t.Run(c.name, func(t *testing.T) {
			e := export.New(export.Config{Protocol: p, ObservationDomain: 7, Boot: boot, Templates: flow.TemplateOptions{Profile: c.profile}})
			in := []flow.Record{sampleV6(), sampleV4(0), sampleV4(1)}
			dg := e.Export(t0, in)
			if len(dg) != 1 {
				t.Fatalf("esperaba 1 datagrama, hay %d", len(dg))
			}
			infos, out := decodeAll(t, dg)
			if infos[0].Version != uint16(p) || infos[0].ObservationID != 7 || infos[0].TemplateRecords != 2 || infos[0].DataRecords != 3 {
				t.Fatalf("cabecera inesperada: %+v", infos[0])
			}
			// El exportador ordena IPv4 antes que IPv6.
			want := []flow.Record{in[1], in[2], in[0]}
			for i := range want {
				w, g := want[i], out[i]
				if p == flow.V9 { // v9 no lleva MAC ni TTL
					w.SrcMAC, w.DstMAC, w.MinTTL, w.MaxTTL = [6]byte{}, [6]byte{}, 0, 0
				}
				if c.name == "ipfix" && !w.IsV6() {
					// RouterOS 7 repite los valores previos en los campos post-NAT.
					w.PostNATSrc, w.PostNATSrcPort, w.PostNATDst, w.PostNATDstPort = w.SrcIP, w.SrcPort, w.DstIP, w.DstPort
				}
				if g != w {
					t.Errorf("registro %d:\n got %+v\nwant %+v", i, g, w)
				}
			}
		})
	}
}

func TestNATFields(t *testing.T) {
	e := export.New(export.Config{Protocol: flow.IPFIX, Boot: boot, Templates: flow.TemplateOptions{NATFields: true}})
	r := sampleV4(0)
	r.PostNATSrc, r.PostNATSrcPort = netip.MustParseAddr("203.0.113.2"), 61000
	r.PostNATDst, r.PostNATDstPort = r.DstIP, r.DstPort
	_, out := decodeAll(t, e.Export(t0, []flow.Record{r}))
	if out[0] != r {
		t.Fatalf("got %+v want %+v", out[0], r)
	}
}

// TestRouterOSTemplates comprueba que las plantillas por defecto de IPFIX
// son las de la captura real (docs/traffic-model.md §4.4.2): IDs 258/259,
// 37 y 34 campos, mismo orden y longitudes.
func TestRouterOSTemplates(t *testing.T) {
	want4 := [][2]uint16{{60, 1}, {22, 4}, {21, 4}, {160, 8}, {2, 8}, {1, 8}, {7, 2}, {11, 2}, {10, 4}, {14, 4},
		{4, 1}, {5, 1}, {6, 1}, {57, 6}, {80, 6}, {81, 6}, {56, 6}, {8, 4}, {12, 4}, {15, 4}, {9, 1}, {13, 1},
		{192, 1}, {206, 1}, {189, 1}, {224, 8}, {205, 2}, {184, 4}, {185, 4}, {186, 2}, {33, 1}, {176, 1},
		{177, 1}, {225, 4}, {226, 4}, {227, 2}, {228, 2}}
	want6 := [][2]uint16{{60, 1}, {22, 4}, {21, 4}, {160, 8}, {2, 8}, {1, 8}, {7, 2}, {11, 2}, {10, 4}, {14, 4},
		{4, 1}, {5, 1}, {6, 1}, {57, 6}, {80, 6}, {81, 6}, {56, 6}, {27, 16}, {28, 16}, {62, 16}, {29, 1},
		{30, 1}, {192, 1}, {206, 1}, {189, 1}, {224, 8}, {205, 2}, {184, 4}, {185, 4}, {186, 2}, {33, 1},
		{178, 1}, {179, 1}, {31, 4}}
	e := export.New(export.Config{Protocol: flow.IPFIX, Boot: boot})
	v4, v6 := e.TemplatesInUse()
	for _, c := range []struct {
		t    flow.Template
		id   uint16
		want [][2]uint16
	}{{v4, 258, want4}, {v6, 259, want6}} {
		if c.t.ID != c.id || len(c.t.Fields) != len(c.want) {
			t.Fatalf("plantilla %d con %d campos, esperaba %d con %d", c.t.ID, len(c.t.Fields), c.id, len(c.want))
		}
		for i, f := range c.t.Fields {
			if f.ID != c.want[i][0] || f.Len != c.want[i][1] {
				t.Errorf("plantilla %d campo %d: IE %d/%d, esperaba %d/%d", c.id, i, f.ID, f.Len, c.want[i][0], c.want[i][1])
			}
		}
	}
	// Campos derivados del registro: versión IP, longitud total y UDP.
	r := sampleV4(0)
	r.Proto, r.TCPFlags, r.Bytes, r.Packets = flow.ProtoUDP, 0, 1348*3, 3
	dg := e.Export(t0, []flow.Record{r})
	rec := dg[0][len(dg[0])-v4.RecordLen():]
	field := func(id uint16) []byte {
		off := 0
		for _, f := range v4.Fields {
			if f.ID == id {
				return rec[off : off+int(f.Len)]
			}
			off += int(f.Len)
		}
		t.Fatalf("IE %d no está", id)
		return nil
	}
	if field(60)[0] != 4 || binary.BigEndian.Uint64(field(224)) != 1348 || binary.BigEndian.Uint16(field(205)) != 1328 ||
		field(189)[0] != 5 || field(192)[0] != 63 {
		t.Errorf("campos derivados inesperados: ver=%d total=%d udp=%d hdr=%d ttl=%d", field(60)[0],
			binary.BigEndian.Uint64(field(224)), binary.BigEndian.Uint16(field(205)), field(189)[0], field(192)[0])
	}
}

func TestSequenceAndTemplateRefresh(t *testing.T) {
	for _, p := range []flow.Protocol{flow.IPFIX, flow.V9} {
		t.Run(p.String(), func(t *testing.T) {
			e := export.New(export.Config{Protocol: p, Boot: boot, TemplateRefresh: 5, TemplateTimeout: time.Minute})
			var all [][]byte
			for i := 0; i < 12; i++ {
				all = append(all, e.Export(t0.Add(time.Duration(i)*time.Second), []flow.Record{sampleV4(i), sampleV4(i + 100)})...)
			}
			infos, _ := decodeAll(t, all)
			var seq uint32
			for i, in := range infos {
				hasT := in.TemplateRecords > 0
				if hasT != (i%5 == 0) {
					t.Errorf("paquete %d: plantilla=%v", i, hasT)
				}
				if in.Sequence != seq {
					t.Errorf("paquete %d: secuencia %d, esperaba %d", i, in.Sequence, seq)
				}
				if p == flow.V9 {
					seq++
				} else {
					seq += uint32(in.DataRecords)
				}
			}
			st := e.Stats()
			if st.Datagrams != 12 || st.TemplateSends != 3 || st.DataRecords != 24 {
				t.Errorf("stats %+v", st)
			}
		})
	}
}

func TestTemplateTimeoutWithoutData(t *testing.T) {
	e := export.New(export.Config{Protocol: flow.IPFIX, Boot: boot, TemplateTimeout: time.Minute})
	if dg := e.Export(t0, nil); len(dg) != 1 {
		t.Fatalf("la primera llamada debe anunciar plantillas")
	}
	if dg := e.Export(t0.Add(30*time.Second), nil); len(dg) != 0 {
		t.Fatalf("sin datos ni timeout no debe enviar nada")
	}
	if dg := e.Export(t0.Add(time.Minute), nil); len(dg) != 1 {
		t.Fatalf("al vencer el timeout debe reenviar plantillas")
	}
}

func TestMaxDatagramAndPadding(t *testing.T) {
	for _, p := range []flow.Protocol{flow.IPFIX, flow.V9} {
		e := export.New(export.Config{Protocol: p, Boot: boot, MaxDatagram: 600})
		recs := make([]flow.Record, 50)
		for i := range recs {
			recs[i] = sampleV4(i)
		}
		recs = append(recs, sampleV6())
		dg := e.Export(t0, recs)
		if len(dg) < 5 {
			t.Fatalf("%s: esperaba varios datagramas, hay %d", p, len(dg))
		}
		for _, d := range dg {
			if len(d) > 600 {
				t.Errorf("%s: datagrama de %d bytes > 600", p, len(d))
			}
			if p == flow.V9 {
				// Cada FlowSet v9 está alineado a 4 bytes.
				for off := 20; off+4 <= len(d); {
					l := int(binary.BigEndian.Uint16(d[off+2:]))
					if l%4 != 0 {
						t.Errorf("FlowSet de %d bytes sin relleno", l)
					}
					off += l
				}
			}
		}
		_, out := decodeAll(t, dg)
		if len(out) != len(recs) {
			t.Fatalf("%s: %d registros decodificados, esperaba %d", p, len(out), len(recs))
		}
	}
}
