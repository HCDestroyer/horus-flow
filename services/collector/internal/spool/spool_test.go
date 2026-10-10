package spool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type drops struct {
	mu sync.Mutex
	b  map[string]int
	r  map[string]int
}

func (d *drops) fn(reason string, batches, records int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.b == nil {
		d.b, d.r = map[string]int{}, map[string]int{}
	}
	d.b[reason] += batches
	d.r[reason] += records
}

func (d *drops) batches(reason string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.b[reason]
}

func msg(i int) *nats.Msg {
	m := nats.NewMsg("horus.telemetry.flows.batch.r1")
	m.Header.Set("Nats-Msg-Id", fmt.Sprintf("batch-%06d", i))
	m.Header.Set("Horus-Tenant", "t1")
	m.Data = []byte(fmt.Sprintf("payload-%06d-%s", i, string(make([]byte, 200))))
	return m
}

func open(t *testing.T, dir string, o Options) (*Spool, *drops) {
	t.Helper()
	d := &drops{}
	o.Dir, o.OnDrop = dir, d.fn
	s, err := Open(o)
	if err != nil {
		t.Fatal(err)
	}
	return s, d
}

// drain lee y confirma todo; devuelve los Nats-Msg-Id en orden.
func drain(t *testing.T, s *Spool) []string {
	t.Helper()
	var ids []string
	for {
		m, n, ok := s.Peek()
		if !ok {
			return ids
		}
		if n != 10 {
			t.Fatalf("records = %d", n)
		}
		ids = append(ids, m.Header.Get("Nats-Msg-Id"))
		s.Commit()
	}
}

func seqIDs(from, to int) []string {
	var out []string
	for i := from; i < to; i++ {
		out = append(out, fmt.Sprintf("batch-%06d", i))
	}
	return out
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d batches, want %d (first %v)", len(got), len(want), got[:min(3, len(got))])
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("batch %d = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestRoundTripAcrossSegmentsAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir, Options{SegmentBytes: 4096, MaxBytes: 1 << 20})
	for i := range 100 {
		if err := s.Append(msg(i), 10); err != nil {
			t.Fatal(err)
		}
	}
	if st := s.Stats(); st.Batches != 100 || st.Records != 1000 || st.Segments < 5 {
		t.Fatalf("stats %+v", st)
	}
	// Se reenvían 30 y el proceso para.
	for range 30 {
		m, _, ok := s.Peek()
		if !ok {
			t.Fatal("empty")
		}
		_ = m
		s.Commit()
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, _ = open(t, dir, Options{SegmentBytes: 4096, MaxBytes: 1 << 20})
	if st := s.Stats(); st.Batches != 70 || st.Records != 700 {
		t.Fatalf("after restart %+v", st)
	}
	equal(t, drain(t, s), seqIDs(30, 100))
	if !s.Empty() {
		t.Fatal("not empty")
	}
	// Los segmentos leídos se borran.
	segs, _ := filepath.Glob(filepath.Join(dir, "*.seg"))
	if len(segs) != 1 {
		t.Fatalf("%d segments left", len(segs))
	}
	// Mensaje entero: cabeceras y cuerpo.
	if err := s.Append(msg(7), 10); err != nil {
		t.Fatal(err)
	}
	m, _, _ := s.Peek()
	if m.Subject != msg(7).Subject || string(m.Data) != string(msg(7).Data) || m.Header.Get("Horus-Tenant") != "t1" {
		t.Fatalf("message altered: %+v", m)
	}
}

// TestCorruptSegment: un registro dañado en mitad de un segmento se salta y se
// cuenta; el resto se reenvía en orden.
func TestCorruptSegment(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir, Options{SegmentBytes: 1 << 20, MaxBytes: 4 << 20})
	for i := range 20 {
		if err := s.Append(msg(i), 10); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	segs, _ := filepath.Glob(filepath.Join(dir, "*.seg"))
	b, err := os.ReadFile(segs[0])
	if err != nil {
		t.Fatal(err)
	}
	recLen := len(b) / 20
	b[recLen*7+recLen/2] ^= 0xff // cuerpo del registro 7
	b[recLen*12+1] ^= 0xff       // magia del registro 12
	if err := os.WriteFile(segs[0], b, 0o640); err != nil {
		t.Fatal(err)
	}
	s, d := open(t, dir, Options{SegmentBytes: 1 << 20, MaxBytes: 4 << 20})
	got := drain(t, s)
	want := append(append(seqIDs(0, 7), seqIDs(8, 12)...), seqIDs(13, 20)...)
	equal(t, got, want)
	if d.batches(DropCorrupt) != 2 {
		t.Fatalf("corrupt drops = %d, want 2", d.batches(DropCorrupt))
	}
}

// TestTornTail: un registro a medio escribir al final (corte durante la
// escritura) se trunca al abrir y no tapa lo que se escribe después.
func TestTornTail(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir, Options{})
	for i := range 5 {
		_ = s.Append(msg(i), 10)
	}
	_ = s.Close()
	segs, _ := filepath.Glob(filepath.Join(dir, "*.seg"))
	rec := encode(msg(5), 10, time.Now())
	f, _ := os.OpenFile(segs[0], os.O_WRONLY|os.O_APPEND, 0)
	_, _ = f.Write(rec[:len(rec)/2])
	_ = f.Close()
	s, d := open(t, dir, Options{})
	if st := s.Stats(); st.Batches != 5 {
		t.Fatalf("pending %d", st.Batches)
	}
	_ = s.Append(msg(6), 10)
	equal(t, drain(t, s), append(seqIDs(0, 5), seqIDs(6, 7)...))
	if d.batches(DropCorrupt) != 0 {
		t.Fatal("torn tail counted as corrupt")
	}
}

// TestFullDropsOldest: al superar MaxBytes se descarta el segmento más
// antiguo y se cuenta; lo que queda es lo más nuevo, en orden.
func TestFullDropsOldest(t *testing.T) {
	dir := t.TempDir()
	one := int64(len(encode(msg(0), 10, time.Now())))
	s, d := open(t, dir, Options{SegmentBytes: one * 10, MaxBytes: one * 40})
	for i := range 100 {
		if err := s.Append(msg(i), 10); err != nil {
			t.Fatal(err)
		}
	}
	st := s.Stats()
	if st.Bytes > one*40 {
		t.Fatalf("spool %d bytes > max %d", st.Bytes, one*40)
	}
	dropped := d.batches(DropFull)
	if dropped == 0 || dropped+st.Batches != 100 {
		t.Fatalf("dropped %d + pending %d != 100", dropped, st.Batches)
	}
	equal(t, drain(t, s), seqIDs(dropped, 100))
}

// TestDiskFull: ENOSPC al escribir descarta lo más antiguo y reintenta; sin
// nada que descartar, el lote nuevo se descarta y se cuenta.
func TestDiskFull(t *testing.T) {
	dir := t.TempDir()
	one := int64(len(encode(msg(0), 10, time.Now())))
	full := false
	var mu sync.Mutex
	hook := func(f *os.File, b []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		if full {
			full = false // solo el primer intento: tras liberar un segmento hay sitio
			n, _ := f.Write(b[:len(b)/3])
			return n, syscall.ENOSPC
		}
		return f.Write(b)
	}
	d := &drops{}
	s, err := Open(Options{Dir: dir, SegmentBytes: one * 5, MaxBytes: one * 1000, OnDrop: d.fn, writeHook: hook})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		_ = s.Append(msg(i), 10)
	}
	mu.Lock()
	full = true
	mu.Unlock()
	if err := s.Append(msg(20), 10); err != nil {
		t.Fatal(err)
	}
	if d.batches(DropFull) != 5 {
		t.Fatalf("dropped %d, want the oldest segment (5)", d.batches(DropFull))
	}
	equal(t, drain(t, s), seqIDs(5, 21))

	// Un solo segmento y sin sitio: se pierde el lote nuevo, no el spool.
	s2dir := t.TempDir()
	d2 := &drops{}
	s2, _ := Open(Options{Dir: s2dir, OnDrop: d2.fn, writeHook: func(f *os.File, b []byte) (int, error) {
		return 0, syscall.ENOSPC
	}})
	if err := s2.Append(msg(0), 10); err == nil || d2.batches(DropWriteError) != 1 {
		t.Fatalf("err=%v write_error=%d", err, d2.batches(DropWriteError))
	}
}

