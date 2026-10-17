package reporting

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	alertsapi "github.com/hcdestroyer/horus-flow/services/alerts/api"
	secapi "github.com/hcdestroyer/horus-flow/services/detection/api/securityreport"
)

// Datos sintéticos (IPs de documentación, alias inventados).
func ptr[T any](v T) *T { return &v }

type fakeSecurity struct {
	rep  *secapi.Report
	err  error
	from time.Time
}

func (f *fakeSecurity) SecurityReport(_ context.Context, _ uuid.UUID, from, _ time.Time) (*secapi.Report, error) {
	f.from = from
	return f.rep, f.err
}

type fakeAlerts struct{ rows []alertsapi.NotificationCount }

func (f fakeAlerts) NotificationStats(context.Context, uuid.UUID, time.Time, time.Time) ([]alertsapi.NotificationCount, error) {
	return f.rows, nil
}

type fakeMetrics struct {
	av  *Availability
	tr  *Traffic
	err error
}

func (f fakeMetrics) Availability(context.Context, uuid.UUID, Period) (*Availability, error) {
	return f.av, f.err
}
func (f fakeMetrics) Traffic(context.Context, uuid.UUID, Period) (*Traffic, error) {
	return f.tr, f.err
}

func sampleReport() *secapi.Report {
	return &secapi.Report{
		Totals: secapi.Counts{Opened: 12, Resolved: 7, FalsePositive: 2},
		ByKind: []secapi.KindRow{{Kind: "botnet_c2_communication", Counts: secapi.Counts{Opened: 8, Resolved: 5, FalsePositive: 1}},
			{Kind: "outbound_scanning", Counts: secapi.Counts{Opened: 4, Resolved: 2, FalsePositive: 1}}},
		BySeverity: []secapi.SeverityRow{{Severity: "critical", Counts: secapi.Counts{Opened: 3, Resolved: 2}},
			{Severity: "high", Counts: secapi.Counts{Opened: 5, Resolved: 3, FalsePositive: 1}}, {Severity: "medium", Counts: secapi.Counts{Opened: 4, Resolved: 2, FalsePositive: 1}},
			{Severity: "low"}},
		MTTRSeconds: ptr(5400.0), MTTRSamples: 7, ActiveNow: 9,
		TopCustomers: []secapi.CustomerRow{
			{CustomerID: uuid.New(), Address: "100.64.10.21", Alias: ptr("Ferretería Demo"), Site: "Nodo Centro", OpenFindings: 3, MaxSeverity: "critical", TopKind: "botnet_c2_communication", SecurityState: "infected"},
			{CustomerID: uuid.New(), Address: "2001:db8::42", Alias: ptr("=HYPERLINK(\"http://x\")"), Site: "Nodo Norte", OpenFindings: 1, MaxSeverity: "high", TopKind: "outbound_scanning", SecurityState: "suspected"},
		},
		OpenCriticalHigh: []secapi.FindingRow{{ID: uuid.New(), Kind: "botnet_c2_communication", Severity: "critical", State: "open",
			Summary: "Contacto con C2 conocido", Address: "100.64.10.21", Alias: ptr("Ferretería Demo"), Site: "Nodo Centro",
			OpenedAt: time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC), LastSeenAt: time.Date(2026, 10, 11, 22, 15, 0, 0, time.UTC)}},
		SecurityStates: map[string]int{"clean": 950, "suspected": 4, "infected": 2, "mitigated": 3},
		ReputationHits: 8, ReputationCustomers: 3,
	}
}

