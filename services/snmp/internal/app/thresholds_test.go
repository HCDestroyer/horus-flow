package app

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

type memThresholdLoader map[uuid.UUID]domain.ThresholdValues

func (m memThresholdLoader) LoadAll(context.Context) (map[uuid.UUID]domain.ThresholdValues, error) {
	out := map[uuid.UUID]domain.ThresholdValues{}
	for k, v := range m {
		out[k] = v
	}
	return out, nil
}

// Umbrales por ISP: el ISP A baja el aviso de CPU al 5 % y sus routers pasan a
// warning/cpu_high con la misma carga (10 %) con la que el ISP B sigue online.
func TestTenantThresholds(t *testing.T) {
	h := newHarness(t, &memStore{})
	ctx := context.Background()
	low := h.eng.DefaultThresholds()
	low.CPUWarning = 5
	h.eng.o.Thresholds = memThresholdLoader{tenantA: low}
	h.eng.reloadThresholds(ctx)
	if got := h.eng.thresholdsFor(tenantA); got.CPUWarning != 5 || got.SustainedCycles != 3 {
		t.Fatalf("umbrales A = %+v", got)
	}
	if got := h.eng.thresholdsFor(tenantB); got.CPUWarning != 80 {
		t.Fatalf("umbrales B = %+v", got)
	}
	for range 5 {
		h.cycle()
	}
	live, _, _ := h.eng.Live(ctx, tenantA, rtA1)
	if live.Status.Status != api.StatusWarning || *live.Status.Reason != api.ReasonCPUHigh {
		t.Fatalf("A con aviso al 5 %%: %+v", live.Status)
	}
	liveB, _, _ := h.eng.Live(ctx, tenantB, rtB1)
	if liveB.Status.Status != api.StatusOnline {
		t.Fatalf("B con los de la instalación: %+v", liveB.Status)
	}
	ev := h.events.last(TypeStateChanged)
	data := ev.Data.(map[string]any)
	if data["router_name"] != "core-1" || data["thresholds"] == nil {
		t.Fatalf("state_changed sin nombre ni umbrales: %+v", data)
	}
	// Volver a los de la instalación en caliente.
	h.eng.SetTenantThresholds(tenantA, nil)
	if got := h.eng.thresholdsFor(tenantA); got.CPUWarning != 80 {
		t.Fatalf("reset = %+v", got)
	}
}

func TestThresholdValuesValidate(t *testing.T) {
	ok := domain.DefaultThresholds.Values()
	if errs := ok.Validate(); len(errs) != 0 {
		t.Fatalf("por defecto = %v", errs)
	}
	bad := ok
	bad.CPUWarning, bad.MemoryCritical, bad.TemperatureWarning = 99, 101, 10
	if errs := bad.Validate(); len(errs) != 3 { // cpu aviso>crítico, memoria y temperatura fuera de rango
		t.Fatalf("errores = %v", errs)
	}
}
