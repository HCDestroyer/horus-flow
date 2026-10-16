package decode

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
)

func repo(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", "..", ".."}, parts...)...)
}

type expected struct {
	Exporters []struct {
		ExporterIP string `json:"exporter_ip"`
		Totals     struct {
			DataRecords int    `json:"data_records"`
			RecordsV4   int    `json:"records_v4"`
			RecordsV6   int    `json:"records_v6"`
			Bytes       uint64 `json:"bytes"`
			Packets     uint64 `json:"packets"`
		} `json:"totals"`
	} `json:"exporters"`
}

func fixtures(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{
		repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng"): repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.expected.json"),
	}
	m, err := filepath.Glob(repo("tools", "flowsim", "fixtures", "sim", "*", "*.hfsim.gz"))
	if err != nil || len(m) == 0 {
		t.Fatalf("no sim fixtures: %v", err)
	}
	for _, f := range m {
		out[f] = strings.TrimSuffix(f, ".hfsim.gz") + ".expected.json"
	}
	return out
}

// TestFixtures decodifica el 100 % de los registros de los fixtures del
// simulador (IPFIX y v9, IPv4 e IPv6) y de la captura real (I1-03 criterio 1).
func TestFixtures(t *testing.T) {
	for capPath, expPath := range fixtures(t) {
		name := filepath.Base(filepath.Dir(capPath)) + "/" + filepath.Base(capPath)
		t.Run(name, func(t *testing.T) {
			ds, err := pcapread.ReadFile(capPath)
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(expPath)
			if err != nil {
				t.Fatal(err)
			}
			var exp expected
			if err := json.Unmarshal(b, &exp); err != nil {
				t.Fatal(err)
			}
			type tot struct {
				records, v4, v6 int
				bytes, packets  uint64
			}
			got := map[string]*tot{}
			d := New(Options{})
			for _, dg := range ds {
				src := dg.Src.Addr().String()
				res, err := d.Decode(src, dg.Payload)
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				if res.DroppedNoTemplate > 0 {
					t.Fatalf("dropped %d sets without template", res.DroppedNoTemplate)
				}
				g := got[src]
				if g == nil {
					g = &tot{}
					got[src] = g
				}
				for _, r := range res.Records {
					g.records++
					if r.SrcIP.Is4() {
						g.v4++
					} else {
						g.v6++
					}
					g.bytes += r.Bytes
					g.packets += r.Packets
					if !r.SrcIP.IsValid() || !r.DstIP.IsValid() || r.TS.IsZero() || r.FlowStart.After(r.TS) {
						t.Fatalf("incomplete record %+v", r)
					}
				}
			}
			for _, e := range exp.Exporters {
				g := got[e.ExporterIP]
				if g == nil {
					t.Fatalf("no records from %s", e.ExporterIP)
				}
				w := e.Totals
				if g.records != w.DataRecords || g.v4 != w.RecordsV4 || g.v6 != w.RecordsV6 || g.bytes != w.Bytes || g.packets != w.Packets {
					t.Fatalf("%s: got %+v want %+v", e.ExporterIP, *g, w)
				}
			}
		})
	}
}