func sampleData(t *testing.T) *Data {
	t.Helper()
	loc := mustLoc(t, "America/Mexico_City")
	p := LastWeek(time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC), loc)
	var daily []DayVolume
	for i := 0; i < 7; i++ {
		day := addDays(p.From.In(loc), i)
		daily = append(daily, DayVolume{Day: day, DownBytes: uint64(900+i*50) * 1e9, UpBytes: uint64(120+i*5) * 1e9, HasSamples: i != 3})
	}
	return &Data{
		TenantName: "ISP Demostración S.A.", Period: p, GeneratedAt: time.Date(2026, 10, 12, 13, 0, 5, 0, time.UTC),
		Security: sampleReport(),
		Availability: &Availability{Overall: ptr(0.9987), Routers: []RouterAvailability{
			{RouterID: uuid.New(), Name: "rt-norte", Site: "Nodo Norte", ICMPRatio: ptr(0.93), SNMPRatio: ptr(0.92), Samples: 10080},
			{RouterID: uuid.New(), Name: "rt-centro", Site: "Nodo Centro", ICMPRatio: ptr(1.0), SNMPRatio: ptr(0.999), Samples: 10080},
			{RouterID: uuid.New(), Name: "rt-nuevo", Site: "Nodo Sur"},
		}},
		Traffic:  &Traffic{DownBytes: 7_350_000_000_000, UpBytes: 980_000_000_000, PrevDownBytes: 7_000_000_000_000, PrevUpBytes: 1_000_000_000_000, Daily: daily},
		Alerts:   []alertsapi.NotificationCount{{ChannelKind: "telegram", EventType: "finding_opened", Sent: 11, Failed: 1}, {ChannelKind: "email", EventType: "router_offline", Sent: 2}},
		Capacity: sampleCapacity(p),
	}
}

func TestRenderPDF(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := RenderPDF(&buf, sampleData(t)); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// Revisión visual: HORUS_REPORTING_PDF_OUT=/ruta/muestra.pdf go test -run TestRenderPDF
	if out := os.Getenv("HORUS_REPORTING_PDF_OUT"); out != "" {
		if err := os.WriteFile(out, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.HasPrefix(b, []byte("%PDF-")) || !bytes.Contains(b[len(b)-64:], []byte("%%EOF")) {
		t.Fatalf("no es un PDF (%d bytes): %q…", len(b), b[:min(16, len(b))])
	}
	if len(b) < 4000 {
		t.Fatalf("PDF sospechosamente pequeño: %d bytes", len(b))
	}
	if n := bytes.Count(b, []byte("/Type /Page\n")) + bytes.Count(b, []byte("/Type /Page\r")) + bytes.Count(b, []byte("/Type /Page ")); n < 2 {
		t.Fatalf("páginas = %d, quiero portada + contenido", n)
	}
	// Reproducible: mismos datos ⇒ mismo PDF (fechas del documento = generado).
	var again bytes.Buffer
	_ = RenderPDF(&again, sampleData(t))
	if !bytes.Equal(b, again.Bytes()) {
		t.Fatal("el PDF no es reproducible con los mismos datos")
	}
}

func TestRenderPDFWithoutSources(t *testing.T) {
	t.Parallel()
	c := &Collector{Now: func() time.Time { return time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC) }}
	d := c.Collect(context.Background(), uuid.New(), "ISP sin datos", LastWeek(time.Now(), time.UTC))
	if d.Security != nil || d.SecurityNote != NoteSecurityUnavailable || d.AvailabilityNote != NoteMetricsUnavailable ||
		d.TrafficNote != NoteMetricsUnavailable || d.Alerts != nil || d.AlertsNote != NoteAlertsUnavailable {
		t.Fatalf("notas = %+v", d)
	}
	var buf bytes.Buffer
	if err := RenderPDF(&buf, d); err != nil || !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Fatalf("PDF sin fuentes: %v", err)
	}
	var z bytes.Buffer
	if err := RenderCSV(&z, d); err != nil {
		t.Fatal(err)
	}
	files := unzip(t, z.Bytes())
	if got := files["hallazgos_por_tipo.csv"]; len(got) != 1 || got[0][0] != "nota" || got[0][1] != NoteSecurityUnavailable {
		t.Fatalf("sección sin fuente = %v", got)
	}
}

