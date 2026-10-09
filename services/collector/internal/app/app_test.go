package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
	"github.com/hcdestroyer/horus-flow/services/collector/api"
)

const inventoryYAML = `
exporters:
  - tenant_id: 0192e000-0000-7000-8000-000000000001
    router_id: 0192e333-0000-7000-8000-000000000033
    site_id: 0192e222-0000-7000-8000-000000000022
    tunnel_ip: 10.255.3.17
client_prefixes:
  - {id: 0192e444-0000-7000-8000-000000000044, tenant_id: 0192e000-0000-7000-8000-000000000001, site_id: 0192e222-0000-7000-8000-000000000022, realm_id: 0192e111-0000-7000-8000-000000000011, prefix: 10.20.0.0/16, role: customers}
`

type fakeSink struct {
	mu   sync.Mutex
	msgs []*nats.Msg
	fail bool
}

func (f *fakeSink) PublishMsg(_ context.Context, m *nats.Msg) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("bus down")
	}
	f.msgs = append(f.msgs, m)
	return nil
}

func (f *fakeSink) bySubjectPrefix(p string) []*nats.Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*nats.Msg
	for _, m := range f.msgs {
		if len(m.Subject) >= len(p) && m.Subject[:len(p)] == p {
			out = append(out, m)
		}
	}
	return out
}

type fakeKV struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *fakeKV) Get(_ context.Context, key string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m[key], nil
}

func (k *fakeKV) Put(_ context.Context, key string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string][]byte{}
	}
	k.m[key] = v
	return nil
}

func newEngine(t *testing.T, sink Sink, kv KV) (*Engine, *flowinv.Store, *Metrics) {
	t.Helper()
	s, err := flowinv.Parse([]byte(inventoryYAML))
	if err != nil {
		t.Fatal(err)
	}
	inv := flowinv.NewStore(s)
	m := NewMetrics(nil)
	e := NewEngine(EngineOptions{Workers: 2, QueueDatagrams: 4096, BatchMaxRecords: 500, BatchMaxAge: time.Second,
		BufferBytes: 64 << 20, PendingTTL: 30 * time.Second, CollectorID: "test",
		State: StateOptions{SilentAfter: 2 * time.Minute, LossThreshold: 0.01, LossWindow: 5 * time.Minute,
			ClockSkew: 30 * time.Second, Interval: time.Hour}}, inv, sink, kv, m, slog.New(slog.DiscardHandler))
	return e, inv, m
}

