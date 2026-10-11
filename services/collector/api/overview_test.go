package api

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
)

type memStates map[uuid.UUID]*FlowExporter

func (m memStates) Get(_ context.Context, id uuid.UUID) (*FlowExporter, error) { return m[id], nil }

type failStates struct{}

func (failStates) Get(context.Context, uuid.UUID) (*FlowExporter, error) {
	return nil, errors.New("kv down")
}

func TestOverviewStats(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	r1, r2, r3, r4, r5 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exp := func(tenant, router uuid.UUID, ip string) flowinv.Exporter {
		return flowinv.Exporter{TenantID: tenant, RouterID: router, SiteID: uuid.New(), TunnelIP: netip.MustParseAddr(ip)}
	}
	inv, err := flowinv.New(flowinv.Data{Exporters: []flowinv.Exporter{
		exp(a, r1, "10.255.0.1"), exp(a, r2, "10.255.0.2"), exp(a, r3, "10.255.0.3"), exp(a, r4, "10.255.0.4"),
		exp(b, r5, "10.255.0.5"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	f := func(v float64) *float64 { return &v }
	now := time.Now()
	st := memStates{
		r1: {TenantID: a, RouterID: r1, State: StateExporting, FlowsPerSecond: f(100), CoverageRatio: f(0.9), DroppedRecordsQuota1h: "5"},
		r2: {TenantID: a, RouterID: r2, State: StateLossy, FlowsPerSecond: f(50.5), CoverageRatio: f(0.7), DroppedRecordsQuota1h: "7"},
		r3: {TenantID: a, RouterID: r3, State: StateSilent, DroppedRecordsQuota1h: "0"},
		// r4 sin estado: pending_configuration. r5 (tenant b) tampoco.
	}
	got, err := OverviewStats(context.Background(), inv, st, now)
	if err != nil {
		t.Fatal(err)
	}
	sa := got[a]
	if *sa.ExportersExporting != 2 || *sa.ExportersSilent != 1 {
		t.Fatalf("tenant a exporting/silent = %d/%d", *sa.ExportersExporting, *sa.ExportersSilent)
	}
	if *sa.IngestFlowsPerSecond != 150.5 || *sa.FlowsDroppedQuota1h != 12 {
		t.Fatalf("tenant a fps/dropped = %v/%v", *sa.IngestFlowsPerSecond, *sa.FlowsDroppedQuota1h)
	}
	if sa.FlowCoverageRatio == nil || *sa.FlowCoverageRatio < 0.7999 || *sa.FlowCoverageRatio > 0.8001 {
		t.Fatalf("tenant a coverage = %v", sa.FlowCoverageRatio)
	}
	if sa.RoutersTotal != nil || sa.TunnelsDown != nil {
		t.Fatalf("exporters source must not report routers or tunnels: %+v", sa)
	}
	sb := got[b]
	if *sb.ExportersExporting != 0 || *sb.ExportersSilent != 0 || *sb.IngestFlowsPerSecond != 0 || sb.FlowCoverageRatio != nil {
		t.Fatalf("tenant b = %+v", sb)
	}

	if _, err := OverviewStats(context.Background(), inv, failStates{}, now); err == nil {
		t.Fatal("want error from failing state store")
	}
	if m, err := OverviewStats(context.Background(), nil, st, now); err != nil || len(m) != 0 {
		t.Fatalf("nil inventory = %v, %v", m, err)
	}
}
