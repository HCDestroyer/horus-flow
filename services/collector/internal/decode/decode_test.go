package decode

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
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
