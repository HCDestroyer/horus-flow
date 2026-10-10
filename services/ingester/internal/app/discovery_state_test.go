package app

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
	"github.com/hcdestroyer/horus-flow/packages/go/flowstate"
)

// TestDiscoveryStateAfterRestart (D23): la actividad acumulada de la hora,
// los first_seen pendientes y los ya emitidos sobreviven a un reinicio del
// ingester (instantánea en flows_state).
func TestDiscoveryStateAfterRestart(t *testing.T) {
	ctx := context.Background()
	states := &flowstate.MemStore{}
	pub := &memPub{}
	d := NewDiscovery(DiscoveryOptions{}, pub, nil, slog.New(slog.DiscardHandler))
	p := &Processor{Inv: store(t), Observers: []Observer{d}}
	now := time.Now().UTC().Truncate(time.Millisecond)
	rows := func(p *Processor, ips ...string) {
		var recs []flowpb.FlowRecord
		for _, ip := range ips {
			recs = append(recs, flowpb.FlowRecord{SrcIP: netip.MustParseAddr(ip), DstIP: a("8.8.8.8"), Bytes: 10, Packets: 1, TS: now, FlowStart: now})
		}
		if _, err := p.Rows(batchFor(recs), "0192e000-0000-7000-8000-000000000001"); err != nil {
			t.Fatal(err)
		}
	}
	rows(p, "10.20.0.1", "10.20.0.2")
	d.FlushFirstSeen(ctx) // .1 y .2 emitidos
	rows(p, "10.20.0.3")  // .3 pendiente
	_ = pub.take(flowbus.TypeClientFirstSeen)
	sctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); d.KeepState(sctx, states, time.Hour, nil) }()
	stop()
	<-done // guarda al parar

	pub2 := &memPub{}
	d2 := NewDiscovery(DiscoveryOptions{}, pub2, nil, slog.New(slog.DiscardHandler))
	if err := d2.LoadState(ctx, states); err != nil {
		t.Fatal(err)
	}
	p2 := &Processor{Inv: store(t), Observers: []Observer{d2}}
	rows(p2, "10.20.0.1") // ya emitido: no se repite
	d2.FlushFirstSeen(ctx)
	got := firstSeenAddrs(t, pub2.take(flowbus.TypeClientFirstSeen))
	if len(got) != 1 || got[0] != "10.20.0.3" {
		t.Fatalf("first_seen after restart = %v, want only the pending 10.20.0.3", got)
	}
	d2.FlushActivity(ctx)
	seen := map[string]bool{}
	for _, m := range pub2.take(flowbus.SubjectActivityPrefix) {
		s, err := flowpb.UnmarshalClientActivitySummary(m.Data)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range s.Clients {
			seen[c.Address] = c.LastSeen.Equal(now)
		}
	}
	for _, ip := range []string{"10.20.0.1", "10.20.0.2", "10.20.0.3"} {
		if !seen[ip] {
			t.Fatalf("activity of %s lost after restart (%v)", ip, seen)
		}
	}
}
