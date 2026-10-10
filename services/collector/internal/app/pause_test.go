package app

import (
	"encoding/json"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/hcdestroyer/horus-flow/packages/go/flowpause"
)

// TestPausedTenantNotIngested: un ISP suspendido con pause_ingest no publica
// lotes (se cuenta) y al reanudarse vuelve a ingerir.
func TestPausedTenantNotIngested(t *testing.T) {
	ds := realDatagrams(t)
	sink := &fakeSink{}
	e, inv, m := newEngine(t, sink, nil)
	pause := flowpause.New()
	e.UsePause(pause)
	tenant := inv.Load().Data().Exporters[0].TenantID
	ev := func(verb string, v int) []byte {
		b, _ := json.Marshal(map[string]any{"type": "horus.auth.tenant." + verb, "data": map[string]any{"id": tenant, "version": v}})
		return b
	}
	if _, err := pause.Apply(ev("suspended", 2)); err != nil {
		t.Fatal(err)
	}
	w := e.workers[0]
	for _, d := range ds[:50] {
		w.Handle(Datagram{Src: d.Src, Payload: d.Payload, At: d.Time})
	}
	if got := testutil.ToFloat64(m.Dropped.WithLabelValues(DropTenantSuspended)); got != 50 {
		t.Fatalf("suspended drops = %v, want 50", got)
	}
	if testutil.ToFloat64(m.ReceivedRecords) != 0 {
		t.Fatal("records ingested while suspended")
	}
	if _, err := pause.Apply(ev("resumed", 3)); err != nil {
		t.Fatal(err)
	}
	for _, d := range ds[50:100] {
		w.Handle(Datagram{Src: d.Src, Payload: d.Payload, At: d.Time})
	}
	if testutil.ToFloat64(m.ReceivedRecords) == 0 {
		t.Fatal("not ingesting after resume")
	}
}
