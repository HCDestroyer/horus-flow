package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	snmpapi "github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// WidgetSiteHealth es el tipo de widget "Salud de sitios / capacidad"
// (widget-types.json): una fila por nodo con la salud explicable que publica
// el rol snmp en el proceso (CapacityReader.SiteHealth).
const WidgetSiteHealth = "site_health"

// ErrCapacityUnavailable: el rol snmp no está en este proceso.
var ErrCapacityUnavailable = errors.New("analytics: snmp capacity not available in this process")

// siteHealthColumns son las columnas de la tabla, en el orden y con los tipos
// que espera el frontend (apps/frontend/app/widgets/shapes.ts, SiteHealthRow).
var siteHealthColumns = []tw.Column{
	{Key: "site", Type: "string"}, {Key: "site_id", Type: "string"}, {Key: "status", Type: "state"}, {Key: "summary", Type: "string"},
	{Key: "uplinks", Type: "number"}, {Key: "uplinks_down", Type: "number"}, {Key: "worst_util_p95", Type: "number"},
	{Key: "worst_loss_pct", Type: "number"}, {Key: "forecast_level", Type: "string"}, {Key: "next_crossing", Type: "timestamp"},
}

var siteHealthRank = map[string]int{"offline": 5, "critical": 4, "degraded": 3, "warning": 2, "unknown": 1, "online": 0}

func (s *Service) capacity() snmpapi.CapacityReader {
	if s.Capacity == nil {
		return nil
	}
	return s.Capacity()
}

// siteHealth resuelve site_health: filas de CapacityReader.SiteHealth
// filtradas por config.show y ordenadas por config.sort (status = peor primero).
// Sin el rol snmp el widget es "no disponible", nunca una lista vacía engañosa.
func (w *Widgets) siteHealth(ctx context.Context, out *tw.WidgetData, sc Scope, cfg widgetConfig) error {
	rd := w.svc.capacity()
	if rd == nil {
		return fmt.Errorf("%w: %v", chread.ErrUnavailable, ErrCapacityUnavailable)
	}
	list, err := rd.SiteHealth(ctx, sc.Tenant, sc.Sites)
	if err != nil {
		return fmt.Errorf("%w: site health: %v", chread.ErrUnavailable, err)
	}
	list = slices.Clone(list)
	if len(cfg.Show) > 0 {
		list = slices.DeleteFunc(list, func(r snmpapi.SiteHealthSummary) bool { return !slices.Contains(cfg.Show, r.Status) })
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if cfg.Sort != "name" && siteHealthRank[a.Status] != siteHealthRank[b.Status] {
			return siteHealthRank[a.Status] > siteHealthRank[b.Status]
		}
		return strings.ToLower(a.SiteName) < strings.ToLower(b.SiteName)
	})
	out.Data = tw.Data{Kind: "table", Columns: siteHealthColumns, Rows: make([]map[string]any, 0, len(list))}
	for _, r := range list {
		row := map[string]any{"site": r.SiteName, "site_id": r.SiteID.String(), "status": r.Status, "summary": r.Summary,
			"uplinks": r.Uplinks, "uplinks_down": r.UplinksDown, "worst_util_p95": r.WorstUtilP95, "worst_loss_pct": r.WorstLossPct,
			"forecast_level": nil, "next_crossing": fmtTime(r.NextCrossing)}
		if r.ForecastLevel != "" {
			row["forecast_level"] = r.ForecastLevel
		}
		out.Data.Rows = append(out.Data.Rows, row)
	}
	return nil
}
