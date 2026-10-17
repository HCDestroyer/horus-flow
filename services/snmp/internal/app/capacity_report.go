package app

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/api/capacityreport"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

var _ capacityreport.Reader = (*Capacity)(nil)

// CapacityReport implementa capacityreport.Reader: la sección de capacidad
// del reporte semanal de [from, to). Los enlaces, su salud y su pronóstico
// salen de la vista de capacidad; el p95 y las horas sobre umbral, de los
// picos diarios del periodo (cada día contra la capacidad vigente ese día);
// el reparto local / internacional, de los días completos del periodo.
func (c *Capacity) CapacityReport(ctx context.Context, tenant uuid.UUID, from, to time.Time) (*capacityreport.Report, error) {
	ov, err := c.Overview(ctx, tenant, defaultWindowDays)
	if err != nil {
		return nil, err
	}
	st := ov.Settings
	out := &capacityreport.Report{From: from.UTC(), To: to.UTC(), SaturationWarning: st.SaturationWarning,
		SaturationCritical: st.SaturationCritical, PlanningThreshold: st.PlanningThreshold, Sites: []capacityreport.Site{},
		History: ov.Meta.History}
	loc := location(c.tenantInfo(ctx, tenant).Timezone)
	firstDay, _, _ := localDay(from, loc)
	lastDay, _, _ := localDay(to.Add(-time.Second), loc)
	var ids []uuid.UUID
	for _, s := range ov.Data {
		for _, u := range s.Uplinks {
			ids = append(ids, u.InterfaceID)
		}
	}
	if ov.Egress != nil {
		ids = append(ids, ov.Egress.SubjectID)
	}
	daily := map[uuid.UUID][]domain.DailyStats{}
	if s := c.series(); s != nil && len(ids) > 0 {
		if daily, err = s.DailySeries(ctx, tenant, ids, firstDay, lastDay); err != nil {
			return nil, err
		}
	}
	for _, s := range ov.Data {
		site := capacityreport.Site{SiteName: s.SiteName, Status: s.Health.Status, Summary: s.Health.Summary, Uplinks: []capacityreport.Uplink{}}
		if s.SiteID != nil {
			site.SiteID = *s.SiteID
		}
		for _, u := range s.Uplinks {
			var capBps int64
			if u.CapacityBps != nil {
				capBps = *u.CapacityBps
			}
			r := reportUplink(u.InterfaceID, u.Label, daily[u.InterfaceID], capBps, u.Forecast)
			r.RouterName = u.RouterName
			if u.Provider != nil {
				r.Provider = *u.Provider
			}
			if u.Probe != nil {
				r.LossPct, r.LatencyMs = u.Probe.LossPct, u.Probe.RTTAvgMs
			}
			site.Uplinks = append(site.Uplinks, r)
		}
		out.Sites = append(out.Sites, site)
	}
	if e := ov.Egress; e != nil {
		r := reportUplink(e.SubjectID, "Salida internacional", daily[e.SubjectID], e.CapacityBps, e.Forecast)
		out.Egress = &r
	}
	days := int(math.Round(to.Sub(from).Hours() / 24))
	if days >= 1 && days <= MaxTrafficDays {
		if tr, err := c.trafficSplitRange(ctx, tenant, nil, from.UTC(), to.UTC()); err == nil {
			out.Traffic = &capacityreport.Traffic{LocalBytes: tr.Total.LocalBytes, LocalByPrefixBytes: tr.Total.LocalByPrefix,
				InternationalBytes: tr.Total.InternationalBytes, UnknownBytes: tr.Total.UnknownBytes, LocalShare: tr.Total.LocalShare,
				InternationalShare: tr.Total.InternationalShare, Country: tr.Country}
		} else {
			c.log.WarnContext(ctx, "snmp: capacity report traffic split unavailable", "error", err)
		}
	}
	return out, nil
}

func reportUplink(id uuid.UUID, label string, days []domain.DailyStats, capBps int64, fc *ForecastSummary) capacityreport.Uplink {
	r := capacityreport.Uplink{InterfaceID: id, Label: label, CapacityBps: capBps, Days: len(days)}
	if len(days) > 0 {
		m := domain.MergeDaily(days)
		r.UtilP95 = domain.WindowUtilP95(days, capBps)
		r.PeakP95Bps, r.HoursAboveWarning, r.HoursAboveCritical = m.PeakP95Bps(), m.HoursAboveWarning, m.HoursAboveCritical
	}
	if fc != nil {
		r.ForecastLevel, r.Recommendation, r.PlanningDate = fc.Level, fc.Recommendation, fc.Planning.Date
	}
	return r
}
