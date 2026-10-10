package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/spool"
)

func batchMsg(i int) *nats.Msg {
	m := nats.NewMsg("horus.telemetry.flows.batch.r1")
	m.Header.Set(flowbus.HeaderMsgID, fmt.Sprintf("b-%05d", i))
	m.Data = make([]byte, 300)
	return m
}

type spoolRig struct {
	p    *Publisher
	sink *fakeSink
	m    *Metrics
	stop func()
}

func startSpooled(t *testing.T, dir string, sink *fakeSink, maxBytes int64, o spool.Options) *spoolRig {
	t.Helper()
	m := NewMetrics(nil)
	p := NewPublisher(sink, 1<<20, "c1", m, slog.New(slog.DiscardHandler))
	o.Dir, o.OnDrop = dir, p.SpoolDropped
	if o.MaxBytes == 0 {
		o.MaxBytes = maxBytes
	}
	s, err := spool.Open(o)
	if err != nil {
		t.Fatal(err)
	}
	p.UseSpool(s, 0.5)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx, 200*time.Millisecond); close(done) }()
	return &spoolRig{p: p, sink: sink, m: m, stop: func() { cancel(); <-done }}
}

func (f *fakeSink) setFail(v bool) {
	f.mu.Lock()
	f.fail = v
	f.mu.Unlock()
}

func ids(msgs []*nats.Msg) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Header.Get(flowbus.HeaderMsgID))
	}
	return out
}

func waitBatches(t *testing.T, sink *fakeSink, n int) []string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if got := ids(sink.bySubjectPrefix(flowbus.SubjectBatchPrefix)); len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := ids(sink.bySubjectPrefix(flowbus.SubjectBatchPrefix))
	t.Fatalf("published %d batches, want %d", len(got), n)
	return nil
}

func wantOrder(t *testing.T, got []string, from, to int) {
	t.Helper()
	if len(got) != to-from {
		t.Fatalf("got %d batches, want %d", len(got), to-from)
	}
	for i, id := range got {
		if want := fmt.Sprintf("b-%05d", from+i); id != want {
			t.Fatalf("batch %d = %s, want %s (order or duplicate)", i, id, want)
		}
	}
}

// TestSpoolWhileBusDown: con el bus caído los lotes van al spool (también los
// que esperaban en memoria) y al volver se reenvían una vez cada uno, en
// orden y con el mismo Nats-Msg-Id; después se vuelve al búfer en memoria.
func TestSpoolWhileBusDown(t *testing.T) {
	sink := &fakeSink{fail: true}
	r := startSpooled(t, t.TempDir(), sink, 64<<20, spool.Options{})
	defer r.stop()
	for i := range 200 {
		if !r.p.Enqueue(batchMsg(i), 10) {
			t.Fatalf("batch %d rejected", i)
		}
		if i == 0 {
			time.Sleep(300 * time.Millisecond) // el primer fallo pasa a modo spool
		}
	}
	if n, _ := r.p.Pending(); n != 0 {
		t.Fatalf("%d batches left in memory while the bus is down", n)
	}
	if testutil.ToFloat64(r.m.SpoolWritten) != 200 {
		t.Fatalf("spool written = %v", testutil.ToFloat64(r.m.SpoolWritten))
	}
	sink.setFail(false)
	wantOrder(t, waitBatches(t, sink, 200), 0, 200)
	r.p.Enqueue(batchMsg(200), 10)
	wantOrder(t, waitBatches(t, sink, 201), 0, 201)
	r.p.mu.Lock()
	spooling := r.p.spooling
	r.p.mu.Unlock()
	if spooling {
		t.Fatal("still spooling after the spool drained")
	}
	if testutil.ToFloat64(r.m.SpoolReplayed) != 200 {
		t.Fatalf("replayed = %v", testutil.ToFloat64(r.m.SpoolReplayed))
	}
}

// TestSpoolAcrossRestart: el collector se para (o muere) con el bus caído; el
// siguiente arranque reenvía lo del spool en orden. Tras un corte sin cerrar
// el spool (kill -9 simulado) se reenvía como mucho el último lote ya
// publicado (JetStream lo descarta por Nats-Msg-Id) y nada se pierde.
func TestSpoolAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	sink := &fakeSink{fail: true}
	r := startSpooled(t, dir, sink, 64<<20, spool.Options{})
	r.p.Enqueue(batchMsg(0), 10)
	time.Sleep(300 * time.Millisecond)
	for i := 1; i < 120; i++ {
		r.p.Enqueue(batchMsg(i), 10)
	}
	r.stop() // apagado ordenado con el bus caído: nada se pierde

	sink2 := &fakeSink{}
	r2 := startSpooled(t, dir, sink2, 64<<20, spool.Options{})
	got := waitBatches(t, sink2, 120)
	r2.stop()
	wantOrder(t, got, 0, 120)

	// kill -9 a mitad del reenvío: un spool abierto sin Close.
	dir3 := t.TempDir()
	s, _ := spool.Open(spool.Options{Dir: dir3})
	for i := range 50 {
		_ = s.Append(batchMsg(i), 10)
	}
	for range 20 {
		s.Peek()
		s.Commit()
	}
	s.Peek() // el lote 20 se está publicando cuando muere
	sink3 := &fakeSink{}
	r3 := startSpooled(t, dir3, sink3, 64<<20, spool.Options{})
	defer r3.stop()
	wantOrder(t, waitBatches(t, sink3, 30), 20, 50)
}

// TestSpoolFullDataGap: el spool lleno descarta lo más antiguo, lo cuenta y
// el evento data_gap lo refleja al volver el bus.
func TestSpoolFullDataGap(t *testing.T) {
	sink := &fakeSink{fail: true}
	one := int64(len(batchMsg(0).Data)) + 64
	r := startSpooled(t, t.TempDir(), sink, 0, spool.Options{MaxBytes: 1 << 16, SegmentBytes: 1 << 13})
	defer r.stop()
	r.p.Enqueue(batchMsg(0), 10)
	time.Sleep(300 * time.Millisecond)
	n := int(3*(1<<16)/one) + 1
	for i := 1; i < n; i++ {
		r.p.Enqueue(batchMsg(i), 10)
	}
	dropped := int(testutil.ToFloat64(r.m.SpoolDropped.WithLabelValues(spool.DropFull)))
	if dropped == 0 {
		t.Fatal("full spool dropped nothing")
	}
	sink.setFail(false)
	got := waitBatches(t, sink, n-dropped)
	wantOrder(t, got, dropped, n)
	deadline := time.Now().Add(5 * time.Second)
	for len(sink.bySubjectPrefix(flowbus.TypeCollectorDataGap)) == 0 && time.Now().Before(deadline) {
		r.p.Enqueue(batchMsg(n), 10)
		n++
		time.Sleep(50 * time.Millisecond)
	}
	gap := sink.bySubjectPrefix(flowbus.TypeCollectorDataGap)
	if len(gap) != 1 {
		t.Fatal("no data_gap event")
	}
	var env flowbus.Envelope
	_ = json.Unmarshal(gap[0].Data, &env)
	var data map[string]any
	_ = json.Unmarshal(env.Data, &data)
	if data["reason"] != "spool_full" || data["dropped_batches"] != fmt.Sprint(dropped) {
		t.Fatalf("data_gap %v (dropped %d)", data, dropped)
	}
	if testutil.ToFloat64(r.m.Dropped.WithLabelValues(DropSpoolFull)) != float64(dropped*10) {
		t.Fatal("dropped records not counted")
	}
}
