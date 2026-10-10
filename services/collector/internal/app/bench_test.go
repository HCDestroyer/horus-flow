package app

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
	"github.com/hcdestroyer/horus-flow/services/collector/internal/decode"
)

type discardSink struct{}

func (discardSink) PublishMsg(context.Context, *nats.Msg) error { return nil }

// benchDatagrams lee HORUS_BENCH_PCAP (p. ej. una captura de flowsim
// -scenario isp10k -out x.pcap); sin ella, la captura real.
func benchDatagrams(b *testing.B) []pcapread.Datagram {
	b.Helper()
	p := os.Getenv("HORUS_BENCH_PCAP")
	if p == "" {
		b.Skip("HORUS_BENCH_PCAP not set")
	}
	ds, err := pcapread.ReadFile(p)
	if err != nil {
		b.Fatal(err)
	}
	return ds
}

func benchInventory(b *testing.B, ds []pcapread.Datagram) *flowinv.Store {
	b.Helper()
	ip := ds[0].Src.Addr().String()
	s, err := flowinv.Parse([]byte(`
exporters:
  - tenant_id: 0192e000-0000-7000-8000-000000000001
    router_id: 0192e333-0000-7000-8000-000000000033
    site_id: 0192e222-0000-7000-8000-000000000022
    tunnel_ip: ` + ip + `
`))
	if err != nil {
		b.Fatal(err)
	}
	return flowinv.NewStore(s)
}

func recordsOf(ds []pcapread.Datagram) int {
	dec := decode.New(decode.Options{})
	n := 0
	for _, d := range ds {
		r, _ := dec.Decode(d.Src.Addr().String(), d.Payload)
		n += len(r.Records)
	}
	return n
}

// BenchmarkDecodeOnly mide solo el decodificador IPFIX.
func BenchmarkDecodeOnly(b *testing.B) {
	ds := benchDatagrams(b)
	recs := recordsOf(ds)
	b.ResetTimer()
	for range b.N {
		dec := decode.New(decode.Options{})
		for _, d := range ds {
			_, _ = dec.Decode(d.Src.Addr().String(), d.Payload)
		}
	}
	b.ReportMetric(float64(recs*b.N)/b.Elapsed().Seconds(), "records/s")
}

// BenchmarkWorkerHandle mide un trabajador entero (decodificar, secuencia,
// filtros, estado, lotes y Protobuf) sin publicar.
func BenchmarkWorkerHandle(b *testing.B) {
	ds := benchDatagrams(b)
	inv := benchInventory(b, ds)
	recs := recordsOf(ds)
	m := NewMetrics(nil)
	b.ResetTimer()
	for range b.N {
		pub := NewPublisher(&discardSink{}, 1<<40, "bench", m, slog.New(slog.DiscardHandler))
		bt := NewBatcher(pub, 500, time.Second, "bench")
		st := NewStates(StateOptions{SilentAfter: time.Minute, LossWindow: time.Minute, Interval: time.Hour}, inv, nil, nil, slog.New(slog.DiscardHandler))
		w := NewWorker(inv, bt, st, m, 30*time.Second)
		for _, d := range ds {
			w.Handle(Datagram{Src: d.Src, Payload: d.Payload, At: d.Time})
		}
		bt.Flush(time.Now(), true)
	}
	b.ReportMetric(float64(recs*b.N)/b.Elapsed().Seconds(), "records/s")
}

// BenchmarkEngine mide el motor entero (un exportador, colas, carriles,
// pool, lotes y publicación a un sumidero vacío) con 1, 2 y 4 hilos de
// decodificación.
func BenchmarkEngine(b *testing.B) {
	ds := benchDatagrams(b)
	inv := benchInventory(b, ds)
	recs := recordsOf(ds)
	for _, n := range []int{1, 2, 4} {
		b.Run(fmt.Sprintf("decode-workers-%d", n), func(b *testing.B) {
			for range b.N {
				m := NewMetrics(nil)
				e := NewEngine(EngineOptions{Workers: 4, DecodeWorkers: n, QueueDatagrams: len(ds) + 1, BatchMaxRecords: 500,
					BatchMaxAge: time.Second, BufferBytes: 1 << 40, PendingTTL: 30 * time.Second, CollectorID: "bench",
					State: StateOptions{SilentAfter: time.Minute, LossWindow: time.Minute, Interval: time.Hour}},
					inv, discardSink{}, nil, m, slog.New(slog.DiscardHandler))
				for _, d := range ds {
					e.Submit(Datagram{Src: d.Src, Payload: d.Payload, At: d.Time})
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_ = e.Run(ctx)
			}
			b.ReportMetric(float64(recs*b.N)/b.Elapsed().Seconds(), "records/s")
		})
	}
}
