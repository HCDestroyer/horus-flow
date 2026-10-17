package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
)

type fakeExporterStates map[uuid.UUID]*collectorapi.FlowExporter

func (f fakeExporterStates) Get(_ context.Context, id uuid.UUID) (*collectorapi.FlowExporter, error) {
	return f[id], nil
}

// El estado del router lleva el último flujo de su exportador (estado del
// collector leído en el sondeo) y conserva el último conocido si el bucket
// deja de tenerlo.
func TestRouterStatusLastFlowAt(t *testing.T) {
	h := newHarness(t, &memStore{})
	last := h.clock().Add(-12 * time.Second)
	states := fakeExporterStates{rtA1: {TenantID: tenantA, RouterID: rtA1, State: "exporting", LastFlowAt: &last}}
	h.eng.o.Exporters = states
	h.cycle()
	live, found, _ := h.eng.Live(context.Background(), tenantA, rtA1)
	if !found || live.Status.LastFlowAt == nil || !live.Status.LastFlowAt.Equal(last) || *live.Status.Signals.Exporter != "exporting" {
		t.Fatalf("último flujo = %+v", live.Status)
	}
	delete(states, rtA1)
	h.cycle()
	rows, _, _ := h.eng.List(context.Background(), tenantA, nil)
	for _, r := range rows {
		if r.RouterID == rtA1 && (r.LastFlowAt == nil || !r.LastFlowAt.Equal(last)) {
			t.Fatalf("se conserva el último conocido: %+v", r.LastFlowAt)
		}
		if r.RouterID == rtA2 && r.LastFlowAt != nil {
			t.Fatalf("router sin flujos: %+v", r.LastFlowAt)
		}
	}
}