func TestCollectAssemblesSections(t *testing.T) {
	t.Parallel()
	sec := &fakeSecurity{rep: sampleReport()}
	m := fakeMetrics{av: &Availability{}, tr: &Traffic{DownBytes: 5}}
	c := &Collector{
		Security: func() (secapi.Reader, bool) { return sec, true },
		Alerts:   func() (alertsapi.NotificationStats, bool) { return fakeAlerts{}, true },
		Metrics:  func() Metrics { return m },
		Now:      func() time.Time { return time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC) },
	}
	p := LastWeek(time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC), time.UTC)
	d := c.Collect(context.Background(), uuid.New(), "ISP", p)
	if d.Security == nil || d.Security.Totals.Opened != 12 || !sec.from.Equal(p.From) {
		t.Fatalf("seguridad = %+v", d.Security)
	}
	if d.Traffic == nil || d.Traffic.DownBytes != 5 || d.Availability == nil || d.Alerts == nil || len(d.Alerts) != 0 {
		t.Fatalf("secciones = %+v", d)
	}
	// Una fuente que falla deja su nota (sin detalles internos) y el resto sigue.
	sec.err = errors.New("pg: conexión rechazada en 10.0.0.5")
	c.Metrics = func() Metrics { return fakeMetrics{err: context.DeadlineExceeded} }
	d = c.Collect(context.Background(), uuid.New(), "ISP", p)
	if d.Security != nil || !strings.HasPrefix(d.SecurityNote, NoteSourceFailed) || strings.Contains(d.SecurityNote, "10.0.0.5") {
		t.Fatalf("nota de seguridad = %q", d.SecurityNote)
	}
	if !strings.Contains(d.TrafficNote, "tiempo de espera") || d.Alerts == nil {
		t.Fatalf("notas = %q / alerts %v", d.TrafficNote, d.Alerts)
	}
}

func unzip(t *testing.T, b []byte) map[string][][]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][][]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(rc)
		_ = rc.Close()
		if !bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
			t.Fatalf("%s sin BOM UTF-8", f.Name)
		}
		r := csv.NewReader(bytes.NewReader(raw[3:]))
		r.Comma = CSVSeparator
		r.FieldsPerRecord = -1
		rows, err := r.ReadAll()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		out[f.Name] = rows
	}
	return out
}

func TestRenderCSV(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	d := sampleData(t)
	if err := RenderCSV(&buf, d); err != nil {
		t.Fatal(err)
	}
	files := unzip(t, buf.Bytes())
	if len(files) != len(CSVFiles) {
		t.Fatalf("archivos = %d, quiero %d", len(files), len(CSVFiles))
	}
	sum := map[string]string{}
	for _, r := range files["resumen.csv"][1:] {
		sum[r[0]] = r[1]
	}
	if sum["semana_iso"] != "2026-W41" || sum["desde"] != "05/10/2026 00:00" || sum["hasta"] != "12/10/2026 00:00" ||
		sum["zona_horaria"] != "America/Mexico_City" || sum["hallazgos_abiertos"] != "12" || sum["mttr_segundos"] != "5400" ||
		sum["bytes_bajada"] != "7350000000000" || sum["disponibilidad_media_pct"] != "99,870" {
		t.Fatalf("resumen = %v", sum)
	}
	cust := files["clientes_con_hallazgos.csv"]
	if cust[1][0] != "Ferretería Demo" || cust[1][1] != "100.64.10.21" || cust[1][6] != "Infectado" {
		t.Fatalf("clientes = %v", cust)
	}
	// Inyección de fórmulas neutralizada.
	if !strings.HasPrefix(cust[2][0], "'=") {
		t.Fatalf("alias con fórmula sin neutralizar: %q", cust[2][0])
	}
	days := files["trafico_diario.csv"]
	if len(days) != 8 || days[1][0] != "05/10/2026" || days[4][1] != "" {
		t.Fatalf("tráfico diario = %v", days)
	}
	av := files["disponibilidad_routers.csv"]
	if av[1][0] != "rt-norte" || av[1][2] != "93,000" || av[3][2] != "" {
		t.Fatalf("disponibilidad = %v", av)
	}
	if al := files["avisos_enviados.csv"]; al[1][0] != "Telegram" || al[1][1] != "Hallazgo abierto" || al[1][3] != "11" {
		t.Fatalf("avisos = %v", al)
	}
}

