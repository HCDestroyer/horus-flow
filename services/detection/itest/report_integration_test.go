//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

// Datos del reporte semanal de seguridad (rol reporting, I3): conteos del
// periodo por tipo y severidad, MTTR, clientes con más hallazgos activos,
// críticos/altos vigentes, estados de seguridad y aislamiento por tenant.
func TestSecurityReportData(t *testing.T) {
	l := newLifecycle(t)
	from := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	to := from.Add(7 * 24 * time.Hour)
	l.now = from.Add(2 * 24 * time.Hour)

	// Cliente 1: escaneo alto (se resuelve 3 h después) y C2 crítico (activo).
	scan := l.cand(l.now.Add(-time.Minute))
	l.apply(scan)
	c2 := l.cand(l.now.Add(-time.Minute))
	c2.Kind, c2.Severity, c2.Signals = domain.KindC2, domain.SeverityCritical, []string{domain.SignalC2}
	c2.Target = domain.Target{Type: domain.TargetRemoteIP, Value: "203.0.113.9"}
	l.apply(c2)
	// Cliente 2: escaneo medio (falso positivo).
	other := l.cand(l.now.Add(-time.Minute))
	other.CustomerID, other.Severity, other.Confidence = uuid.New(), domain.SeverityMedium, 0.5
	other.Client = other.Client.Next()
	l.apply(other)

	mgr := l.ctx(app.PermRead, app.PermManage)
	var scanID, otherID uuid.UUID
	for _, f := range findingsOf(t, l.db, l.tenant) {
		switch {
		case f.Kind == domain.KindScanning && f.CustomerID == l.cust:
			scanID = f.ID
		case f.CustomerID == other.CustomerID:
			otherID = f.ID
		}
	}
	l.now = l.now.Add(3 * time.Hour)
	if _, _, err := l.svc.Transition(mgr, scanID, 1, "resolve", app.Transition{}); err != nil {
		t.Fatal(err)
	}
	comment := "tráfico legítimo del cliente"
	if _, _, err := l.svc.Transition(mgr, otherID, 1, "false_positive", app.Transition{Comment: &comment}); err != nil {
		t.Fatal(err)
	}

	rep, err := l.svc.SecurityReport(context.Background(), l.tenant, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Totals.Opened != 3 || rep.Totals.Resolved != 1 || rep.Totals.FalsePositive != 1 || rep.ActiveNow != 1 {
		t.Fatalf("totales = %+v activos %d", rep.Totals, rep.ActiveNow)
	}
	if len(rep.ByKind) != 2 || rep.ByKind[0].Kind != domain.KindScanning || rep.ByKind[0].Opened != 2 {
		t.Fatalf("por tipo = %+v", rep.ByKind)
	}
	if len(rep.BySeverity) != 4 || rep.BySeverity[0].Severity != "critical" || rep.BySeverity[0].Opened != 1 ||
		rep.BySeverity[2].FalsePositive != 1 {
		t.Fatalf("por severidad = %+v", rep.BySeverity)
	}
	if rep.MTTRSeconds == nil || rep.MTTRSamples != 1 || *rep.MTTRSeconds < 3*3600 || *rep.MTTRSeconds > 3*3600+120 {
		t.Fatalf("mttr = %v (%d)", rep.MTTRSeconds, rep.MTTRSamples)
	}
	if len(rep.TopCustomers) != 1 || rep.TopCustomers[0].CustomerID != l.cust || rep.TopCustomers[0].Address != "10.20.0.41" ||
		rep.TopCustomers[0].MaxSeverity != "critical" || rep.TopCustomers[0].SecurityState == "" {
		t.Fatalf("clientes = %+v", rep.TopCustomers)
	}
	if len(rep.OpenCriticalHigh) != 1 || rep.OpenCriticalHigh[0].Kind != domain.KindC2 || rep.OpenCriticalHigh[0].Address != "10.20.0.41" {
		t.Fatalf("críticos/altos = %+v", rep.OpenCriticalHigh)
	}
	if rep.ReputationHits != 1 || rep.ReputationCustomers != 1 {
		t.Fatalf("reputación = %d / %d", rep.ReputationHits, rep.ReputationCustomers)
	}
	if n := rep.SecurityStates[domain.SecurityInfected] + rep.SecurityStates[domain.SecuritySuspected] +
		rep.SecurityStates[domain.SecurityMitigated] + rep.SecurityStates[domain.SecurityClean]; n != 2 {
		t.Fatalf("estados = %v", rep.SecurityStates)
	}
	// Fuera del periodo: nada abierto ni resuelto; otro tenant: vacío (RLS).
	before, err := l.svc.SecurityReport(context.Background(), l.tenant, from.Add(-7*24*time.Hour), from)
	if err != nil || before.Totals.Opened != 0 || before.MTTRSeconds != nil {
		t.Fatalf("semana anterior = %+v %v", before, err)
	}
	foreign, err := l.svc.SecurityReport(context.Background(), uuid.New(), from, to)
	if err != nil || foreign.Totals.Opened != 0 || len(foreign.TopCustomers) != 0 || len(foreign.SecurityStates) != 0 {
		t.Fatalf("otro tenant = %+v %v", foreign, err)
	}
}
