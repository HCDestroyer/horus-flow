package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/pcapread"
)

// engineRun es lo que publica un motor con decodeWorkers hilos.
type engineRun struct {
	records  []string            // registros en orden de publicación de cada router, router a router
	batches  map[string][]string // batch_id de cada router en orden de publicación
	gaps     uint64
	lost     uint64
	received float64
	dropped  map[string]float64
}

func recordKey(r *flowpb.FlowRecord) string {
	return fmt.Sprintf("%s|%s|%d|%d|%d|%d|%d|%d|%s|%s|%d|%d", r.SrcIP, r.DstIP, r.SrcPort, r.DstPort, r.Protocol, r.Bytes,
		r.Packets, r.ObservationDomainID, r.TS.Format(time.RFC3339Nano), r.FlowStart.Format(time.RFC3339Nano), r.SamplingRate,
		r.PostNATSrcPort)
}

func runEngine(t *testing.T, inv *flowinv.Store, ds []pcapread.Datagram, decodeWorkers int) engineRun {
	t.Helper()
	sink := &fakeSink{}
	m := NewMetrics(nil)
	e := NewEngine(EngineOptions{Workers: 2, DecodeWorkers: decodeWorkers, QueueDatagrams: len(ds) + 1,
		BatchMaxRecords: 500, BatchMaxAge: time.Hour, BufferBytes: 1 << 40, PendingTTL: 30 * time.Second, CollectorID: "eq",
		State: StateOptions{SilentAfter: time.Hour, LossThreshold: 0.01, LossWindow: 5 * time.Minute, ClockSkew: time.Hour,
			Interval: time.Hour}}, inv, sink, nil, m, slog.New(slog.DiscardHandler))
	for _, d := range ds {
		e.Submit(Datagram{Src: d.Src, Payload: d.Payload, At: d.Time})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run vacía las colas, los lotes abiertos y la publicación
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	out := engineRun{batches: map[string][]string{}}
	perRouter := map[string][]string{}
	for _, msg := range sink.bySubjectPrefix(flowbus.SubjectBatchPrefix) {
		fb, err := flowpb.UnmarshalFlowBatch(msg.Data)
		if err != nil {
			t.Fatal(err)
		}
		out.batches[fb.RouterID] = append(out.batches[fb.RouterID], fmt.Sprintf("%s:%d", fb.BatchID, len(fb.Records)))
		for i := range fb.Records {
			perRouter[fb.RouterID] = append(perRouter[fb.RouterID], fb.RouterID+"|"+recordKey(&fb.Records[i]))
		}
	}
	routers := make([]string, 0, len(perRouter))
	for r := range perRouter {
		routers = append(routers, r)
	}
	sort.Strings(routers)
	for _, r := range routers {
		out.records = append(out.records, perRouter[r]...)
	}
	for _, w := range e.workers {
		for _, tr := range w.seq {
			out.gaps += tr.Gaps
			out.lost += tr.Lost
		}
	}
	out.received = testutil.ToFloat64(m.ReceivedRecords)
	out.dropped = map[string]float64{}
	for _, r := range []string{DropMalformed, DropNoTemplate, DropTunnel, DropExcluded, DropQueueFull, DropUnknownExporter} {
		out.dropped[r] = testutil.ToFloat64(m.Dropped.WithLabelValues(r))
	}
	return out
}

func equivalenceStreams(t *testing.T) map[string][]pcapread.Datagram {
	t.Helper()
	real := realDatagrams(t)
	streams := map[string][]pcapread.Datagram{"mikrotik-real": real}
	// Varios exportadores intercalados y la captura repetida (reinicios de
	// secuencia, plantillas reanunciadas, datagramas sin plantilla al
	// principio de cada exportador).
	var mixed []pcapread.Datagram
	for rep := range 4 {
		for i, d := range real {
			for _, src := range []string{"10.255.3.17", "10.255.3.18", "10.255.3.19"} {
				if rep == 0 && src != "10.255.3.17" && i < 2 {
					continue // el resto de exportadores empieza sin plantilla
				}
				dd := d
				dd.Src = netip.AddrPortFrom(netip.MustParseAddr(src), d.Src.Port())
				mixed = append(mixed, dd)
			}
		}
	}
	streams["three-exporters-x4"] = mixed
	if p := os.Getenv("HORUS_BENCH_PCAP"); p != "" {
		ds, err := pcapread.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		streams["bench"] = ds
	}
	return streams
}

func equivalenceInventory(t *testing.T, ds []pcapread.Datagram) *flowinv.Store {
	t.Helper()
	ips := map[string]bool{}
	for _, d := range ds {
		ips[d.Src.Addr().String()] = true
	}
	y := "exporters:\n"
	keys := make([]string, 0, len(ips))
	for ip := range ips {
		keys = append(keys, ip)
	}
	sort.Strings(keys)
	for i, ip := range keys {
		y += fmt.Sprintf("  - {tenant_id: 0192e000-0000-7000-8000-000000000001, router_id: %s, site_id: 0192e222-0000-7000-8000-000000000022, tunnel_ip: %s}\n",
			uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprint(i))), ip)
	}
	y += `client_prefixes:
  - {id: 0192e444-0000-7000-8000-000000000044, tenant_id: 0192e000-0000-7000-8000-000000000001, site_id: 0192e222-0000-7000-8000-000000000022, realm_id: 0192e111-0000-7000-8000-000000000011, prefix: 10.20.0.0/16, role: customers}
  - {id: 0192e444-0000-7000-8000-000000000045, tenant_id: 0192e000-0000-7000-8000-000000000001, site_id: 0192e222-0000-7000-8000-000000000022, realm_id: 0192e111-0000-7000-8000-000000000011, prefix: 192.168.88.0/30, role: excluded}
`
	s, err := flowinv.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	return flowinv.NewStore(s)
}

