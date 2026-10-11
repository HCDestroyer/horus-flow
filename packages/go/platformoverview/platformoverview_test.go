package platformoverview

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestMergeKeepsUnsetFields(t *testing.T) {
	s := Stats{RoutersTotal: Ptr(3), TunnelsDown: Ptr(1)}
	s.Merge(Stats{ExportersExporting: Ptr(2), TunnelsDown: Ptr(4), FlowCoverageRatio: Ptr(0.5)})
	if *s.RoutersTotal != 3 || *s.ExportersExporting != 2 || *s.TunnelsDown != 4 || *s.FlowCoverageRatio != 0.5 {
		t.Fatalf("merged = %+v", s)
	}
	if s.ExportersSilent != nil || s.OpenFindingsCritical != nil || s.IngestFlowsPerSecond != nil || s.FlowsDroppedQuota1h != nil {
		t.Fatalf("unset fields must stay nil: %+v", s)
	}
}

func TestSourceFunc(t *testing.T) {
	id := uuid.New()
	var src Source = SourceFunc(func(context.Context) (map[uuid.UUID]Stats, error) {
		return map[uuid.UUID]Stats{id: {OpenFindingsCritical: Ptr(7)}}, nil
	})
	m, err := src.OverviewStats(context.Background())
	if err != nil || *m[id].OpenFindingsCritical != 7 {
		t.Fatalf("got %v, %v", m, err)
	}
}
