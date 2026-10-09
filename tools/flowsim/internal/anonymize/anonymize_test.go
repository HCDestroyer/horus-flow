package anonymize_test

import (
	"bytes"
	"context"
	"net/netip"
	"testing"

	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/anonymize"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/capture"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/flow"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/selftest"
	"github.com/hcdestroyer/horus-flow/tools/flowsim/internal/verify"
)

var (
	key = bytes.Repeat([]byte{0x42}, 32)
	// El simulador ya usa 192.0.2.10 (hub WireGuard): aquí se sustituye el NAT
	// por 192.0.2.20-22.
	natIPs  = []netip.Addr{netip.MustParseAddr("192.0.2.20"), netip.MustParseAddr("192.0.2.21"), netip.MustParseAddr("192.0.2.22")}
	expIP   = netip.MustParseAddr("10.255.3.17")
	clients = netip.MustParsePrefix("10.20.0.0/16")
	docs    = []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	}
)

// simFrames genera el escenario normal (IPFIX RouterOS 7 con NAT real) y lo
// envuelve en tramas Ethernet con MAC unicast, como una captura real.
func simFrames(t *testing.T) []capture.Frame {
	t.Helper()
	data, _, err := selftest.Generate(context.Background(), selftest.Case{Scenario: "normal", Protocol: flow.IPFIX, Format: capture.FormatPcapng})
	if err != nil {
		t.Fatal(err)
	}
	var out []capture.Frame
	err = capture.ReadFrames(bytes.NewReader(data), func(f capture.Frame) error {
		eth := []byte{0x08, 0xbf, 0xb8, 0x39, 0xad, 0xcc, 0x78, 0x9a, 0x18, 0xbe, 0x99, 0xb4}
		if f.Data[0]>>4 == 6 {
			eth = append(eth, 0x86, 0xdd)
		} else {
			eth = append(eth, 0x08, 0x00)
		}
		out = append(out, capture.Frame{Time: f.Time, LinkType: 1, Data: append(eth, f.Data...)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func decode(t *testing.T, frames []capture.Frame) []flow.Record {
	t.Helper()
	dec := verify.NewDecoder()
	var recs []flow.Record
	for _, f := range frames {
		d, ok := capture.ParseFrame(f)
		if !ok {
			t.Fatal("trama no UDP")
		}
		_, rs, err := dec.Decode("x", d.Payload)
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, rs...)
	}
	return recs
}

func inDocs(a netip.Addr) bool {
	for _, p := range docs {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func TestAnonymizeSimCapture(t *testing.T) {
	orig := simFrames(t)
	out, st, err := anonymize.Anonymize(orig, anonymize.Config{Key: key, ClientNet: clients, Exporter: expIP, NATIPs: natIPs})
	if err != nil {
		t.Fatal(err)
	}
	if st.FramesOut != len(orig) || st.NATIPs != 3 || st.DroppedNoTmpl != 0 {
		t.Fatalf("estadísticas inesperadas: %+v", st)
	}
	r, err := anonymize.Check(orig, out)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() || r.OriginalIPs == 0 || r.OriginalMACs == 0 {
		t.Fatalf("comprobación: %+v", r)
	}
	// Cabeceras: exportador sustituido y checksums válidos (los relee capture).
	for _, f := range out {
		d, ok := capture.ParseFrame(f)
		if !ok || d.Src.Addr() != expIP || d.Src.Port() == 0 {
			t.Fatalf("cabecera: %+v %v", d.Src, ok)
		}
	}
	a, b := decode(t, orig), decode(t, out)
	if len(a) != len(b) {
		t.Fatalf("%d registros, esperaba %d", len(b), len(a))
	}
	seen := map[netip.Addr]netip.Addr{}
	consistent := func(x, y netip.Addr) {
		if !x.IsValid() {
			return
		}
		if prev, ok := seen[x]; ok && prev != y {
			t.Fatalf("mapeo inconsistente: %s → %s y %s", x, prev, y)
		}
		seen[x] = y
	}
	for i := range a {
		x, y := a[i], b[i]
		// Puertos, contadores, tiempos, protocolo y flags intactos.
		if x.SrcPort != y.SrcPort || x.DstPort != y.DstPort || x.Bytes != y.Bytes || x.Packets != y.Packets ||
			!x.Start.Equal(y.Start) || !x.End.Equal(y.End) || x.Proto != y.Proto || x.TCPFlags != y.TCPFlags ||
			x.PostNATSrcPort != y.PostNATSrcPort || x.PostNATDstPort != y.PostNATDstPort {
			t.Fatalf("registro %d cambió fuera de las direcciones:\n%+v\n%+v", i, x, y)
		}
		for _, pair := range [][2]netip.Addr{{x.SrcIP, y.SrcIP}, {x.DstIP, y.DstIP}, {x.PostNATSrc, y.PostNATSrc}, {x.PostNATDst, y.PostNATDst}, {x.NextHop, y.NextHop}} {
			o, n := pair[0], pair[1]
			consistent(o, n)
			switch {
			case !o.IsValid() || o.IsMulticast() || o.IsUnspecified() || n == expIP:
			case o.Is4() && flow.IsClientPrivate(o):
				if !clients.Contains(n) || o.As4()[3] != n.As4()[3] {
					t.Fatalf("privada %s → %s fuera de %s o sin conservar el host", o, n, clients)
				}
			case o.Is4():
				if !inDocs(n) {
					t.Fatalf("pública %s → %s fuera de los rangos de documentación", o, n)
				}
			case o.IsLinkLocalUnicast():
				if !n.IsLinkLocalUnicast() {
					t.Fatalf("enlace local %s → %s", o, n)
				}
			default:
				if !netip.MustParsePrefix("2001:db8::/32").Contains(n) {
					t.Fatalf("IPv6 %s → %s fuera de 2001:db8::/32", o, n)
				}
			}
		}
		// Estructura de /24 y /64: mismo prefijo original ⇒ mismo prefijo nuevo.
		if x.SrcMAC != ([6]byte{}) && y.SrcMAC[0]&0x02 == 0 {
			t.Fatalf("MAC %x no administrada localmente", y.SrcMAC)
		}
	}
	// Bajadas NAT: dst = IP pública del NAT ⇒ 192.0.2.10-12.
	nat := 0
	for i := range b {
		if b[i].PostNATDst.IsValid() && b[i].PostNATDst != b[i].DstIP && clients.Contains(b[i].PostNATDst) {
			nat++
			if b[i].DstIP.Compare(natIPs[0]) < 0 || b[i].DstIP.Compare(natIPs[2]) > 0 {
				t.Fatalf("IP de NAT %s fuera de 192.0.2.20-22", b[i].DstIP)
			}
		}
	}
	if nat == 0 {
		t.Fatal("no hay bajadas con NAT")
	}
	// Mismo /24 original ⇒ mismo /24 anonimizado (y distintos ⇒ distintos).
	p24 := map[netip.Prefix]netip.Prefix{}
	back := map[netip.Prefix]netip.Prefix{}
	for o, n := range seen {
		if o.Is4() && flow.IsClientPrivate(o) && n != expIP { // el exportador tiene IP fija
			po, _ := o.Prefix(24)
			pn, _ := n.Prefix(24)
			if v, ok := p24[po]; ok && v != pn {
				t.Fatalf("el /24 %s se reparte en %s y %s", po, v, pn)
			}
			if v, ok := back[pn]; ok && v != po {
				t.Fatalf("los /24 %s y %s van al mismo %s", v, po, pn)
			}
			p24[po], back[pn] = pn, po
		}
	}
}

// TestDeterministic: misma clave ⇒ mismos bytes; otra clave ⇒ otro mapeo.
func TestDeterministic(t *testing.T) {
	orig := simFrames(t)[:40]
	cfg := anonymize.Config{Key: key, ClientNet: clients, Exporter: expIP, NATIPs: natIPs}
	a, _, err := anonymize.Anonymize(orig, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := anonymize.Anonymize(orig, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Key = bytes.Repeat([]byte{0x17}, 32)
	c, _, err := anonymize.Anonymize(orig, cfg)
	if err != nil {
		t.Fatal(err)
	}
	same, diff := true, false
	for i := range a {
		same = same && bytes.Equal(a[i].Data, b[i].Data)
		diff = diff || !bytes.Equal(a[i].Data, c[i].Data)
	}
	if !same || !diff {
		t.Fatalf("determinismo: misma clave igual=%t, otra clave distinta=%t", same, diff)
	}
}

func TestCropAlignsToTemplate(t *testing.T) {
	orig := simFrames(t)
	from := orig[len(orig)/2].Time.Sub(orig[0].Time)
	out, st, err := anonymize.Anonymize(orig, anonymize.Config{
		Key: key, ClientNet: clients, Exporter: expIP, NATIPs: natIPs, From: from, Duration: 30e9, AlignTemplate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 || len(out) >= len(orig) || st.DroppedNoTmpl != 0 {
		t.Fatalf("recorte: %d tramas de %d", len(out), len(orig))
	}
	// El primer datagrama lleva plantillas: el recorte se decodifica sin huecos.
	dec := verify.NewDecoder()
	d, _ := capture.ParseFrame(out[0])
	info, _, err := dec.Decode("x", d.Payload)
	if err != nil || info.TemplateRecords == 0 {
		t.Fatalf("el recorte no empieza por plantillas: %+v %v", info, err)
	}
	if out[len(out)-1].Time.Sub(out[0].Time) > 30e9 {
		t.Fatal("el recorte excede la duración")
	}
}

func TestRejectsShortKeyAndTooManyNATIPs(t *testing.T) {
	orig := simFrames(t)[:20]
	if _, _, err := anonymize.Anonymize(orig, anonymize.Config{Key: []byte("corta"), Exporter: expIP, NATIPs: natIPs}); err == nil {
		t.Error("una clave corta debe rechazarse")
	}
	if _, _, err := anonymize.Anonymize(orig, anonymize.Config{Key: key, Exporter: expIP, NATIPs: natIPs[:1]}); err == nil {
		t.Error("con más IPs de NAT que sustitutas debe fallar")
	}
	clash := []netip.Addr{netip.MustParseAddr("192.0.2.10"), natIPs[1], natIPs[2]}
	if _, _, err := anonymize.Anonymize(simFrames(t), anonymize.Config{Key: key, Exporter: expIP, NATIPs: clash}); err == nil {
		t.Error("una IP de sustitución presente en el original debe rechazarse")
	}
}
