package notify

import (
	"slices"
	"strings"
	"testing"
)

// Categoría capacity: transiciones de condiciones de enlace y de
// pronósticos (deduplicadas por recurso, con recuperación).
func TestCapacityEventMapping(t *testing.T) {
	cases := []struct {
		typ  string
		data map[string]any
		want string
	}{
		{"horus.snmp.uplink.condition_changed", map[string]any{"condition": "saturation", "previous_level": "ok", "level": "warning"}, eventUplinkSaturated},
		{"horus.snmp.uplink.condition_changed", map[string]any{"condition": "saturation", "previous_level": "warning", "level": "critical"}, eventUplinkSaturated},
		{"horus.snmp.uplink.condition_changed", map[string]any{"condition": "saturation", "previous_level": "critical", "level": "warning"}, ""},
		{"horus.snmp.uplink.condition_changed", map[string]any{"condition": "loss", "previous_level": "ok", "level": "critical"}, eventUplinkDegraded},
		{"horus.snmp.uplink.condition_changed", map[string]any{"condition": "latency", "previous_level": "warning", "level": "ok"}, eventUplinkRecovered},
		{"horus.snmp.uplink.condition_changed", map[string]any{"condition": "latency", "previous_level": "ok", "level": "ok"}, ""},
		{"horus.snmp.capacity.forecast_changed", map[string]any{"previous_level": "ok", "level": "planning"}, eventCapacityPlanning},
		{"horus.snmp.capacity.forecast_changed", map[string]any{"previous_level": "", "level": "urgent"}, eventCapacityUrgent},
		{"horus.snmp.capacity.forecast_changed", map[string]any{"previous_level": "planning", "level": "urgent"}, eventCapacityUrgent},
		{"horus.snmp.capacity.forecast_changed", map[string]any{"previous_level": "urgent", "level": "planning"}, ""},
		{"horus.snmp.capacity.forecast_changed", map[string]any{"previous_level": "planning", "level": "ok"}, eventCapacityCleared},
		{"horus.snmp.capacity.forecast_changed", map[string]any{"previous_level": "urgent", "level": "insufficient_data"}, eventCapacityCleared},
		{"horus.snmp.capacity.forecast_changed", map[string]any{"previous_level": "ok", "level": "insufficient_data"}, ""},
	}
	for _, c := range cases {
		if got := mapEvent(c.typ, c.data); got != c.want {
			t.Errorf("%s %v → %q, quiero %q", c.typ, c.data, got, c.want)
		}
	}
	// Cada condición de un enlace es su propio recurso (la recuperación cierra la suya).
	sat := eventResource("horus.snmp.uplink.condition_changed", map[string]any{"interface_id": "i1", "condition": "saturation"})
	loss := eventResource("horus.snmp.uplink.condition_changed", map[string]any{"interface_id": "i1", "condition": "loss"})
	if sat == loss || sat != "i1:saturation" {
		t.Fatalf("recursos: %q %q", sat, loss)
	}
	if r := eventResource("horus.snmp.capacity.forecast_changed", map[string]any{"subject_id": "s1"}); r != "forecast:s1" {
		t.Fatalf("recurso del pronóstico: %q", r)
	}
	for _, e := range []string{eventUplinkSaturated, eventUplinkDegraded, eventUplinkRecovered, eventCapacityPlanning, eventCapacityUrgent, eventCapacityCleared} {
		if EventCategory[e] != CategoryCapacity || !slices.Contains(EventTypes, e) {
			t.Errorf("%s fuera de la categoría capacity o de EventTypes", e)
		}
	}
	if !slices.Contains(Categories, CategoryCapacity) || !slices.Contains(RecoveryOf[eventUplinkRecovered], eventUplinkSaturated) ||
		!slices.Contains(RecoveryOf[eventCapacityCleared], eventCapacityUrgent) {
		t.Fatal("categoría o recuperaciones")
	}
	if !slices.Contains(NotifySubjects, "horus.snmp.uplink.condition_changed.>") || !slices.Contains(NotifySubjects, "horus.snmp.capacity.forecast_changed.>") {
		t.Fatal("el durable alerts-notify-snmp no escucha los eventos de capacidad")
	}
}

func TestCapacityRender(t *testing.T) {
	s := &Service{baseURL: "https://horus.example"}
	data := map[string]any{"interface_id": "0192-if", "router_id": "0192-rt", "label": "Proveedor A", "condition": "saturation",
		"previous_level": "ok", "level": "critical", "value": 0.93, "threshold": 0.85, "unit": "ratio", "since": "2026-10-16T20:00:00.000Z",
		"site_name": "Nodo Centro"}
	m, sev := s.render(eventUplinkSaturated, data, renderCtx{routerName: "rt-core", slug: "isp-a"})
	if sev != "high" || m.Subject != "Enlace «Proveedor A» del router «rt-core» saturado" ||
		m.Text != "Valor: 93 % (umbral 85 %), sostenido desde 16/10/2026 20:00 UTC.\nNodo: Nodo Centro" ||
		m.Link != "https://horus.example/t/isp-a/capacity/uplinks/0192-if" {
		t.Fatalf("saturado: %s %+v", sev, m)
	}
	data["condition"], data["unit"], data["value"], data["threshold"], data["level"] = "loss", "percent", 2.4, 1.0, "warning"
	if m, sev := s.render(eventUplinkDegraded, data, renderCtx{routerName: "rt-core"}); sev != "medium" ||
		!strings.Contains(m.Subject, "pérdida de paquetes sostenida") || !strings.Contains(m.Text, "Valor: 2,4 %") && !strings.Contains(m.Text, "Valor: 2.4 %") {
		t.Fatalf("pérdida: %s %+v", sev, m)
	}
	data["level"], data["value"] = "ok", 0.0
	if m, sev := s.render(eventUplinkRecovered, data, renderCtx{routerName: "rt-core"}); sev != "info" || !strings.Contains(m.Subject, "normalizado") {
		t.Fatalf("recuperado: %s %+v", sev, m)
	}
	fc := map[string]any{"subject_id": "eg", "subject_kind": "international_egress", "label": "Salida internacional", "level": "planning",
		"recommendation": "Planificar upgrade de salida internacional: pico 780 Mb/s, en 6 meses ~1,0 Gb/s (+28 %), cruza el 80 % hacia 03/2027."}
	m, sev = s.render(eventCapacityPlanning, fc, renderCtx{})
	if sev != "low" || m.Subject != "Planificar capacidad: Salida internacional" || !strings.HasPrefix(m.Text, "Planificar upgrade") ||
		m.Link != "https://horus.example/capacity" {
		t.Fatalf("planificación: %s %+v", sev, m)
	}
	if _, sev := s.render(eventCapacityUrgent, fc, renderCtx{}); sev != "high" {
		t.Fatalf("urgente: %s", sev)
	}
}
