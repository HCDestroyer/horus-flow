package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hcdestroyer/horus-flow/packages/go/flowbus"
	"github.com/hcdestroyer/horus-flow/services/collector/api"
)

// TestExporterStateAfterCollectorRestart (I1-26): un collector nuevo continúa
// el estado del KV; si estuvo caído más que SilentAfter, el exportador pasa
// por silent (y recovered) aunque el primer datagrama llegue antes de la
// primera evaluación; con una caída corta sigue exporting sin pasar por
// pending_configuration.
func TestExporterStateAfterCollectorRestart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		down      time.Duration
		dataFirst bool // el primer datagrama llega antes de la primera evaluación
		silent    bool
	}{
		{"long outage, datagram first", 3 * time.Minute, true, true},
		{"long outage, evaluation first", 3 * time.Minute, false, true},
		{"short outage", 30 * time.Second, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kv := &fakeKV{}
			now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
			e1, inv, _ := newEngine(t, &fakeSink{}, kv)
			e1.States.now = func() time.Time { return now }
			exp := &inv.Load().Data().Exporters[0]
			e1.States.Observe(exp, Observation{At: now, Records: 100, FlowSource: "ipfix"})
			e1.States.Evaluate(context.Background(), true)

			// Collector caído tc.down; arranca otro proceso con el mismo KV.
			now = now.Add(tc.down)
			sink := &fakeSink{}
			e2, inv2, _ := newEngine(t, sink, kv)
			st := e2.States
			st.now = func() time.Time { return now }
			exp2 := &inv2.Load().Data().Exporters[0]
			state := func() string {
				var fe api.FlowExporter
				_ = json.Unmarshal(kv.m[exp2.RouterID.String()], &fe)
				return fe.State
			}
			if !tc.dataFirst {
				st.Evaluate(context.Background(), false)
				want := api.StateExporting
				if tc.silent {
					want = api.StateSilent
				}
				if got := state(); got != want {
					t.Fatalf("before the first datagram = %s, want %s", got, want)
				}
				now = now.Add(time.Second)
			}
			st.Observe(exp2, Observation{At: now, Records: 100, FlowSource: "ipfix"})
			st.Evaluate(context.Background(), true)
			if got := state(); got != api.StateExporting {
				t.Fatalf("after the first datagram = %s", got)
			}
			want := 0
			if tc.silent {
				want = 1
			}
			for _, typ := range []string{flowbus.TypeExporterSilent, flowbus.TypeExporterRecovered} {
				if n := len(sink.bySubjectPrefix(typ + ".")); n != want {
					t.Fatalf("%s events = %d, want %d", typ, n, want)
				}
			}
		})
	}
}
