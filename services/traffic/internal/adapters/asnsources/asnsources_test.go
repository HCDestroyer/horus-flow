package asnsources

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"flag"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/datasets"
	"github.com/hcdestroyer/horus-flow/services/traffic/api/asn"
	"github.com/hcdestroyer/horus-flow/services/traffic/internal/app/asnbuild"
)

var update = flag.Bool("update", false, "regenerar los fixtures MRT binarios")

func fixtures(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "tests", "fixtures", "datasets")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no se encontró la raíz del repositorio")
		}
		dir = parent
	}
}

func src(id, format string) datasets.Source {
	return datasets.Source{ID: id, Kind: "asn", Format: format, URL: "https://example.org/x", License: "x",
		LicenseURL: "https://example.org/l", CommercialUse: datasets.CommercialYes, Frequency: datasets.Duration(time.Hour)}
}

func routesByPrefix(res *Result) map[string]asn.Route {
	m := map[string]asn.Route{}
	for _, e := range res.Routes {
		m[e.Prefix.String()] = e.Route
	}
	return m
}

// --- Constructor de MRT TABLE_DUMP_V2 para los fixtures --------------------

type mrtWriter struct{ buf bytes.Buffer }

func (w *mrtWriter) record(typ, sub uint16, body []byte) {
	var h [12]byte
	binary.BigEndian.PutUint32(h[0:4], 1791432000) // 2026-10-08T04:00:00Z
	binary.BigEndian.PutUint16(h[4:6], typ)
	binary.BigEndian.PutUint16(h[6:8], sub)
	binary.BigEndian.PutUint32(h[8:12], uint32(len(body)))
	w.buf.Write(h[:])
	w.buf.Write(body)
}

func (w *mrtWriter) peerIndex(peers int) {
	var b []byte
	b = append(b, 192, 0, 2, 254)           // collector BGP ID
	b = binary.BigEndian.AppendUint16(b, 4) // longitud del nombre de la vista
	b = append(b, "test"...)                // nombre de la vista
	b = binary.BigEndian.AppendUint16(b, uint16(peers))
	for i := range peers {
		b = append(b, 0x02)                                   // peer type: IPv4, AS de 4 bytes
		b = append(b, 192, 0, 2, byte(i+1))                   // peer BGP ID
		b = append(b, 192, 0, 2, byte(i+1))                   // peer IP
		b = binary.BigEndian.AppendUint32(b, 64600+uint32(i)) // peer AS
	}
	w.record(mrtTableDumpV2, subPeerIndexTable, b)
}

// asPath codifica un AS_PATH: cada segmento es (tipo, ASN…).
func asPath(segs ...[]uint32) []byte {
	var val []byte
	for _, s := range segs {
		val = append(val, byte(s[0]), byte(len(s)-1))
		for _, a := range s[1:] {
			val = binary.BigEndian.AppendUint32(val, a)
		}
	}
	attrs := []byte{0x40, 1, 1, 0} // ORIGIN IGP
	attrs = append(attrs, 0x50, bgpAttrASPath)
	attrs = binary.BigEndian.AppendUint16(attrs, uint16(len(val)))
	return append(attrs, val...)
}

func seq(asns ...uint32) []uint32 { return append([]uint32{asPathSegSequence}, asns...) }
func set(asns ...uint32) []uint32 { return append([]uint32{asPathSegSet}, asns...) }

func (w *mrtWriter) rib(seqNo uint32, p netip.Prefix, paths ...[]byte) {
	sub := uint16(subRIBIPv4Unicast)
	if p.Addr().Is6() {
		sub = subRIBIPv6Unicast
	}
	var b []byte
	b = binary.BigEndian.AppendUint32(b, seqNo)
	b = append(b, byte(p.Bits()))
	raw := p.Addr().AsSlice()
	b = append(b, raw[:(p.Bits()+7)/8]...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(paths)))
	for i, attrs := range paths {
		b = binary.BigEndian.AppendUint16(b, uint16(i))
		b = binary.BigEndian.AppendUint32(b, 1791400000)
		b = binary.BigEndian.AppendUint16(b, uint16(len(attrs)))
		b = append(b, attrs...)
	}
	w.record(mrtTableDumpV2, sub, b)
}

