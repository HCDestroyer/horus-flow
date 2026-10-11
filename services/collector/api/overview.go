package api

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	"github.com/hcdestroyer/horus-flow/packages/go/platformoverview"
)

// OverviewStats resume los exportadores de cada tenant del inventario para
// GET /platform/overview (TenantOverview): exportando (exporting o lossy,
// como la UI), silenciosos, flujos/s, registros descartados por cuota en la
// última hora y cobertura media de los exportadores que la informan (nil si
// ninguno). Solo conteos: ni IPs ni clientes.
func OverviewStats(ctx context.Context, inv *flowinv.Snapshot, st States, now time.Time) (map[uuid.UUID]platformoverview.Stats, error) {
	out := map[uuid.UUID]platformoverview.Stats{}
	if inv == nil {
		return out, nil
	}
	seen := map[uuid.UUID]bool{}
	for _, e := range inv.Data().Exporters {
		if seen[e.TenantID] {
			continue
		}
		seen[e.TenantID] = true
		views, err := Exporters(ctx, inv, st, e.TenantID, nil, now)
		if err != nil {
			return nil, err
		}
		var exporting, silent int
		var fps, coverage float64
		var dropped uint64
		var covered int
		for _, v := range views {
			switch v.State {
			case StateExporting, StateLossy:
				exporting++
			case StateSilent:
				silent++
			}
			if v.FlowsPerSecond != nil {
				fps += *v.FlowsPerSecond
			}
			if n, err := strconv.ParseUint(v.DroppedRecordsQuota1h, 10, 64); err == nil {
				dropped += n
			}
			if v.CoverageRatio != nil {
				coverage += *v.CoverageRatio
				covered++
			}
		}
		s := platformoverview.Stats{
			ExportersExporting:   platformoverview.Ptr(exporting),
			ExportersSilent:      platformoverview.Ptr(silent),
			IngestFlowsPerSecond: platformoverview.Ptr(fps),
			FlowsDroppedQuota1h:  platformoverview.Ptr(dropped),
		}
		if covered > 0 {
			s.FlowCoverageRatio = platformoverview.Ptr(coverage / float64(covered))
		}
		out[e.TenantID] = s
	}
	return out, nil
}
