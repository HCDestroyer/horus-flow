package app

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

// Exportador en modo directo (Huawei VRP sin túnel): identificado por su dirección de gestión verificada. Sus
// flujos hacia o desde esa dirección NO se descartan (puede ser la IP del NAT de los clientes) y sFlow v5 entra
// como flow_source=sflow_v5 con la tasa de muestreo de cada muestra.
const directInventoryYAML = `
exporters:
  - tenant_id: 0192e000-0000-7000-8000-000000000001
    router_id: 0192e333-0000-7000-8000-0000000000a1
    site_id: 0192e222-0000-7000-8000-000000000022
    tunnel_ip: 198.51.100.20
    direct: true
`

func u32s(vs ...uint32) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.BigEndian.AppendUint32(b, v)
	}
	return b
}

// sflowNATDatagram: una flow sample (1:256) con un registro sampled_ipv4 de Internet hacia la IP del NAT.
func sflowNATDatagram(seq uint32) []byte {
	rec := append(u32s(3, 32, 1200, 6), 142, 250, 1, 1, 198, 51, 100, 20)
	rec = append(rec, u32s(443, 40000, 0x10, 0)...)
	sample := append(u32s(seq, 1, 256, 1000, 0, 5, 7, 1), rec...)
	d := append(u32s(5, 1), 198, 51, 100, 20)
	d = append(d, u32s(0, seq, 1000, 1, 1, uint32(len(sample)))...) //nolint:gosec // test
	return append(d, sample...)
}

func TestDirectExporterSFlow(t *testing.T) {
	s, err := flowinv.Parse([]byte(directInventoryYAML))
	if err != nil {
		t.Fatal(err)
	}
	inv := flowinv.NewStore(s)
	sink := &fakeSink{}
	m := NewMetrics(nil)
	e := NewEngine(EngineOptions{Workers: 1, QueueDatagrams: 64, BatchMaxRecords: 500, BatchMaxAge: time.Second, BufferBytes: 1 << 20,
		PendingTTL: time.Second, CollectorID: "test", State: StateOptions{SilentAfter: 2 * time.Minute, LossThreshold: 0.01,
			LossWindow: 5 * time.Minute, ClockSkew: 30 * time.Second, Interval: time.Hour}}, inv, sink, nil, m, slog.New(slog.DiscardHandler))
	w := NewWorker(inv, e.Batcher, e.States, m, time.Second)
	src := netip.AddrPortFrom(netip.MustParseAddr("198.51.100.20"), 6343)
	now := time.Now()
	w.Handle(Datagram{Src: src, Payload: sflowNATDatagram(1), At: now})
	w.Handle(Datagram{Src: src, Payload: sflowNATDatagram(3), At: now}) // salto de secuencia (datagrama 2 perdido)
	e.Batcher.Flush(now, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.Pub.Run(ctx, time.Second); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for n, _ := e.Pub.Pending(); n > 0 && time.Now().Before(deadline); n, _ = e.Pub.Pending() {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	var recs []flowpb.FlowRecord
	for _, msg := range sink.bySubjectPrefix(flowbus.SubjectBatchPrefix) {
		fb, err := flowpb.UnmarshalFlowBatch(msg.Data)
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, fb.Records...)
	}
	if len(recs) != 2 {
		t.Fatalf("flujos hacia la dirección de gestión (NAT) descartados: %d", len(recs))
	}
	r := recs[0]
	if r.DstIP != netip.MustParseAddr("198.51.100.20") || r.FlowSource != "sflow_v5" || r.SamplingRate != 256 || r.Bytes != 1200 ||
		r.InputIfIndex != 5 || r.ExporterIP != netip.MustParseAddr("198.51.100.20") {
		t.Fatalf("registro sFlow: %+v", r)
	}
	var gaps uint64
	for _, tr := range w.seq {
		gaps += tr.Gaps
	}
	if gaps != 1 {
		t.Fatalf("secuencia de datagramas sFlow: %d saltos", gaps)
	}
	e.States.Evaluate(context.Background(), false)
	snap := e.States.Snapshot()
	if len(snap) != 1 || snap[0].FlowSource == nil || *snap[0].FlowSource != "sflow_v5" {
		t.Fatalf("estado del exportador: %+v", snap)
	}
	if versionLabel(0x5F05) != "sflow5" || versionLabel(10) != "10" {
		t.Fatal("etiqueta de versión")
	}
}
