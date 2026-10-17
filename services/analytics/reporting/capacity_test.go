package reporting

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	capreport "github.com/hcdestroyer/horus-flow/services/snmp/api/capacityreport"
)

func sampleCapacity(p Period) *capreport.Report {
	planning := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	return &capreport.Report{From: p.From, To: p.To, SaturationWarning: 0.7, SaturationCritical: 0.85, PlanningThreshold: 0.8, History: true,
		Sites: []capreport.Site{{SiteID: uuid.New(), SiteName: "Nodo Centro", Status: "warning", Summary: "Aviso: Proveedor A al 78 % en hora pico.",
			Uplinks: []capreport.Uplink{{InterfaceID: uuid.New(), Label: "Proveedor A", RouterName: "rt-centro", Provider: "Tránsito Demo",
				CapacityBps: 1_000_000_000, UtilP95: ptr(0.78), PeakP95Bps: 780e6, HoursAboveWarning: 31.5, HoursAboveCritical: 2.25, Days: 7,
				LossPct: ptr(0.4), LatencyMs: ptr(18.2), ForecastLevel: "planning", PlanningDate: &planning,
				Recommendation: "Planificar upgrade del enlace «Proveedor A»: pico 780 Mb/s, cruza el 80 % hacia 03/2027."}}}},
		Egress: &capreport.Uplink{InterfaceID: uuid.New(), Label: "Salida internacional", CapacityBps: 1_000_000_000, UtilP95: ptr(0.78),
			Days: 7, ForecastLevel: "ok"},
		Traffic: &capreport.Traffic{LocalBytes: 400, LocalByPrefixBytes: 50, InternationalBytes: 600, UnknownBytes: 10,
			LocalShare: ptr(0.4), InternationalShare: ptr(0.6)}}
}

type fakeCapacity struct {
	rep  *capreport.Report
	err  error
	from time.Time
}

func (f *fakeCapacity) CapacityReport(_ context.Context, _ uuid.UUID, from, _ time.Time) (*capreport.Report, error) {
	f.from = from
	return f.rep, f.err
}

// La sección de capacidad viene del rol snmp por su contrato estrecho; sin
// el rol (o si falla) queda su nota y el resto del reporte sigue.
func TestCollectCapacity(t *testing.T) {
	t.Parallel()
	p := LastWeek(time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC), time.UTC)
	fc := &fakeCapacity{rep: sampleCapacity(p)}
	c := &Collector{Capacity: func() (capreport.Reader, bool) { return fc, true }}
	d := c.Collect(context.Background(), uuid.New(), "ISP", p)
	if d.Capacity == nil || !fc.from.Equal(p.From) || d.CapacityNote != "" {
		t.Fatalf("capacidad = %+v %q", d.Capacity, d.CapacityNote)
	}
	fc.err = errors.New("clickhouse: 10.0.0.9 no responde")
	d = c.Collect(context.Background(), uuid.New(), "ISP", p)
	if d.Capacity != nil || !strings.HasPrefix(d.CapacityNote, NoteSourceFailed) || strings.Contains(d.CapacityNote, "10.0.0.9") {
		t.Fatalf("nota = %q", d.CapacityNote)
	}
	if d := (&Collector{}).Collect(context.Background(), uuid.New(), "ISP", p); d.CapacityNote != NoteCapacityUnavailable {
		t.Fatalf("sin rol snmp: %q", d.CapacityNote)
	}
}

func TestCapacityCSVAndMasked(t *testing.T) {
	t.Parallel()
	d := sampleData(t)
	var buf bytes.Buffer
	if err := RenderCSV(&buf, d); err != nil {
		t.Fatal(err)
	}
	files := unzip(t, buf.Bytes())
	rows := files["capacidad_enlaces.csv"]
	if len(rows) < 8 || rows[1][0] != "Nodo Centro" || rows[1][2] != "Proveedor A" || rows[1][5] != "1000000000" || rows[1][6] != "78,000" ||
		rows[1][8] != "31,50" || rows[1][13] != "Planificar" || rows[1][15] != "2027-03-01" || rows[2][2] != "Salida internacional" {
		t.Fatalf("capacidad_enlaces.csv = %v", rows)
	}
	sum := map[string]string{}
	for _, r := range files["resumen.csv"][1:] {
		sum[r[0]] = r[1]
	}
	if sum["trafico_internacional_pct"] != "60,000" || sum["trafico_local_pct"] != "40,000" {
		t.Fatalf("resumen = %v", sum)
	}
	// Sin datos personales: la variante enmascarada lleva la misma sección.
	var masked bytes.Buffer
	if err := RenderCSV(&masked, MaskData(d)); err != nil {
		t.Fatal(err)
	}
	if m := unzip(t, masked.Bytes())["capacidad_enlaces.csv"]; len(m) != len(rows) || m[1][2] != rows[1][2] {
		t.Fatalf("enmascarada = %v", m)
	}
	// PDF con la sección y sin ella (nota).
	var pdf bytes.Buffer
	if err := RenderPDF(&pdf, d); err != nil || pdf.Len() < 4000 {
		t.Fatalf("pdf: %v", err)
	}
	d.Capacity, d.CapacityNote = nil, NoteCapacityUnavailable
	pdf.Reset()
	if err := RenderPDF(&pdf, d); err != nil {
		t.Fatal(err)
	}
	var z bytes.Buffer
	_ = RenderCSV(&z, d)
	if got := unzip(t, z.Bytes())["capacidad_enlaces.csv"]; len(got) != 1 || got[0][1] != NoteCapacityUnavailable {
		t.Fatalf("sin capacidad = %v", got)
	}
	if ForecastLabel("urgent") != "Urgente" || HealthLabel("offline") != "Fuera de servicio" || ForecastLabel("") != "—" {
		t.Fatal("etiquetas")
	}
}
