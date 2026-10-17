package decode

import (
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixtures SINTÉTICOS de Huawei VRP (NetStream v9 e IPFIX) y sFlow v5, construidos a partir de la
// documentación oficial (plantillas de NetStream de VRP, RFC 3954, RFC 7011, RFC 5477 y la especificación
// sFlow v5), NO capturados de un equipo real (docs/vendors/huawei.md §7 y §9). Se guardan en
// testdata/*.hex (un datagrama por línea) y se regeneran con HORUS_UPDATE_FIXTURES=1. Cuando haya una captura
// real de NetStream/sFlow basta con añadirla como fixture con sus totales esperados.

const fixtureNow = 1_800_000_000

// --- constructores ---

func v9(seq uint32, uptimeMs uint32, sets []byte) []byte {
	b := binary.BigEndian.AppendUint16(nil, 9)
	b = binary.BigEndian.AppendUint16(b, 0) // count (no se usa)
	b = binary.BigEndian.AppendUint32(b, uptimeMs)
	b = binary.BigEndian.AppendUint32(b, fixtureNow)
	b = binary.BigEndian.AppendUint32(b, seq)
	b = binary.BigEndian.AppendUint32(b, 0x00010000) // source id: slot 1
	return append(b, sets...)
}

func v9OptionsTemplate(id uint16, scope, opts [][2]uint16) []byte {
	b := binary.BigEndian.AppendUint16(nil, id)
	b = binary.BigEndian.AppendUint16(b, uint16(4*len(scope))) //nolint:gosec // test
	b = binary.BigEndian.AppendUint16(b, uint16(4*len(opts)))  //nolint:gosec // test
	for _, f := range append(append([][2]uint16{}, scope...), opts...) {
		b = binary.BigEndian.AppendUint16(b, f[0])
		b = binary.BigEndian.AppendUint16(b, f[1])
	}
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func be(n int, v uint64) []byte {
	b := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
	return b
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// huaweiV9Template es la plantilla de flujos originales IPv4 de NetStream v9 de VRP (campos documentados por
// Huawei para "ip netstream export version 9": bytes, paquetes, protocolo, ToS, flags, puertos, direcciones y
// máscaras, ifIndex de entrada/salida, siguiente salto, AS, tiempos relativos a sysUptime, dirección, ID de
// sampler) más un campo propietario desconocido (tipo 40001) que el decodificador debe saltar.
var huaweiV9Template = [][2]uint16{
	{1, 4}, {2, 4}, {4, 1}, {5, 1}, {6, 1}, {7, 2}, {8, 4}, {9, 1}, {10, 4}, {11, 2}, {12, 4}, {13, 1}, {14, 4}, {15, 4},
	{16, 4}, {17, 4}, {21, 4}, {22, 4}, {61, 1}, {48, 1}, {40001, 4},
}

func huaweiV9Record(src, dst [4]byte, sport, dport uint16, bytes, pkts uint32, in, out uint32, dir byte) []byte {
	return cat(be(4, uint64(bytes)), be(4, uint64(pkts)), []byte{6, 0, 0x18}, be(2, uint64(sport)), src[:], []byte{24}, be(4, uint64(in)),
		be(2, uint64(dport)), dst[:], []byte{0}, be(4, uint64(out)), []byte{198, 51, 100, 1}, be(4, 64512), be(4, 15169),
		be(4, 3_600_000), be(4, 3_540_000), []byte{dir, 1}, be(4, 0xdeadbeef))
}

func huaweiV9Datagrams() [][]byte {
	tmpl := set(0, templateRec(256, huaweiV9Template))
	// Opciones del sampler (ip netstream export template option sampler): ámbito System, samplerId,
	// samplerMode (determinista) y samplerRandomInterval = 1000.
	opt := set(1, v9OptionsTemplate(257, [][2]uint16{{1, 4}}, [][2]uint16{{48, 1}, {49, 1}, {50, 4}}))
	optData := set(257, cat(be(4, 0x01000000), []byte{1, 1}, be(4, 1000), []byte{0, 0})) // relleno a 4
	data := set(256, cat(
		huaweiV9Record([4]byte{100, 64, 1, 10}, [4]byte{142, 250, 1, 1}, 51000, 443, 1500, 3, 17, 10, 0),
		huaweiV9Record([4]byte{142, 250, 1, 1}, [4]byte{100, 64, 1, 10}, 443, 51000, 9000, 6, 10, 17, 1),
	))
	return [][]byte{v9(1, 3_600_500, cat(tmpl, opt)), v9(2, 3_600_600, cat(optData, data))}
}

// huaweiIPFIXDatagrams: IPFIX (ip netstream export version ipfix) con IPv6, tiempos absolutos en ms, muestreo
// PSAMP (samplingSize 1 / samplingPopulation 1000) y dos elementos de empresa de Huawei (PEN 2011), uno de
// longitud fija y otro variable, que se ignoran.
func huaweiIPFIXDatagrams() [][]byte {
	fields := cat(
		templateRec(300, nil)[:2], be(2, 13), // id + nº de campos (se rellenan abajo)
		be(2, 1), be(2, 8), be(2, 2), be(2, 8), be(2, 4), be(2, 1), be(2, 7), be(2, 2), be(2, 11), be(2, 2),
		be(2, 27), be(2, 16), be(2, 28), be(2, 16), be(2, 10), be(2, 4), be(2, 14), be(2, 4),
		be(2, 152), be(2, 8), be(2, 153), be(2, 8),
		be(2, 309), be(2, 4), be(2, 310), be(2, 4),
	)
	// Campos de empresa: bit alto + PEN 2011.
	fields = cat(fields, be(2, 0x8000|1), be(2, 4), be(4, 2011), be(2, 0x8000|2), be(2, 0xffff), be(4, 2011))
	binary.BigEndian.PutUint16(fields[2:], 15)
	src := netip.MustParseAddr("2001:db8:100::10").As16()
	dst := netip.MustParseAddr("2a00:1450::1").As16()
	start := uint64(fixtureNow)*1000 - 30_000
	rec := cat(be(8, 4000), be(8, 4), []byte{17}, be(2, 5353), be(2, 53), src[:], dst[:], be(4, 21), be(4, 10),
		be(8, start), be(8, start+20_000), be(4, 1), be(4, 1000), be(4, 7), []byte{3, 'h', 'w', '!'})
	return [][]byte{ipfix(1, cat(set(2, fields), set(300, rec)))}
}

// sFlow v5.
func sflowDatagram(seq uint32, samples ...[]byte) []byte {
	b := cat(be(4, 5), be(4, 1), []byte{198, 51, 100, 20}, be(4, 7), be(4, uint64(seq)), be(4, 123456), be(4, uint64(len(samples))))
	for _, s := range samples {
		b = append(b, s...)
	}
	return b
}

func mkSample(format uint32, body []byte) []byte {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	return cat(be(4, uint64(format)), be(4, uint64(len(body))), body)
}

func sflowRecord(format uint32, data []byte) []byte { return mkSample(format, data) }

func rawHeader(proto, frameLen uint32, hdr []byte) []byte {
	return sflowRecord(1, cat(be(4, uint64(proto)), be(4, uint64(frameLen)), be(4, 4), be(4, uint64(len(hdr))), hdr))
}

func ethVLANIPv4TCP() []byte {
	eth := cat([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, be(2, 0x8100), be(2, 100), be(2, 0x0800))
	ip := cat([]byte{0x45, 0, 0, 40, 0, 1, 0x40, 0, 64, 6, 0, 0}, []byte{100, 64, 1, 10}, []byte{93, 184, 216, 34})
	tcp := cat(be(2, 51000), be(2, 443), be(4, 1), be(4, 0), []byte{0x50, 0x12}, be(2, 65535), be(4, 0))
	return cat(eth, ip, tcp)
}

func ethIPv6UDP() []byte {
	eth := cat([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, be(2, 0x86dd))
	src := netip.MustParseAddr("2001:db8:100::10").As16()
	dst := netip.MustParseAddr("2001:4860::8888").As16()
	ip := cat([]byte{0x60, 0, 0, 0}, be(2, 8), []byte{17, 64}, src[:], dst[:])
	return cat(eth, ip, be(2, 40000), be(2, 53), be(2, 8), be(2, 0))
}

func sflowDatagrams() [][]byte {
	flow := mkSample(1, cat(be(4, 1), be(4, 0x00000011), be(4, 2048), be(4, 99999), be(4, 0), be(4, 17), be(4, 10), be(4, 2),
		rawHeader(1, 1514, ethVLANIPv4TCP()), sflowRecord(1001, cat(be(4, 100), be(4, 0), be(4, 200), be(4, 0)))))
	exp := mkSample(3, cat(be(4, 2), be(4, 0), be(4, 10), be(4, 512), be(4, 5000), be(4, 0), be(4, 0), be(4, 10), be(4, 0), be(4, 17),
		be(4, 1), rawHeader(1, 90, ethIPv6UDP())))
	counters := mkSample(2, cat(be(4, 3), be(4, 17), be(4, 0)))
	vendor := mkSample(2011<<12|1, []byte{1, 2, 3, 4})
	return [][]byte{sflowDatagram(1, flow, counters), sflowDatagram(2, exp, vendor)}
}

// --- fixtures en disco ---

func fixtureSets() map[string][][]byte {
	return map[string][][]byte{
		"huawei-netstream-v9-synthetic.hex": huaweiV9Datagrams(),
		"huawei-ipfix-synthetic.hex":        huaweiIPFIXDatagrams(),
		"sflow-v5-synthetic.hex":            sflowDatagrams(),
	}
}

func loadHex(t *testing.T, name string) [][]byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := hex.DecodeString(line)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out = append(out, p)
	}
	return out
}

func TestSyntheticFixturesUpToDate(t *testing.T) {
	for name, dgs := range fixtureSets() {
		var b strings.Builder
		b.WriteString("# SINTÉTICO (no capturado de un equipo real): ver huawei_test.go y docs/vendors/huawei.md §9. Un datagrama por línea.\n")
		for _, d := range dgs {
			b.WriteString(hex.EncodeToString(d) + "\n")
		}
		path := filepath.Join("testdata", name)
		if os.Getenv("HORUS_UPDATE_FIXTURES") == "1" {
			if err := os.MkdirAll("testdata", 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != b.String() {
			t.Fatalf("%s desactualizado: HORUS_UPDATE_FIXTURES=1 go test -run TestSyntheticFixturesUpToDate (%v)", name, err)
		}
	}
}

// --- pruebas ---

func TestHuaweiNetStreamV9(t *testing.T) {
	t.Parallel()
	d := New(Options{Now: func() time.Time { return time.Unix(fixtureNow, 0) }})
	var recs []Result
	for _, dg := range loadHex(t, "huawei-netstream-v9-synthetic.hex") {
		res, err := d.Decode("198.51.100.20", dg)
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, res)
	}
	if recs[0].Templates != 2 || recs[1].OptionsRecords != 1 || recs[1].DataRecords != 2 || len(recs[1].Records) != 2 {
		t.Fatalf("plantillas/opciones/datos: %+v / %+v", recs[0], recs[1])
	}
	if s := d.Sampling("198.51.100.20"); s != 1000 {
		t.Fatalf("samplerRandomInterval (IE 50) del sampler de NetStream: %d", s)
	}
	up, down := recs[1].Records[0], recs[1].Records[1]
	if up.SrcIP != netip.MustParseAddr("100.64.1.10") || up.DstPort != 443 || up.Bytes != 1500 || up.Packets != 3 || up.InputIfIndex != 17 ||
		up.OutputIfIndex != 10 || up.SrcAS != 64512 || up.DstAS != 15169 || up.TCPFlags != 0x18 || up.FlowDirection != "ingress" ||
		up.NextHop != netip.MustParseAddr("198.51.100.1") || up.FlowSource != "netflow_v9" || up.SamplingRate != 0 {
		t.Fatalf("registro de subida: %+v", up)
	}
	// Tiempos relativos a sysUptime: arranque = exportTime − uptime de la cabecera.
	boot := time.UnixMilli(int64(fixtureNow)*1000 - 3_600_600)
	if !up.TS.Equal(boot.Add(3_600_000*time.Millisecond)) || !up.FlowStart.Equal(boot.Add(3_540_000*time.Millisecond)) {
		t.Fatalf("tiempos: %v %v", up.FlowStart, up.TS)
	}
	if down.FlowDirection != "egress" || down.Bytes != 9000 || down.SrcPort != 443 {
		t.Fatalf("registro de bajada: %+v", down)
	}
}

func TestHuaweiIPFIX(t *testing.T) {
	t.Parallel()
	d := New(Options{})
	dgs := loadHex(t, "huawei-ipfix-synthetic.hex")
	res, err := d.Decode("198.51.100.20", dgs[0])
	if err != nil || len(res.Records) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	r := res.Records[0]
	if r.SrcIP != netip.MustParseAddr("2001:db8:100::10") || r.DstIP != netip.MustParseAddr("2a00:1450::1") || r.Protocol != 17 ||
		r.SrcPort != 5353 || r.DstPort != 53 || r.Bytes != 4000 || r.Packets != 4 || r.InputIfIndex != 21 || r.OutputIfIndex != 10 ||
		r.SamplingRate != 1000 || r.FlowSource != "ipfix" || r.TS.Sub(r.FlowStart) != 20*time.Second {
		t.Fatalf("registro IPFIX de Huawei (IE de empresa ignorados, PSAMP 1/1000): %+v", r)
	}
}

func TestSFlowV5(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	d := New(Options{Now: func() time.Time { return at }})
	dgs := loadHex(t, "sflow-v5-synthetic.hex")
	res, err := d.Decode("198.51.100.20", dgs[0])
	if err != nil {
		t.Fatal(err)
	}
	if res.Header.Version != VersionSFlow || res.Header.Sequence != 1 || res.Header.ODID != 7 || res.DataRecords != 1 || res.OptionsRecords != 1 ||
		len(res.Records) != 1 || !res.Header.ExportTime.Equal(at) {
		t.Fatalf("cabecera/contadores: %+v", res)
	}
	r := res.Records[0]
	if r.SrcIP != netip.MustParseAddr("100.64.1.10") || r.DstIP != netip.MustParseAddr("93.184.216.34") || r.Protocol != 6 || r.SrcPort != 51000 ||
		r.DstPort != 443 || r.TCPFlags != 0x12 || r.VlanID != 100 || r.Bytes != 1514 || r.Packets != 1 || r.SamplingRate != 2048 ||
		r.InputIfIndex != 17 || r.OutputIfIndex != 10 || r.FlowSource != FlowSourceSFlow || !r.TS.Equal(at) || r.ObservationDomainID != 7 {
		t.Fatalf("muestra Ethernet/802.1Q/IPv4/TCP: %+v", r)
	}
	res, err = d.Decode("198.51.100.20", dgs[1])
	if err != nil || len(res.Records) != 1 || res.DataRecords != 1 || res.OptionsRecords != 0 {
		t.Fatalf("datagrama 2 (muestra expandida + muestra de empresa ignorada): %v %+v", err, res)
	}
	r = res.Records[0]
	if r.SrcIP != netip.MustParseAddr("2001:db8:100::10") || r.Protocol != 17 || r.DstPort != 53 || r.SamplingRate != 512 || r.InputIfIndex != 10 ||
		r.OutputIfIndex != 17 || r.Bytes != 90 {
		t.Fatalf("muestra expandida IPv6/UDP: %+v", r)
	}
}

func TestSFlowSampledIPAndMPLS(t *testing.T) {
	t.Parallel()
	d := New(Options{})
	// sampled_ipv4 (registro 3) sin cabecera y una trama MPLS con IPv4 dentro.
	ipv4 := sflowRecord(3, cat(be(4, 600), be(4, 17), []byte{10, 0, 0, 1}, []byte{10, 0, 0, 2}, be(4, 1000), be(4, 2000), be(4, 0), be(4, 0)))
	s1 := mkSample(1, cat(be(4, 1), be(4, 1), be(4, 100), be(4, 0), be(4, 0), be(4, 0x80000001), be(4, 3), be(4, 1), ipv4))
	mpls := cat([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, be(2, 0x8847), []byte{0, 0x10, 0x01, 64},
		[]byte{0x45, 0, 0, 28, 0, 0, 0, 0, 64, 1, 0, 0, 10, 1, 1, 1, 10, 2, 2, 2, 8, 0, 0, 0})
	s2 := mkSample(1, cat(be(4, 2), be(4, 1), be(4, 100), be(4, 0), be(4, 0), be(4, 4), be(4, 5), be(4, 1), rawHeader(1, 98, mpls)))
	res, err := d.Decode("x", sflowDatagram(9, s1, s2))
	if err != nil || len(res.Records) != 2 {
		t.Fatalf("%v %+v", err, res)
	}
	if a := res.Records[0]; a.Bytes != 600 || a.DstPort != 2000 || a.InputIfIndex != 0 || a.OutputIfIndex != 3 {
		t.Fatalf("sampled_ipv4 (entrada con formato especial = 0): %+v", a)
	}
	if b := res.Records[1]; b.SrcIP != netip.MustParseAddr("10.1.1.1") || b.ICMPTypeCode != 0x0800 {
		t.Fatalf("MPLS → IPv4/ICMP: %+v", b)
	}
	// Fragmento IPv4 no inicial: sin puertos; cabecera desconocida: la muestra no da flujo.
	frag := cat([]byte{0x45, 0, 0, 40, 0, 1, 0x00, 0x10, 64, 6, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2}, make([]byte, 20))
	s3 := mkSample(1, cat(be(4, 3), be(4, 1), be(4, 1), be(4, 0), be(4, 0), be(4, 1), be(4, 2), be(4, 2), rawHeader(11, 60, frag),
		sflowRecord(9999, []byte{1, 2, 3, 4})))
	s4 := mkSample(1, cat(be(4, 4), be(4, 1), be(4, 1), be(4, 0), be(4, 0), be(4, 1), be(4, 2), be(4, 1), rawHeader(99, 60, frag)))
	res, err = d.Decode("x", sflowDatagram(10, s3, s4))
	if err != nil || len(res.Records) != 1 || res.Records[0].DstPort != 0 || res.Records[0].Protocol != 6 || res.DataRecords != 2 {
		t.Fatalf("fragmento / cabecera desconocida: %v %+v", err, res)
	}
}

func TestSFlowMalformedNoPanic(t *testing.T) {
	t.Parallel()
	d := New(Options{})
	for _, dg := range sflowDatagrams() {
		for i := range dg {
			_, _ = d.Decode("x", dg[:i])
			q := append([]byte(nil), dg...)
			q[i] ^= 0xff
			_, _ = d.Decode("y", q)
		}
	}
	if _, err := d.Decode("x", cat(be(4, 5), be(4, 3))); err == nil {
		t.Fatal("tipo de dirección de agente inválido")
	}
	if _, err := d.Decode("x", sflowDatagram(1)[:20]); err == nil {
		t.Fatal("cabecera truncada")
	}
	big := cat(be(4, 5), be(4, 1), []byte{1, 2, 3, 4}, be(4, 0), be(4, 1), be(4, 1), be(4, 100000))
	if _, err := d.Decode("x", big); err == nil {
		t.Fatal("demasiadas muestras")
	}
}

func FuzzDecodeSFlow(f *testing.F) {
	for _, dg := range sflowDatagrams() {
		f.Add(dg)
	}
	for _, dg := range huaweiV9Datagrams() {
		f.Add(dg)
	}
	f.Add(huaweiIPFIXDatagrams()[0])
	d := New(Options{PendingMaxSets: 8})
	f.Fuzz(func(_ *testing.T, b []byte) {
		_, _ = d.Decode("fuzz", b)
	})
}