func buildRISFixture() []byte {
	var w mrtWriter
	w.record(16, 4, []byte{0, 1, 2, 3}) // BGP4MP previo: se ignora
	w.peerIndex(3)
	w.rib(0, netip.MustParsePrefix("192.0.2.0/24"),
		asPath(seq(64600, 3356, 64500)), asPath(seq(64601, 64500)), asPath(seq(64602, 174, 64500)))
	w.rib(1, netip.MustParsePrefix("198.51.100.0/26"), asPath(seq(64600, 64501)))
	w.rib(2, netip.MustParsePrefix("203.0.113.0/24"), // MOAS: gana 64502 (2 votos)
		asPath(seq(64600, 64502)), asPath(seq(64601, 64503)), asPath(seq(64602, 64502)))
	w.rib(3, netip.MustParsePrefix("203.0.113.128/25"), asPath(seq(64600), set(64504, 64505))) // AS_SET: ambiguo
	w.rib(4, netip.MustParsePrefix("10.0.0.0/8"), asPath(seq(64600, 64506)))                   // privado: se ignora
	w.rib(5, netip.MustParsePrefix("2001:db8:1000::/36"), asPath(seq(64600, 64510)), asPath(seq(64601, 6939, 64510)))
	return w.buf.Bytes()
}

func TestMRTFixture(t *testing.T) {
	path := filepath.Join(fixtures(t), "ris-rrc00.mrt.gz")
	want := buildRISFixture()
	if *update {
		var gz bytes.Buffer
		zw := gzip.NewWriter(&gz)
		zw.ModTime = time.Time{}
		_, _ = zw.Write(want)
		_ = zw.Close()
		if err := os.WriteFile(path, gz.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		trunc := want[:len(want)-7]
		if err := os.WriteFile(filepath.Join(fixtures(t), "corrupt", "ris-truncated.mrt"), trunc, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(committed))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil || !bytes.Equal(plain, want) {
		t.Fatalf("el fixture MRT no coincide con el generador (go test -run TestMRTFixture -update): %v", err)
	}
	res, err := ParseFile(path, src("ris-rrc00", "mrt-table-dump-v2"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Role != asnbuild.RoleBGP {
		t.Fatalf("role %v", res.Role)
	}
	m := routesByPrefix(res)
	cases := map[string]uint32{
		"192.0.2.0/24":       64500,
		"198.51.100.0/26":    64501,
		"203.0.113.0/24":     64502,
		"2001:db8:1000::/36": 64510,
	}
	if len(m) != len(cases) {
		t.Fatalf("rutas %v", m)
	}
	for p, a := range cases {
		if m[p].ASN != a || m[p].Source != "ris-rrc00" {
			t.Errorf("%s: %+v, quiero AS%d", p, m[p], a)
		}
	}
	if res.Lines != 6 || res.Ignored != 3 { // BGP4MP + AS_SET + privado
		t.Fatalf("lines %d ignored %d", res.Lines, res.Ignored)
	}
}

func TestMRTCorrupt(t *testing.T) {
	s := src("ris", "mrt-table-dump-v2")
	if _, err := ParseFile(filepath.Join(fixtures(t), "corrupt", "ris-truncated.mrt"), s); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncado: %v", err)
	}
	// RIB antes del PEER_INDEX_TABLE.
	var w mrtWriter
	w.rib(0, netip.MustParsePrefix("192.0.2.0/24"), asPath(seq(64500)))
	if _, err := Parse(bytes.NewReader(w.buf.Bytes()), s); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("sin peer index: %v", err)
	}
	// Texto en vez de MRT.
	if _, err := Parse(bytes.NewReader([]byte("<html>hola</html> no es un MRT válido")), s); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("texto: %v", err)
	}
}

func TestIPToASN(t *testing.T) {
	res, err := ParseFile(filepath.Join(fixtures(t), "iptoasn-v4.tsv"), src("iptoasn-v4", "iptoasn-tsv"))
	if err != nil {
		t.Fatal(err)
	}
	m := routesByPrefix(res)
	if m["192.0.2.0/24"].ASN != 64496 || m["192.0.2.0/24"].Country != "ES" {
		t.Fatalf("192.0.2.0/24: %+v", m["192.0.2.0/24"])
	}
	// 203.0.113.0-99 → /26 + /27 + /30, sin país ("None").
	for _, p := range []string{"203.0.113.0/26", "203.0.113.64/27", "203.0.113.96/30"} {
		if r, ok := m[p]; !ok || r.ASN != 64498 || r.Country != "" {
			t.Fatalf("%s: %+v %v", p, r, ok)
		}
	}
	if res.Ignored != 1 || len(res.Routes) != 5 {
		t.Fatalf("ignored %d rutas %d", res.Ignored, len(res.Routes))
	}
	if res.ASInfo[64497].Name != "EXAMPLE-CONTENT" {
		t.Fatalf("ASInfo %+v", res.ASInfo)
	}
	res, err = ParseFile(filepath.Join(fixtures(t), "iptoasn-v6.tsv"), src("iptoasn-v6", "iptoasn-tsv"))
	if err != nil || routesByPrefix(res)["2001:db8::/48"].ASN != 64499 {
		t.Fatalf("v6: %v %+v", err, res)
	}
	if _, err := ParseFile(filepath.Join(fixtures(t), "corrupt", "iptoasn-garbage.tsv"), src("x", "iptoasn-tsv")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("basura: %v", err)
	}
}

func TestRIR(t *testing.T) {
	res, err := ParseFile(filepath.Join(fixtures(t), "rir-ripencc.txt"), src("rir-ripencc", "rir-delegated"))
	if err != nil {
		t.Fatal(err)
	}
	cc := map[string]string{}
	for _, c := range res.Countries {
		cc[c.Prefix.String()] = c.Country
	}
	if cc["192.0.2.0/24"] != "ES" || cc["203.0.113.0/25"] != "CO" || cc["2001:db8::/32"] != "DE" || len(cc) != 3 {
		t.Fatalf("países %v", cc)
	}
	if res.ASInfo[64496].Country != "NL" || res.ASInfo[64499].Country != "AR" || len(res.ASInfo) != 3 {
		t.Fatalf("ASN %v", res.ASInfo)
	}
	if res.Ignored != 1 { // available
		t.Fatalf("ignored %d", res.Ignored)
	}
	if _, err := ParseFile(filepath.Join(fixtures(t), "corrupt", "rir-count-mismatch.txt"), src("x", "rir-delegated")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("recuento: %v", err)
	}
	if _, err := Parse(bytes.NewReader([]byte("hola|mundo\n")), src("x", "rir-delegated")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("sin cabecera: %v", err)
	}
}

func TestPeeringDB(t *testing.T) {
	res, err := ParseFile(filepath.Join(fixtures(t), "peeringdb.json"), src("peeringdb", "peeringdb-net"))
	if err != nil {
		t.Fatal(err)
	}
	if i := res.ASInfo[64498]; i.Name != "Example Access ISP" || i.NetworkType != "access" {
		t.Fatalf("64498: %+v", i)
	}
	if res.ASInfo[64497].NetworkType != "content" || res.ASInfo[64496].NetworkType != "nsp" {
		t.Fatalf("tipos %+v", res.ASInfo)
	}
	if _, err := ParseFile(filepath.Join(fixtures(t), "corrupt", "peeringdb-error.json"), src("x", "peeringdb-net")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("html: %v", err)
	}
	if _, err := Parse(bytes.NewReader([]byte(`{"meta":{}}`)), src("x", "peeringdb-net")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("sin data: %v", err)
	}
	if _, err := Parse(bytes.NewReader([]byte(`{"data":[]}`)), src("x", "peeringdb-net")); !errors.Is(err, ErrEmpty) {
		t.Fatalf("vacío: %v", err)
	}
}

func TestFormatsAndRoles(t *testing.T) {
	if len(Formats()) != 4 || RoleOf("iptoasn-tsv") != asnbuild.RoleIPToASN || RoleOf("nope") != 0 {
		t.Fatalf("Formats %v", Formats())
	}
	if _, err := Parse(bytes.NewReader(nil), src("x", "nope")); err == nil {
		t.Fatal("formato desconocido debería fallar")
	}
}