// TestKill9WhileWriting: un proceso que escribe en el spool muere con
// SIGKILL en un momento cualquiera; al abrir, lo pendiente es un prefijo
// contiguo de lo escrito (sin registros dañados ni huecos) y se sigue
// escribiendo detrás.
func TestKill9WhileWriting(t *testing.T) {
	if os.Getenv("HORUS_SPOOL_WRITER") != "" {
		return
	}
	for round := range 3 {
		dir := t.TempDir()
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperSpoolWriter$") //nolint:gosec // binario del test
		cmd.Env = append(os.Environ(), "HORUS_SPOOL_WRITER="+dir)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(150+round*120) * time.Millisecond)
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
		s, d := open(t, dir, Options{SegmentBytes: 64 << 10, MaxBytes: 1 << 30})
		ids := drain(t, s)
		if len(ids) == 0 {
			t.Fatal("nothing written before the kill")
		}
		// El escritor confirma (Commit) los 50 primeros: el resto, en orden.
		equal(t, ids, seqIDs(50, 50+len(ids)))
		if d.batches(DropCorrupt) != 0 {
			t.Fatalf("round %d: %d corrupt records after kill -9", round, d.batches(DropCorrupt))
		}
		if err := s.Append(msg(999999), 10); err != nil {
			t.Fatal(err)
		}
		if m, _, ok := s.Peek(); !ok || m.Header.Get("Nats-Msg-Id") != "batch-999999" {
			t.Fatal("append after recovery not readable")
		}
		t.Logf("round %d: %d batches recovered after SIGKILL", round, len(ids))
	}
}

// TestHelperSpoolWriter es el proceso que TestKill9WhileWriting mata.
func TestHelperSpoolWriter(t *testing.T) {
	dir := os.Getenv("HORUS_SPOOL_WRITER")
	if dir == "" {
		t.Skip("helper")
	}
	s, err := Open(Options{Dir: dir, SegmentBytes: 64 << 10, MaxBytes: 1 << 30, Fsync: -1})
	if err != nil {
		os.Exit(2)
	}
	for i := 0; ; i++ {
		_ = s.Append(msg(i), 10)
		if i == 99 {
			for range 50 {
				s.Peek()
				s.Commit()
			}
		}
	}
}