func realDatagrams(t *testing.T) []pcapread.Datagram {
	t.Helper()
	ds, err := pcapread.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "mikrotik-real", "ipfix-nat-20s.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

// TestRealCapture reproduce la captura real: todos los registros salen en
// lotes ≤ 500 salvo los 8 del túnel, y se miden los 23 saltos de secuencia
// (3 247 registros perdidos) que fija expected.json.
func TestRealCapture(t *testing.T) {
	sink, kv := &fakeSink{}, &fakeKV{}
	e, inv, _ := newEngine(t, sink, kv)
	w := NewWorker(inv, e.Batcher, e.States, e.m, 30*time.Second)
	ds := realDatagrams(t)
	for _, d := range ds {
		// El exportador manda con su reloj: se reproduce con el tiempo de la captura.
		w.Handle(Datagram{Src: d.Src, Payload: d.Payload, At: d.Time})
	}
	e.Batcher.Flush(time.Now(), true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.Pub.Run(ctx, time.Second); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for n, _ := e.Pub.Pending(); n > 0 && time.Now().Before(deadline); n, _ = e.Pub.Pending() {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	var records int
	for _, m := range sink.bySubjectPrefix(flowbus.SubjectBatchPrefix) {
		fb, err := flowpb.UnmarshalFlowBatch(m.Data)
		if err != nil {
			t.Fatal(err)
		}
		if len(fb.Records) > 500 || len(m.Data) > 512<<10 {
			t.Fatalf("batch too big: %d records, %d bytes", len(fb.Records), len(m.Data))
		}
		if m.Header.Get(flowbus.HeaderTenant) != fb.TenantID || m.Header.Get(flowbus.HeaderMsgID) != fb.BatchID {
			t.Fatal("headers do not match batch")
		}
		for _, r := range fb.Records {
			if r.SrcIP == netip.MustParseAddr("10.255.3.17") || r.DstIP == netip.MustParseAddr("10.255.3.17") {
				t.Fatal("tunnel flow published")
			}
		}
		records += len(fb.Records)
	}
	if records != 2596-8 {
		t.Fatalf("records = %d, want %d", records, 2596-8)
	}
	var gaps, lost uint64
	for _, tr := range w.seq {
		gaps += tr.Gaps
		lost += tr.Lost
	}
	if gaps != 23 || lost != 3247 {
		t.Fatalf("sequence gaps=%d lost=%d, want 23/3247", gaps, lost)
	}
}

// TestUnknownExporter descarta y cuenta; no publica flujos.
func TestUnknownExporter(t *testing.T) {
	sink := &fakeSink{}
	e, inv, m := newEngine(t, sink, nil)
	w := NewWorker(inv, e.Batcher, e.States, m, time.Second)
	for _, d := range realDatagrams(t)[:5] {
		w.Handle(Datagram{Src: netip.AddrPortFrom(netip.MustParseAddr("198.51.100.7"), 4739), Payload: d.Payload, At: time.Now()})
	}
	e.Batcher.Flush(time.Now(), true)
	if n, _ := e.Pub.Pending(); n != 0 {
		t.Fatalf("published %d batches from unknown exporter", n)
	}
	e.States.Evaluate(context.Background(), false)
	ev := sink.bySubjectPrefix(flowbus.TypeExporterUnregistered)
	if len(ev) != 1 || ev[0].Header.Get(flowbus.HeaderTenant) != flowbus.TenantPlatform {
		t.Fatalf("unregistered events = %d", len(ev))
	}
}

// TestBusDownBuffersThenDrops: con el bus caído no se pierde nada mientras
// cabe en el búfer; al superarlo se descarta lo nuevo y se publica data_gap.
func TestBusDownBuffersThenDrops(t *testing.T) {
	sink := &fakeSink{fail: true}
	m := NewMetrics(nil)
	p := NewPublisher(sink, 1000, "c1", m, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx, time.Second); close(done) }()
	for i := range 3 {
		ok := p.Enqueue(&nats.Msg{Subject: "s", Data: make([]byte, 400)}, 10)
		if want := i < 2; ok != want {
			t.Fatalf("enqueue %d = %v", i, ok)
		}
	}
	time.Sleep(150 * time.Millisecond)
	sink.mu.Lock()
	sink.fail = false
	sink.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	for len(sink.bySubjectPrefix(flowbus.TypeCollectorDataGap)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if got := len(sink.bySubjectPrefix("s")); got != 2 {
		t.Fatalf("published %d buffered batches, want 2", got)
	}
	gap := sink.bySubjectPrefix(flowbus.TypeCollectorDataGap)
	if len(gap) != 1 {
		t.Fatal("no data_gap event")
	}
	var env flowbus.Envelope
	_ = json.Unmarshal(gap[0].Data, &env)
	var data map[string]any
	_ = json.Unmarshal(env.Data, &data)
	if data["dropped_batches"] != "1" || data["dropped_records_estimated"] != "10" {
		t.Fatalf("data_gap %v", data)
	}
}

// TestExporterStates recorre pendiente → exportando → con pérdidas → silencioso → exportando.
func TestExporterStates(t *testing.T) {
	sink, kv := &fakeSink{}, &fakeKV{}
	e, inv, _ := newEngine(t, sink, kv)
	st := e.States
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return now }
	exp := &inv.Load().Data().Exporters[0]
	state := func() string {
		var fe api.FlowExporter
		_ = json.Unmarshal(kv.m[exp.RouterID.String()], &fe)
		return fe.State
	}
	st.Evaluate(context.Background(), false)
	if state() != api.StatePendingConfiguration {
		t.Fatalf("initial = %s", state())
	}
	st.Observe(exp, Observation{At: now, Records: 1000, FlowSource: "ipfix"})
	st.Evaluate(context.Background(), true) // primer lote: evaluación inmediata
	if state() != api.StateExporting {
		t.Fatalf("after first batch = %s", state())
	}
	now = now.Add(10 * time.Second)
	st.Observe(exp, Observation{At: now, Records: 1000, Lost: 50, Gap: true, FlowSource: "ipfix"})
	st.Evaluate(context.Background(), false)
	if state() != api.StateLossy {
		t.Fatalf("with 2.4%% loss = %s", state())
	}
	now = now.Add(6 * time.Minute) // fuera de la ventana de pérdida de 5 min
	st.Evaluate(context.Background(), false)
	st.Evaluate(context.Background(), false) // histéresis: una sola transición
	if state() != api.StateSilent {
		t.Fatalf("after 6 min = %s", state())
	}
	if n := len(sink.bySubjectPrefix(flowbus.TypeExporterSilent + ".")); n != 1 {
		t.Fatalf("silent events = %d", n)
	}
	now = now.Add(time.Second)
	st.Observe(exp, Observation{At: now, Records: 10, FlowSource: "ipfix"})
	st.Evaluate(context.Background(), true)
	if state() != api.StateExporting || len(sink.bySubjectPrefix(flowbus.TypeExporterRecovered+".")) != 1 {
		t.Fatalf("recovered = %s", state())
	}
	if n := len(sink.bySubjectPrefix(flowbus.TypeExporterStateChanged + ".")); n != 4 {
		t.Fatalf("state_changed events = %d, want 4", n)
	}
	now = now.Add(time.Second)
	st.Observe(exp, Observation{At: now, Records: 10, Skew: 45 * time.Second, FlowSource: "ipfix"})
	st.Evaluate(context.Background(), false)
	if state() != api.StateClockSkew {
		t.Fatalf("skew = %s", state())
	}
}
