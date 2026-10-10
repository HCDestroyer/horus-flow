package app

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/decode"
)

type memStateKV struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *memStateKV) Put(_ context.Context, key string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string][]byte{}
	}
	k.m[key] = append([]byte(nil), v...)
	return nil
}

func (k *memStateKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, key)
	return nil
}

func (k *memStateKV) List(_ context.Context, prefix string) (map[string][]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := map[string][]byte{}
	for key, v := range k.m {
		if strings.HasPrefix(key, prefix) {
			out[key] = v
		}
	}
	return out, nil
}

func stateEngine(t *testing.T, inv *flowinv.Store, kv StateKV, decodeWorkers int) (*Engine, *fakeSink, *Metrics) {
	t.Helper()
	sink := &fakeSink{}
	m := NewMetrics(nil)
	e := NewEngine(EngineOptions{Workers: 2, DecodeWorkers: decodeWorkers, QueueDatagrams: 4096, BatchMaxRecords: 500,
		BatchMaxAge: time.Hour, BufferBytes: 1 << 30, PendingTTL: 30 * time.Second, CollectorID: "st",
		State: StateOptions{SilentAfter: time.Hour, LossThreshold: 0.01, LossWindow: 5 * time.Minute, ClockSkew: time.Hour,
			Interval: time.Hour}}, inv, sink, nil, m, slog.New(slog.DiscardHandler))
	if kv != nil {
		if _, _, err := e.RestoreState(context.Background(), kv, time.Hour); err != nil {
			t.Fatal(err)
		}
		e.UsePersistence(kv)
	}
	return e, sink, m
}

func runAll(t *testing.T, e *Engine, ds []pcapread.Datagram) {
	t.Helper()
	for _, d := range ds {
		e.Submit(Datagram{Src: d.Src, Payload: d.Payload, At: d.Time})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func publishedRecords(t *testing.T, sink *fakeSink) int {
	t.Helper()
	n := 0
	for _, m := range sink.bySubjectPrefix(flowbus.SubjectBatchPrefix) {
		fb, err := flowpb.UnmarshalFlowBatch(m.Data)
		if err != nil {
			t.Fatal(err)
		}
		n += len(fb.Records)
	}
	return n
}

// TestCollectorStateAcrossRestart (D23): tras reiniciar el collector, las
// plantillas guardadas decodifican los datos que llegan antes de que el router
// las reenvíe (nada retenido ni descartado por falta de plantilla), y lo que el
// router envió mientras el collector estaba caído se mide con la secuencia
// guardada sin contarlo como pérdida del exportador. Con 1 y con 8 hilos.
func TestCollectorStateAcrossRestart(t *testing.T) {
	ds := realDatagrams(t)
	s, err := flowinv.Parse([]byte(inventoryYAML))
	if err != nil {
		t.Fatal(err)
	}
	inv := flowinv.NewStore(s)
	// Primer datagrama sin plantillas a partir de la mitad: ahí "vuelve" el collector.
	dec := decode.New(decode.Options{})
	cut, skip := -1, 5
	for i, d := range ds {
		r, _ := dec.Decode(d.Src.Addr().String(), d.Payload)
		if i > len(ds)/2 && cut < 0 && r.Templates == 0 && len(r.Records) > 0 {
			cut = i
		}
	}
	if cut < 0 {
		t.Fatal("no datagram without templates")
	}
	// Lo que la secuencia dice que falta entre el último datagrama antes de la
	// caída y el primero después (los datagramas no recibidos y cualquier
	// pérdida de la captura en medio).
	full := decode.New(decode.Options{})
	var tr seqTracker
	var downRecords uint64
	for i, d := range ds[:cut+skip+1] {
		r, _ := full.Decode(d.Src.Addr().String(), d.Payload)
		if i >= cut && i < cut+skip {
			continue
		}
		lost, _ := tr.observe(true, r.Header.Sequence, r.DataRecords+r.OptionsRecords)
		if i == cut+skip {
			downRecords = lost
		}
	}
	// Referencia: un collector sin reinicio con los mismos datagramas perdidos.
	ref, refSink, refM := stateEngine(t, inv, nil, 1)
	runAll(t, ref, append(append([]pcapread.Datagram(nil), ds[:cut]...), ds[cut+skip:]...))
	refRecs := publishedRecords(t, refSink)

	for _, n := range []int{1, 8} {
		kv := &memStateKV{}
		e1, sink1, _ := stateEngine(t, inv, kv, n)
		runAll(t, e1, ds[:cut])
		e2, sink2, m2 := stateEngine(t, inv, kv, n)
		runAll(t, e2, ds[cut+skip:])
		if got := testutil.ToFloat64(m2.Dropped.WithLabelValues(DropNoTemplate)); got != 0 {
			t.Fatalf("N=%d: %v sets dropped for lack of template after restart", n, got)
		}
		if got := publishedRecords(t, sink1) + publishedRecords(t, sink2); got != refRecs {
			t.Fatalf("N=%d: published %d records across the restart, %d without restart", n, got, refRecs)
		}
		down := testutil.ToFloat64(m2.DowntimeLost.WithLabelValues(inv.Load().Data().Exporters[0].RouterID.String()))
		if uint64(down) != downRecords {
			t.Fatalf("N=%d: downtime lost %v, want %d", n, down, downRecords)
		}
		// La pérdida del exportador (huecos de secuencia) es la misma que sin reinicio
		// menos el hueco de la caída, que la referencia cuenta como pérdida.
		refLost := testutil.ToFloat64(refM.LostRecords.WithLabelValues(inv.Load().Data().Exporters[0].RouterID.String()))
		lost := testutil.ToFloat64(m2.LostRecords.WithLabelValues(inv.Load().Data().Exporters[0].RouterID.String()))
		if lost > refLost-down {
			t.Fatalf("N=%d: exporter loss %v after restart includes the downtime (ref %v, downtime %v)", n, lost, refLost, down)
		}
		var gap int
		for _, m := range sink2.bySubjectPrefix(flowbus.TypeCollectorDataGap) {
			if strings.Contains(string(m.Data), "collector_down") {
				gap++
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		for gap == 0 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			for _, m := range sink2.bySubjectPrefix(flowbus.TypeCollectorDataGap) {
				if strings.Contains(string(m.Data), "collector_down") {
					gap++
				}
			}
		}
		if gap != 1 {
			t.Fatalf("N=%d: %d data_gap collector_down events", n, gap)
		}
		t.Logf("N=%d: %d records across the restart, downtime %v records (%d datagrams)", n, refRecs, down, skip)

		// Solo los datagramas hasta que el router reenvía la plantilla: con las
		// plantillas restauradas se publican; sin ellas quedarían retenidos.
		var noTpl []pcapread.Datagram
		probe := decode.New(decode.Options{})
		for i, d := range ds {
			r, _ := probe.Decode(d.Src.Addr().String(), d.Payload)
			if i >= cut+skip {
				if r.Templates > 0 {
					break
				}
				noTpl = append(noTpl, d)
			}
		}
		if len(noTpl) == 0 {
			t.Fatal("the first datagram after the outage carries templates: pick another cut")
		}
		e3, sink3, _ := stateEngine(t, inv, kv, n)
		runAll(t, e3, noTpl)
		e4, sink4, _ := stateEngine(t, inv, nil, n)
		runAll(t, e4, noTpl)
		if restored, cold := publishedRecords(t, sink3), publishedRecords(t, sink4); restored == 0 || cold != 0 {
			t.Fatalf("N=%d: before the template refresh: %d records with restored templates, %d without (want >0 and 0)", n, restored, cold)
		}
	}
}