// TestRealNATFields comprueba que la captura real trae IE 225/226 y tiempos absolutos.
func TestRealNATFields(t *testing.T) {
	ds, err := pcapread.ReadFile(repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	d := New(Options{})
	var postDst, postSrc int
	for _, dg := range ds {
		res, err := d.Decode("x", dg.Payload)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Records {
			if r.PostNATDstIP.IsValid() && r.PostNATDstIP != r.DstIP {
				postDst++
			}
			if r.PostNATSrcIP.IsValid() && r.PostNATSrcIP != r.SrcIP {
				postSrc++
			}
			if r.TS.Year() != 2026 {
				t.Fatalf("ts %v", r.TS)
			}
		}
	}
	if postDst == 0 || postSrc == 0 {
		t.Fatalf("post-NAT fields: dst=%d src=%d", postDst, postSrc)
	}
}

// TestDataBeforeTemplate retiene datos sin plantilla y los libera al llegar.
func TestDataBeforeTemplate(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	d := New(Options{Now: func() time.Time { return now }, PendingTTL: 10 * time.Second})
	tmpl := ipfix(1, set(2, templateRec(300, [][2]uint16{{8, 4}, {12, 4}, {1, 8}})))
	data := ipfix(2, set(300, append(append([]byte{10, 0, 0, 1, 8, 8, 8, 8}, u64(100)...), append([]byte{10, 0, 0, 2, 8, 8, 4, 4}, u64(5)...)...)))
	res, err := d.Decode("e", data)
	if err != nil || res.Pending != 1 || len(res.Records) != 0 {
		t.Fatalf("pending: %v %+v", err, res)
	}
	res, err = d.Decode("e", tmpl)
	if err != nil || len(res.Records) != 2 || res.Records[1].Bytes != 5 {
		t.Fatalf("released: %v %+v", err, res)
	}
	// Caducidad: datos sin plantilla más allá del TTL se descartan y cuentan.
	_, _ = d.Decode("e", ipfix(3, set(301, []byte{1, 2, 3, 4})))
	now = now.Add(11 * time.Second)
	res, _ = d.Decode("e", ipfix(4, nil))
	if res.DroppedNoTemplate != 1 {
		t.Fatalf("expired = %d", res.DroppedNoTemplate)
	}
}

func TestMalformedNoPanic(t *testing.T) {
	ds, err := pcapread.ReadFile(repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	d := New(Options{})
	p := ds[0].Payload
	for i := range p {
		_, _ = d.Decode("x", p[:i])
		q := append([]byte(nil), p...)
		q[i] ^= 0xff
		_, _ = d.Decode("y", q)
	}
}

func FuzzDecode(f *testing.F) {
	if ds, err := pcapread.ReadFile(repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng")); err == nil {
		for _, dg := range ds[:4] {
			f.Add(dg.Payload)
		}
	}
	if ds, err := pcapread.ReadFile(repo("tools", "flowsim", "fixtures", "sim", "normal", "v9.hfsim.gz")); err == nil {
		for _, dg := range ds[:4] {
			f.Add(dg.Payload)
		}
	}
	d := New(Options{PendingMaxSets: 8})
	f.Fuzz(func(_ *testing.T, b []byte) {
		_, _ = d.Decode("fuzz", b)
	})
}

// ---------------------------------------------------------------- helpers

func u64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

func templateRec(id uint16, fields [][2]uint16) []byte {
	b := binary.BigEndian.AppendUint16(nil, id)
	b = binary.BigEndian.AppendUint16(b, uint16(len(fields))) //nolint:gosec // test
	for _, f := range fields {
		b = binary.BigEndian.AppendUint16(b, f[0])
		b = binary.BigEndian.AppendUint16(b, f[1])
	}
	return b
}

func set(id uint16, body []byte) []byte {
	b := binary.BigEndian.AppendUint16(nil, id)
	b = binary.BigEndian.AppendUint16(b, uint16(4+len(body))) //nolint:gosec // test
	return append(b, body...)
}

func ipfix(seq uint32, sets []byte) []byte {
	b := binary.BigEndian.AppendUint16(nil, 10)
	b = binary.BigEndian.AppendUint16(b, uint16(16+len(sets))) //nolint:gosec // test
	b = binary.BigEndian.AppendUint32(b, 1_800_000_000)
	b = binary.BigEndian.AppendUint32(b, seq)
	b = binary.BigEndian.AppendUint32(b, 0)
	return append(b, sets...)
}

// TestRealCaptureRebase: pcapread.Rebase desplaza la captura real al presente
// sin cambiar nada más: mismos registros y bytes, y cada tiempo (ts, inicio y
// hora de exportación) se mueve exactamente lo mismo.
func TestRealCaptureRebase(t *testing.T) {
	ds, err := pcapread.ReadFile(repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	first := append([]byte(nil), ds[0].Payload...)
	shifted := pcapread.Rebase(ds, start)
	delta := start.Sub(ds[0].Time.Truncate(time.Second))
	if !shifted[0].Time.Truncate(time.Second).Equal(start) {
		t.Fatalf("primer datagrama en %v, quiero %v", shifted[0].Time, start)
	}
	orig, moved := New(Options{}), New(Options{})
	var n int
	for i := range ds {
		a, err := orig.Decode("x", ds[i].Payload)
		if err != nil {
			t.Fatal(err)
		}
		b, err := moved.Decode("x", shifted[i].Payload)
		if err != nil {
			t.Fatal(err)
		}
		if !b.Header.ExportTime.Equal(a.Header.ExportTime.Add(delta)) {
			t.Fatalf("datagrama %d: export %v, quiero %v", i, b.Header.ExportTime, a.Header.ExportTime.Add(delta))
		}
		if len(a.Records) != len(b.Records) {
			t.Fatalf("datagrama %d: %d registros, quiero %d", i, len(b.Records), len(a.Records))
		}
		for j := range a.Records {
			ra, rb := a.Records[j], b.Records[j]
			if !rb.TS.Equal(ra.TS.Add(delta)) || !rb.FlowStart.Equal(ra.FlowStart.Add(delta)) {
				t.Fatalf("registro %d/%d: ts %v inicio %v; quiero %v / %v", i, j, rb.TS, rb.FlowStart, ra.TS.Add(delta), ra.FlowStart.Add(delta))
			}
			ra.TS, ra.FlowStart, rb.TS, rb.FlowStart = time.Time{}, time.Time{}, time.Time{}, time.Time{}
			if !reflect.DeepEqual(ra, rb) {
				t.Fatalf("registro %d/%d cambió además del tiempo:\n%+v\n%+v", i, j, ra, rb)
			}
			n++
		}
	}
	if n < 2500 {
		t.Fatalf("solo %d registros comparados", n)
	}
	if !bytes.Equal(ds[0].Payload, first) {
		t.Fatal("Rebase debe copiar los datagramas, no modificarlos")
	}
}
