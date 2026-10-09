package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/flowpb"
)

type memPub struct {
	mu   sync.Mutex
	msgs []*nats.Msg
}

func (p *memPub) PublishMsg(_ context.Context, m *nats.Msg) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, m)
	return nil
}

func (p *memPub) take(prefix string) []*nats.Msg {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out, rest []*nats.Msg
	for _, m := range p.msgs {
		if strings.HasPrefix(m.Subject, prefix) {
			out = append(out, m)
		} else {
			rest = append(rest, m)
		}
	}
	p.msgs = rest
	return out
}

func firstSeenAddrs(t *testing.T, msgs []*nats.Msg) []string {
	t.Helper()
	var out []string
	for _, m := range msgs {
		var env flowbus.Envelope
		if err := json.Unmarshal(m.Data, &env); err != nil {
			t.Fatal(err)
		}
		if env.TenantID == nil || m.Header.Get(flowbus.HeaderTenant) != *env.TenantID {
			t.Fatal("first_seen without tenant")
		}
		var p ClientFirstSeen
		if err := json.Unmarshal(env.Data, &p); err != nil {
			t.Fatal(err)
		}
		if len(m.Data) > 64<<10 {
			t.Fatalf("first_seen message of %d bytes exceeds FLOWS_EVENTS max_msg_size", len(m.Data))
		}
		for _, c := range p.Clients {
			out = append(out, c.Address)
		}
	}
	return out
}

func batchFor(records []flowpb.FlowRecord) *flowpb.FlowBatch {
	return &flowpb.FlowBatch{BatchID: uuid.NewString(), TenantID: "0192e000-0000-7000-8000-000000000001",
		RouterID: "0192e333-0000-7000-8000-000000000033", ReceivedTo: time.Now(), Records: records}
}

// TestDiscoveryFirstSeenOncePerKey: 300 clientes vistos varias veces ⇒ los
// lotes first_seen cubren exactamente esas 300 claves, una vez cada una
// (I1-05 criterio 1); un cliente conocido no se emite pero sí aparece en el
// activity_summary (criterio 2).
func TestDiscoveryFirstSeenOncePerKey(t *testing.T) {
	pub := &memPub{}
	d := NewDiscovery(DiscoveryOptions{}, pub, nil, slog.New(slog.DiscardHandler))
	inv := store(t)
	known := netip.MustParseAddr("10.20.200.1")
	d.LoadKnown([]flowinv.CustomerKey{{TenantID: uuid.MustParse("0192e000-0000-7000-8000-000000000001"),
		RealmID: uuid.MustParse("0192e111-0000-7000-8000-000000000011"), Address: known}})
	p := &Processor{Inv: inv, Observers: []Observer{d}}
	now := time.Now()
	for round := range 3 {
		var recs []flowpb.FlowRecord
		for i := range 300 {
			ip := netip.AddrFrom4([4]byte{10, 20, byte(i / 250), byte(i%250 + 1)})
			recs = append(recs, flowpb.FlowRecord{SrcIP: ip, DstIP: a("8.8.8.8"), Bytes: 100, Packets: 1, TS: now, FlowStart: now},
				flowpb.FlowRecord{SrcIP: a("8.8.8.8"), DstIP: a("192.0.2.10"), PostNATDstIP: ip, Bytes: 1000, Packets: 1, TS: now, FlowStart: now})
		}
		recs = append(recs, flowpb.FlowRecord{SrcIP: known, DstIP: a("1.1.1.1"), Bytes: 7, Packets: 1, TS: now, FlowStart: now})
		if _, err := p.Rows(batchFor(recs), "0192e000-0000-7000-8000-000000000001"); err != nil {
			t.Fatal(err)
		}
		d.FlushFirstSeen(context.Background())
		got := firstSeenAddrs(t, pub.take(flowbus.TypeClientFirstSeen))
		if round == 0 {
			seen := map[string]int{}
			for _, s := range got {
				seen[s]++
			}
			if len(got) != 300 || len(seen) != 300 {
				t.Fatalf("first_seen: %d addresses (%d distinct), want 300", len(got), len(seen))
			}
			if seen[known.String()] != 0 {
				t.Fatal("known client emitted")
			}
		} else if len(got) != 0 {
			t.Fatalf("round %d re-emitted %d keys", round, len(got))
		}
	}
	d.FlushActivity(context.Background())
	msgs := pub.take(flowbus.SubjectActivityPrefix)
	var clients int
	var knownSeen bool
	for _, m := range msgs {
		s, err := flowpb.UnmarshalClientActivitySummary(m.Data)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range s.Clients {
			clients++
			if c.Address == known.String() {
				knownSeen = c.Flows == 3 && c.BytesEst == 21
			}
		}
	}
	if clients != 301 || !knownSeen {
		t.Fatalf("activity summary: %d clients, known=%v", clients, knownSeen)
	}
}

// TestDiscoveryIPv6SamePrefix: varias IPv6 del mismo prefijo delegado son un solo cliente.
func TestDiscoveryIPv6SamePrefix(t *testing.T) {
	pub := &memPub{}
	d := NewDiscovery(DiscoveryOptions{}, pub, nil, slog.New(slog.DiscardHandler))
	p := &Processor{Inv: store(t), Observers: []Observer{d}}
	var recs []flowpb.FlowRecord
	for i := range 20 {
		ip := netip.MustParseAddr(fmt.Sprintf("2001:db8:1000:2a%02x::%x", i%4, i+1)) // mismo /56
		recs = append(recs, flowpb.FlowRecord{SrcIP: ip, DstIP: a("2606:4700::1"), Bytes: 1, Packets: 1, TS: time.Now()})
	}
	if _, err := p.Rows(batchFor(recs), "0192e000-0000-7000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	d.FlushFirstSeen(context.Background())
	got := firstSeenAddrs(t, pub.take(flowbus.TypeClientFirstSeen))
	if len(got) != 1 || got[0] != "2001:db8:1000:2a00::" {
		t.Fatalf("IPv6 keys = %v", got)
	}
}

// TestDiscoveryThrottle: el límite por realm y minuto descarta y cuenta.
func TestDiscoveryThrottle(t *testing.T) {
	pub := &memPub{}
	d := NewDiscovery(DiscoveryOptions{PerMinute: 10}, pub, nil, slog.New(slog.DiscardHandler))
	p := &Processor{Inv: store(t), Observers: []Observer{d}}
	var recs []flowpb.FlowRecord
	for i := range 50 {
		recs = append(recs, flowpb.FlowRecord{SrcIP: netip.AddrFrom4([4]byte{10, 20, 1, byte(i + 1)}), DstIP: a("8.8.8.8"), Bytes: 1, Packets: 1, TS: time.Now()})
	}
	if _, err := p.Rows(batchFor(recs), "0192e000-0000-7000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	d.FlushFirstSeen(context.Background())
	if got := firstSeenAddrs(t, pub.take(flowbus.TypeClientFirstSeen)); len(got) != 10 {
		t.Fatalf("throttled first_seen = %d, want 10", len(got))
	}
}