func TestFormatting(t *testing.T) {
	t.Parallel()
	for in, want := range map[uint64]string{0: "0 B", 999: "999 B", 1000: "1,00 kB", 7_350_000_000_000: "7,35 TB", 1_500_000: "1,50 MB"} {
		if got := FmtBytes(in); got != want {
			t.Errorf("FmtBytes(%d) = %q, quiero %q", in, got, want)
		}
	}
	if got := FmtBitsPerSecond(12_500_000); got != "12,50 Mb/s" {
		t.Errorf("FmtBitsPerSecond = %q", got)
	}
	if got := FmtPercent(ptr(0.99871), 2); got != "99,87 %" {
		t.Errorf("FmtPercent = %q", got)
	}
	if FmtPercent(nil, 2) != "—" || FmtChange(5, 0) != "—" {
		t.Error("sin datos → —")
	}
	if got := FmtChange(80, 100); got != "-20,0 %" {
		t.Errorf("FmtChange = %q", got)
	}
	if got := FmtDuration(5400); got != "1 h 30 min" {
		t.Errorf("FmtDuration = %q", got)
	}
	if got := FmtDuration(2*86400 + 3*3600); got != "2 d 3 h" {
		t.Errorf("FmtDuration = %q", got)
	}
	if got := FmtInt(1234567); got != "1.234.567" {
		t.Errorf("FmtInt = %q", got)
	}
	loc := mustLoc(t, "America/Bogota")
	if got := FmtDateTime(time.Date(2026, 10, 12, 5, 7, 0, 0, time.UTC), loc); got != "12/10/2026 00:07" {
		t.Errorf("FmtDateTime = %q", got)
	}
	if csvSafe("@SUM(A1)") != "'@SUM(A1)" || csvSafe("Cliente") != "Cliente" {
		t.Error("csvSafe")
	}
}

func TestFilesStore(t *testing.T) {
	t.Parallel()
	fs := Files{Root: t.TempDir()}
	tenant, run := uuid.New(), uuid.New()
	f, err := fs.Write(tenant, run, FormatCSV, false, func(w io.Writer) error { _, err := io.WriteString(w, "hola"); return err })
	if err != nil {
		t.Fatal(err)
	}
	if f.ObjectKey != "reports/"+tenant.String()+"/"+run.String()+"-csv.zip" || f.SizeBytes != 4 ||
		f.SHA256 != "b221d9dbb083a7f33428d7c2a3c3198ae925614d70210e28716ccaa7cd4ddb79" {
		t.Fatalf("file = %+v", f)
	}
	fh, err := fs.Open(f.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(fh)
	_ = fh.Close()
	if string(b) != "hola" {
		t.Fatalf("contenido = %q", b)
	}
	for _, bad := range []string{"../etc/passwd", "reports/../x/y", "reports/" + tenant.String(), "backups/" + tenant.String() + "/x", "reports/no-uuid/x"} {
		if _, err := fs.Open(bad); !errors.Is(err, errBadKey) {
			t.Errorf("%q: %v, quiero errBadKey", bad, err)
		}
	}
	// Un render que falla no deja archivo.
	if _, err := fs.Write(tenant, run, FormatPDF, false, func(io.Writer) error { return errors.New("boom") }); err == nil {
		t.Fatal("quiero error")
	}
	if _, err := fs.Open(objectKey(tenant, run, FormatPDF, false)); err == nil {
		t.Fatal("quedó un archivo de un render fallido")
	}
	if err := fs.Remove(f.ObjectKey); err != nil || fs.Remove(f.ObjectKey) != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestNoticeMessage(t *testing.T) {
	t.Parallel()
	loc := mustLoc(t, "America/Mexico_City")
	p := LastWeek(time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC), loc)
	d := &Definition{Recipients: []string{"noc@isp.example"}, TenantSlug: "isp-demo"}
	r := &Run{ID: uuid.New(), PeriodFrom: p.From, PeriodTo: p.To, Timezone: loc.String(), Formats: []string{"pdf", "csv"}}
	m := NoticeMessage(d, r, "ISP Demo", "https://horus.example.net/")
	if !strings.Contains(m.Subject, "semana 41 de 2026") || !strings.Contains(m.Text, "https://horus.example.net/t/isp-demo/reports") ||
		!strings.Contains(m.Text, "05/10/2026 00:00 – 11/10/2026 23:59") || len(m.To) != 1 {
		t.Fatalf("aviso = %+v", m)
	}
	if strings.Contains(m.Text, r.ID.String()) {
		t.Fatal("el aviso no lleva identificadores ni enlaces de descarga directa")
	}
}
