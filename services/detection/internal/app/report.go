package app

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	"github.com/hcdestroyer/horus-flow/services/detection/api/securityreport"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/adapters/postgres"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
)

var _ securityreport.Reader = (*Service)(nil)

// Límites de las tablas del reporte semanal.
const (
	reportTopCustomers = 10
	reportOpenFindings = 50
)

// SecurityReport implementa securityreport.Reader: datos de seguridad del
// reporte semanal del ISP (rol reporting, I3). Todo en una transacción del
// tenant (RLS); alias y nombres de nodo por ClickHouse si está disponible.
func (s *Service) SecurityReport(ctx context.Context, tenant uuid.UUID, from, to time.Time) (*securityreport.Report, error) {
	if tenant == uuid.Nil || !to.After(from) {
		return nil, errors.New("detection: security report: tenant y periodo obligatorios")
	}
	t := pgdb.TenantID(tenant)
	out := &securityreport.Report{}
	var counts []postgres.ReportCount
	var custs []postgres.ReportCustomer
	var open []domain.Finding
	var secStates map[uuid.UUID]string
	err := s.db.TenantTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		if counts, err = postgres.ReportCounts(ctx, tx, from, to); err != nil {
			return err
		}
		if out.MTTRSeconds, out.MTTRSamples, err = postgres.ReportMTTR(ctx, tx, from, to); err != nil {
			return err
		}
		if out.ActiveNow, err = postgres.ReportActive(ctx, tx); err != nil {
			return err
		}
		if custs, err = postgres.ReportTopCustomers(ctx, tx, reportTopCustomers); err != nil {
			return err
		}
		open, err = postgres.List(ctx, tx, postgres.ListFilter{States: []string{domain.StateOpen, domain.StateAcknowledged},
			Severities: []string{domain.SeverityCritical, domain.SeverityHigh}, Sort: "-severity", Limit: reportOpenFindings})
		if err != nil {
			return err
		}
		if len(open) > reportOpenFindings {
			open = open[:reportOpenFindings]
		}
		ids := make([]uuid.UUID, 0, len(custs))
		for _, c := range custs {
			ids = append(ids, c.CustomerID)
		}
		if secStates, err = postgres.SecurityStates(ctx, tx, ids); err != nil {
			return err
		}
		if out.SecurityStates, err = postgres.ReportSecurityStates(ctx, tx); err != nil {
			return err
		}
		out.ReputationHits, out.ReputationCustomers, err = postgres.ReportReputationHits(ctx, tx, from, to)
		return err
	})
	if err != nil {
		return nil, err
	}
	out.ByKind, out.BySeverity, out.Totals = groupReportCounts(counts)

	// Nombres de nodo y alias (dim.site / dim.customer): sin ClickHouse, el UUID y sin alias.
	siteIDs := make([]uuid.UUID, 0, len(custs)+len(open))
	asFindings := make([]domain.Finding, 0, len(custs))
	for _, c := range custs {
		siteIDs = append(siteIDs, c.SiteID)
		asFindings = append(asFindings, domain.Finding{RealmID: c.RealmID, Address: c.Address})
	}
	for _, f := range open {
		siteIDs = append(siteIDs, f.SiteID)
	}
	w := &Widgets{svc: s}
	names := w.siteNames(ctx, t, siteIDs)
	custViews := s.customerViews(ctx, t, asFindings)
	openViews := s.customerViews(ctx, t, open)
	for i, c := range custs {
		st := secStates[c.CustomerID]
		if st == "" {
			st = domain.SecuritySuspected
		}
		out.TopCustomers = append(out.TopCustomers, securityreport.CustomerRow{CustomerID: c.CustomerID, Address: custViews[i].Address,
			Alias: custViews[i].Alias, Site: names[c.SiteID], OpenFindings: c.OpenFindings, MaxSeverity: c.MaxSeverity,
			TopKind: c.TopKind, SecurityState: st})
	}
	for i := range open {
		f := &open[i]
		out.OpenCriticalHigh = append(out.OpenCriticalHigh, securityreport.FindingRow{ID: f.ID, Kind: f.Kind, Severity: f.Severity,
			State: f.State, Summary: f.Summary.Text, Address: openViews[i].Address, Alias: openViews[i].Alias, Site: names[f.SiteID],
			OpenedAt: f.OpenedAt, LastSeenAt: f.LastSeenAt})
	}
	return out, nil
}

// groupReportCounts agrega los conteos por tipo y por severidad y el total.
func groupReportCounts(rows []postgres.ReportCount) ([]securityreport.KindRow, []securityreport.SeverityRow, securityreport.Counts) {
	byKind := map[string]*securityreport.KindRow{}
	bySev := map[string]*securityreport.SeverityRow{}
	var total securityreport.Counts
	add := func(dst *securityreport.Counts, r postgres.ReportCount) {
		dst.Opened += r.Opened
		dst.Resolved += r.Resolved
		dst.FalsePositive += r.FalsePositive
	}
	for _, r := range rows {
		if byKind[r.Kind] == nil {
			byKind[r.Kind] = &securityreport.KindRow{Kind: r.Kind}
		}
		if bySev[r.Severity] == nil {
			bySev[r.Severity] = &securityreport.SeverityRow{Severity: r.Severity}
		}
		add(&byKind[r.Kind].Counts, r)
		add(&bySev[r.Severity].Counts, r)
		add(&total, r)
	}
	kinds := make([]securityreport.KindRow, 0, len(byKind))
	for _, k := range byKind {
		kinds = append(kinds, *k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if kinds[i].Opened != kinds[j].Opened {
			return kinds[i].Opened > kinds[j].Opened
		}
		return kinds[i].Kind < kinds[j].Kind
	})
	var sevs []securityreport.SeverityRow
	for _, s := range []string{domain.SeverityCritical, domain.SeverityHigh, domain.SeverityMedium, domain.SeverityLow} {
		row := securityreport.SeverityRow{Severity: s}
		if r := bySev[s]; r != nil {
			row = *r
		}
		sevs = append(sevs, row)
	}
	// Severidades fuera del enum (no debería haber): al final, en orden alfabético.
	var extra []string
	for s := range bySev {
		if domain.SeverityRank(s) == 0 {
			extra = append(extra, s)
		}
	}
	slices.Sort(extra)
	for _, s := range extra {
		sevs = append(sevs, *bySev[s])
	}
	return kinds, sevs, total
}
