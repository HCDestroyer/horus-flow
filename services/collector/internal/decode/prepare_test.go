package decode

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
)

// runPlan decodifica un datagrama con Prepare y los Job en goroutines (en
// orden inverso, para que el orden de ejecución no coincida con el de
// llegada) y devuelve lo mismo que Decode.
func runPlan(d *Decoder, src string, p []byte) (Result, uint32, error) {
	pl := d.Prepare(src, p)
	var wg sync.WaitGroup
	for i := len(pl.Jobs) - 1; i >= 0; i-- {
		wg.Add(1)
		go func(j *Job) { defer wg.Done(); j.Run() }(pl.Jobs[i])
	}
	wg.Wait()
	res, err := pl.Finish()
	return res, pl.Sampling, err
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// equivalent compara Decode con Prepare + Job + Finish sobre una secuencia de
// datagramas de un mismo exportador (plantillas que llegan antes o después de
// sus datos, retenidos que caducan, opciones de muestreo).
func equivalent(t *testing.T, srcs []string, ds [][]byte) {
	t.Helper()
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	serial := New(Options{PendingMaxSets: 8, PendingTTL: 2 * time.Second, Now: clock})
	planned := New(Options{PendingMaxSets: 8, PendingTTL: 2 * time.Second, Now: clock})
	for i, p := range ds {
		src := srcs[i%len(srcs)]
		want, werr := serial.Decode(src, p)
		got, samp, gerr := runPlan(planned, src, p)
		if errString(werr) != errString(gerr) {
			t.Fatalf("datagram %d: error %q, Decode %q", i, gerr, werr)
		}
		if len(want.Records) == 0 {
			want.Records = nil
		}
		if len(got.Records) == 0 {
			got.Records = nil
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("datagram %d: Prepare != Decode\n got  %+v\n want %+v", i, got, want)
		}
		if samp != serial.Sampling(src) {
			t.Fatalf("datagram %d: sampling %d, Decode %d", i, samp, serial.Sampling(src))
		}
		now = now.Add(500 * time.Millisecond)
	}
}

// TestPrepareEquivalentFixtures: la captura real y los fixtures del
// simulador (IPFIX y v9) dan lo mismo por Decode que por Prepare.
func TestPrepareEquivalentFixtures(t *testing.T) {
	for capPath := range fixtures(t) {
		ds, err := pcapread.ReadFile(capPath)
		if err != nil {
			t.Fatal(err)
		}
		var ps [][]byte
		var srcs []string
		for _, d := range ds {
			ps = append(ps, d.Payload)
			srcs = append(srcs, d.Src.Addr().String())
		}
		// Un solo exportador por fixture salvo excepciones: se respeta su IP.
		t.Run(capPath, func(t *testing.T) { equivalentPerDatagram(t, srcs, ps) })
	}
}

func equivalentPerDatagram(t *testing.T, srcs []string, ds [][]byte) {
	t.Helper()
	serial := New(Options{})
	planned := New(Options{})
	for i, p := range ds {
		want, werr := serial.Decode(srcs[i], p)
		got, _, gerr := runPlan(planned, srcs[i], p)
		if errString(werr) != errString(gerr) || len(want.Records) != len(got.Records) || want.DataRecords != got.DataRecords ||
			want.Templates != got.Templates || want.Pending != got.Pending || want.DroppedNoTemplate != got.DroppedNoTemplate {
			t.Fatalf("datagram %d differs", i)
		}
		if len(want.Records) > 0 && !reflect.DeepEqual(want.Records, got.Records) {
			t.Fatalf("datagram %d: records differ", i)
		}
	}
}

// TestPrepareTemplateAfterData: datos antes que su plantilla (retenidos) y
// una plantilla nueva con el mismo ID que cambia los campos a mitad.
func TestPrepareTemplateAfterData(t *testing.T) {
	tA := templateRec(300, [][2]uint16{{ieSrcIPv4, 4}, {ieDstIPv4, 4}, {ieOctetDeltaCount, 8}})
	tB := templateRec(300, [][2]uint16{{ieDstIPv4, 4}, {ieSrcIPv4, 4}, {iePacketDeltaCount, 4}})
	recA := append([]byte{10, 0, 0, 1, 8, 8, 8, 8}, u64(100)...)
	recB := []byte{1, 1, 1, 1, 10, 0, 0, 2, 0, 0, 0, 7}
	var ds [][]byte
	seq := uint32(0)
	add := func(sets ...[]byte) {
		ds = append(ds, ipfixDatagram(seq, sets...))
		seq++
	}
	add(dset(300, recA, recA))                         // sin plantilla: retenido
	add(dset(2, tA), dset(300, recA))                  // plantilla: libera el retenido
	add(dset(300, recA), dset(2, tB))                  // datos con A y luego plantilla B
	add(dset(300, recB, recB, recB))                   // datos con B
	add(dset(300, recB), dset(2, tA), dset(300, recA)) // B, cambio a A y datos A
	add(dset(301, recA))                               // plantilla desconocida: caduca
	add(dset(300, recA))
	add(dset(300, recA))
	add(dset(300, recA))
	add(dset(300, recA))
	add(dset(300, recA))
	equivalent(t, []string{"10.255.0.1"}, ds)
}

func ipfixDatagram(seq uint32, sets ...[]byte) []byte {
	var body []byte
	for _, s := range sets {
		body = append(body, s...)
	}
	b := make([]byte, 16, 16+len(body))
	binary.BigEndian.PutUint16(b, VersionIPFIX)
	binary.BigEndian.PutUint16(b[2:], uint16(16+len(body))) //nolint:gosec // test
	binary.BigEndian.PutUint32(b[4:], 1760000000)
	binary.BigEndian.PutUint32(b[8:], seq)
	binary.BigEndian.PutUint32(b[12:], 7)
	return append(b, body...)
}

func dset(id uint16, recs ...[]byte) []byte {
	var body []byte
	for _, r := range recs {
		body = append(body, r...)
	}
	b := binary.BigEndian.AppendUint16(nil, id)
	b = binary.BigEndian.AppendUint16(b, uint16(4+len(body))) //nolint:gosec // test
	return append(b, body...)
}

// FuzzPrepareEquivalence: con datagramas arbitrarios (la entrada se trocea en
// varios datagramas del mismo exportador, más plantillas y datos de la
// captura real), Prepare + Job concurrentes + Finish da exactamente lo mismo
// que Decode, y nunca entra en pánico.
func FuzzPrepareEquivalence(f *testing.F) {
	if ds, err := pcapread.ReadFile(repo("tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng")); err == nil {
		for _, dg := range ds[:6] {
			f.Add(dg.Payload, uint8(3))
		}
	}
	if ds, err := pcapread.ReadFile(repo("tools", "flowsim", "fixtures", "sim", "normal", "v9.hfsim.gz")); err == nil {
		for _, dg := range ds[:4] {
			f.Add(dg.Payload, uint8(2))
		}
	}
	f.Fuzz(func(t *testing.T, b []byte, parts uint8) {
		n := int(parts%5) + 1
		var ds [][]byte
		step := len(b)/n + 1
		for i := 0; i < len(b); i += step {
			ds = append(ds, b[i:min(i+step, len(b))])
		}
		ds = append(ds, b) // y entero
		equivalent(t, []string{"fuzz", fmt.Sprint(n)}, ds)
	})
}
