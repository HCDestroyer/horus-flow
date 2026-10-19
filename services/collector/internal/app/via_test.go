package app

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

// Switch vía router (ADR-0032): el router del sitio traduce el origen del
// sFlow a la dirección Horus del switch (10.255.1.40), que es su identidad;
// la IP privada 192.168.88.2 (agente sFlow dentro del datagrama, repetible
// entre ISP) nunca identifica a nadie. Sus muestras siguen siendo fuente
// SECUNDARIA: nunca al lote primario (flows_raw, clientes, atribución).
const viaInventoryYAML = `
exporters:
  - tenant_id: 0192e000-0000-7000-8000-000000000001
    router_id: 0192e333-0000-7000-8000-0000000000c1
    site_id: 0192e222-0000-7000-8000-000000000022
    tunnel_ip: 10.255.1.40
    secondary: true
`

// sflowFromAgent: una flow sample (1:256) de un agente sFlow con IP privada.
func sflowFromAgent(seq uint32, agent [4]byte) []byte {
	rec := append(u32s(3, 32, 1200, 6), 142, 250, 1, 1, 192, 168, 50, 9)
	rec = append(rec, u32s(443, 40000, 0x10, 0)...)
	sample := append(u32s(seq, 1, 256, 1000, 0, 5, 7, 1), rec...)
	d := append(u32s(5, 1), agent[:]...)
	d = append(d, u32s(0, seq, 1000, 1, 1, uint32(len(sample)))...) //nolint:gosec // test
	return append(d, sample...)
}

func TestViaRouterSwitchIdentity(t *testing.T) {
	s, err := flowinv.Parse([]byte(viaInventoryYAML))
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
	now := time.Now()
	agent := [4]byte{192, 168, 88, 2}
	// Llega por el túnel con el origen traducido a la dirección Horus.
	w.Handle(Datagram{Src: netip.AddrPortFrom(netip.MustParseAddr("10.255.1.40"), 6343), Payload: sflowFromAgent(1, agent), At: now})
	// La misma muestra con origen la IP privada (sin NAT, u otro ISP con la misma IP): exportador desconocido.
	w.Handle(Datagram{Src: netip.AddrPortFrom(netip.MustParseAddr("192.168.88.2"), 6343), Payload: sflowFromAgent(2, agent), At: now})
	if got := testutil.ToFloat64(m.Dropped.WithLabelValues(DropUnknownExporter)); got != 1 {
		t.Fatalf("datagrama desde la IP privada: descartes unknown_exporter = %v", got)
	}
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
	if n := len(sink.bySubjectPrefix(flowbus.SubjectBatchPrefix)); n != 0 {
		t.Fatalf("muestras de un switch vía router en el lote primario: %d", n)
	}
	sec := sink.bySubjectPrefix(flowbus.SubjectSecondaryPrefix)
	if len(sec) != 1 {
		t.Fatalf("lotes secundarios: %d", len(sec))
	}
	fb, err := flowpb.UnmarshalFlowBatch(sec[0].Data)
	if err != nil || fb.RouterID != "0192e333-0000-7000-8000-0000000000c1" || len(fb.Records) != 1 ||
		fb.Records[0].ExporterIP != netip.MustParseAddr("10.255.1.40") {
		t.Fatalf("lote del switch: %+v %v", fb, err)
	}
}