// TestDecodeWorkersEquivalence (D23 §4): con 1 y con 8 hilos de
// decodificación el collector publica los mismos registros en el mismo orden
// (luego el mismo multiconjunto), los mismos lotes en número y tamaño, y
// cuenta los mismos huecos de secuencia, recibidos y descartes.
func TestDecodeWorkersEquivalence(t *testing.T) {
	for name, ds := range equivalenceStreams(t) {
		t.Run(name, func(t *testing.T) {
			inv := equivalenceInventory(t, ds)
			one := runEngine(t, inv, ds, 1)
			if len(one.records) == 0 {
				t.Fatal("nothing published")
			}
			for _, n := range []int{2, 8} {
				par := runEngine(t, inv, ds, n)
				if len(par.records) != len(one.records) || !reflect.DeepEqual(par.records, one.records) {
					t.Fatalf("N=%d: %d records, N=1: %d (or different order)", n, len(par.records), len(one.records))
				}
				a, b := append([]string(nil), one.records...), append([]string(nil), par.records...)
				sort.Strings(a)
				sort.Strings(b)
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("N=%d: different multiset of records", n)
				}
				if sizes(par.batches) != sizes(one.batches) {
					t.Fatalf("N=%d: batches %s, N=1: %s", n, sizes(par.batches), sizes(one.batches))
				}
				for r, ids := range par.batches {
					if !sort.StringsAreSorted(ids) {
						t.Fatalf("N=%d: batch_id of router %s not in emission order", n, r)
					}
				}
				if par.gaps != one.gaps || par.lost != one.lost {
					t.Fatalf("N=%d: gaps/lost %d/%d, N=1: %d/%d", n, par.gaps, par.lost, one.gaps, one.lost)
				}
				if par.received != one.received || !reflect.DeepEqual(par.dropped, one.dropped) {
					t.Fatalf("N=%d: received %v dropped %v, N=1: %v %v", n, par.received, par.dropped, one.received, one.dropped)
				}
			}
			t.Logf("%s: %d records, batches %s, gaps %d, lost %d, dropped %v", name, len(one.records), sizes(one.batches), one.gaps, one.lost, one.dropped)
		})
	}
}

// sizes resume los lotes de cada router (número y tamaños, sin los IDs).
func sizes(b map[string][]string) string {
	routers := make([]string, 0, len(b))
	for r := range b {
		routers = append(routers, r)
	}
	sort.Strings(routers)
	out := ""
	for _, r := range routers {
		out += r[:8] + "["
		for _, id := range b[r] {
			out += id[37:] + " "
		}
		out += "] "
	}
	return out
}
