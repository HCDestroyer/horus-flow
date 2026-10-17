package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// ForecastSummary es el pronóstico resumido de un enlace o de la salida
// internacional (CapacityForecastSummary del contrato).
type ForecastSummary struct {
	SubjectID      uuid.UUID       `json:"subject_id"`
	SubjectKind    string          `json:"subject_kind"`
	Level          string          `json:"level"`
	Confidence     string          `json:"confidence"`
	ComputedAt     time.Time       `json:"computed_at"`
	HistoryDays    int             `json:"history_days"`
	Seasonality    bool            `json:"seasonality"`
	CapacityBps    int64           `json:"capacity_bps"`
	CurrentPeakBps *float64        `json:"current_peak_bps"`
	MonthlyGrowth  *float64        `json:"monthly_growth"`
	Peak6mBps      *float64        `json:"peak_6m_bps"`
	Growth6m       *float64        `json:"growth_6m"`
	Planning       domain.Crossing `json:"planning"`
	Urgent         domain.Crossing `json:"urgent"`
	LeadDays       int             `json:"lead_days"`
	Recommendation string          `json:"recommendation"`
}

func summarize(f *domain.ForecastRow) *ForecastSummary {
	if f == nil {
		return nil
	}
	x := f.Forecast
	return &ForecastSummary{SubjectID: f.SubjectID, SubjectKind: f.SubjectKind, Level: f.Level, Confidence: x.Confidence,
		ComputedAt: f.ComputedAt, HistoryDays: x.HistoryDays, Seasonality: x.Seasonality, CapacityBps: x.CapacityBps,
		CurrentPeakBps: x.CurrentPeakBps, MonthlyGrowth: x.MonthlyGrowth, Peak6mBps: x.Peak6mBps, Growth6m: x.Growth6m,
		Planning: x.Planning, Urgent: x.Urgent, LeadDays: x.LeadDays, Recommendation: f.Recommendation}
}

// WindowStats resume una ventana de días (UplinkWindow del contrato).
type WindowStats struct {
	Days               int       `json:"days"`
	From               time.Time `json:"from"`
	To                 time.Time `json:"to"`
	Samples            int       `json:"samples"`
	RxP95Bps           float64   `json:"rx_p95_bps"`
	TxP95Bps           float64   `json:"tx_p95_bps"`
	UtilP95            *float64  `json:"util_p95"`
	HoursAboveWarning  float64   `json:"hours_above_warning"`
	HoursAboveCritical float64   `json:"hours_above_critical"`
	Errors             uint64    `json:"errors"`
	Drops              uint64    `json:"drops"`
}

// ProbeView es una sonda con su resumen de 24 h (CapacityProbe del contrato).
type ProbeView struct {
	ProbeID     uuid.UUID  `json:"probe_id"`
	Source      string     `json:"source"`
	Ref         string     `json:"ref"`
	Target      string     `json:"target"`
	Label       string     `json:"label"`
	RouterID    *uuid.UUID `json:"router_id"`
	InterfaceID *uuid.UUID `json:"interface_id"`
	Status      string     `json:"status"`
	LastAt      *time.Time `json:"last_at"`
	LossPct     *float64   `json:"loss_pct"`
	LossPctMax  *float64   `json:"loss_pct_max"`
	RTTAvgMs    *float64   `json:"rtt_avg_ms"`
	JitterMs    *float64   `json:"jitter_ms"`
	LastLossPct *float64   `json:"last_loss_pct"`
	LastRTTMs   *float64   `json:"last_rtt_ms"`
	LastJitter  *float64   `json:"last_jitter_ms"`
}

// ConditionView es una condición vigente no sana.
type ConditionView struct {
	Condition string    `json:"condition"`
	Level     string    `json:"level"`
	Since     time.Time `json:"since"`
	Value     float64   `json:"value"`
}

// UplinkView es un enlace con su capacidad (CapacityUplink del contrato).
type UplinkView struct {
	InterfaceID         uuid.UUID        `json:"interface_id"`
	RouterID            uuid.UUID        `json:"router_id"`
	RouterName          string           `json:"router_name"`
	SiteID              *uuid.UUID       `json:"site_id"`
	SiteName            string           `json:"site_name"`
	InterfaceName       string           `json:"interface_name"`
	Configured          bool             `json:"configured"`
	AutoDetected        bool             `json:"auto_detected"`
	Label               string           `json:"label"`
	Provider            *string          `json:"provider"`
	Kind                *string          `json:"kind"`
	InternationalEgress bool             `json:"international_egress"`
	ContractedBps       *int64           `json:"contracted_bps"`
	SpeedBps            *int64           `json:"speed_bps"`
	CapacityBps         *int64           `json:"capacity_bps"`
	CapacitySource      string           `json:"capacity_source"`
	OperStatus          *string          `json:"oper_status"`
	RxBps               *float64         `json:"rx_bps"`
	TxBps               *float64         `json:"tx_bps"`
	Utilization         *float64         `json:"utilization"`
	SampledAt           *time.Time       `json:"sampled_at"`
	Window              *WindowStats     `json:"window"`
	Diagnosis           domain.Diagnosis `json:"diagnosis"`
	Probe               *ProbeView       `json:"probe"`
	Conditions          []ConditionView  `json:"conditions"`
	Forecast            *ForecastSummary `json:"forecast"`
	Version             int              `json:"version"`
}

// RouterBrief es un router del nodo con el estado de la lectura de sondas.
type RouterBrief struct {
	RouterID    uuid.UUID `json:"router_id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	ProbesError *string   `json:"probes_error"`
}

// SiteView es un nodo (CapacitySite del contrato).
type SiteView struct {
	SiteID       *uuid.UUID        `json:"site_id"`
	SiteName     string            `json:"site_name"`
	Health       domain.SiteHealth `json:"health"`
	Uplinks      []UplinkView      `json:"uplinks"`
	RouterProbes []ProbeView       `json:"router_probes"`
	Routers      []RouterBrief     `json:"routers"`
}

// EgressView es la salida internacional agregada del ISP.
type EgressView struct {
	SubjectID   uuid.UUID        `json:"subject_id"`
	Members     []uuid.UUID      `json:"members"`
	CapacityBps int64            `json:"capacity_bps"`
	RxBps       *float64         `json:"rx_bps"`
	TxBps       *float64         `json:"tx_bps"`
	Utilization *float64         `json:"utilization"`
	Window      *WindowStats     `json:"window"`
	Forecast    *ForecastSummary `json:"forecast"`
}

// OverviewMeta es meta de GET /capacity/overview.
type OverviewMeta struct {
	GeneratedAt time.Time `json:"generated_at"`
	WindowDays  int       `json:"window_days"`
	History     bool      `json:"history"`
	Store       bool      `json:"store"`
}

// Overview es GET /capacity/overview.
type Overview struct {
	Data        []SiteView              `json:"data"`
	Egress      *EgressView             `json:"egress"`
	HorusProbes []ProbeView             `json:"horus_probes"`
	Settings    domain.CapacitySettings `json:"settings"`
	Meta        OverviewMeta            `json:"meta"`
}

func probeView(p clickhouse.ProbeAgg, last *domain.ProbeSample, now time.Time) ProbeView {
	v := ProbeView{ProbeID: p.ProbeID, Source: p.Source, Ref: p.Ref, Target: p.Target, Label: p.Label, Status: "unknown",
		LossPct: p.LossPct, LossPctMax: p.LossPctMax, RTTAvgMs: p.RTTAvgMs, JitterMs: p.JitterMs}
	if p.RouterID != uuid.Nil {
		id := p.RouterID
		v.RouterID = &id
	}
	if p.InterfaceID != uuid.Nil {
		id := p.InterfaceID
		v.InterfaceID = &id
	}
	if !p.LastAt.IsZero() {
		at := p.LastAt
		v.LastAt = &at
	}
	if last != nil {
		at := last.At
		v.LastAt = &at
		v.LastLossPct, v.LastRTTMs, v.LastJitter = last.LossPct, last.RTTAvgMs, last.JitterMs
		if now.Sub(last.At) <= probeStaleAfter && last.Up != nil {
			v.Status = "down"
			if *last.Up {
				v.Status = "up"
			}
		}
	}
	return v
}

// probeViewFromSample construye la vista de una sonda sin histórico.
func probeViewFromSample(s domain.ProbeSample, now time.Time) ProbeView {
	agg := clickhouse.ProbeAgg{ProbeID: s.ProbeID, RouterID: s.RouterID, InterfaceID: s.InterfaceID, Source: s.Source, Ref: s.Ref,
		Target: s.Target, Label: s.Label, LastAt: s.At, LossPct: s.LossPct, RTTAvgMs: s.RTTAvgMs, JitterMs: s.JitterMs}
	return probeView(agg, &s, now)
}

func windowOf(days []domain.DailyStats, n int, capBps int64) *WindowStats {
	if len(days) == 0 {
		return nil
	}
	m := domain.MergeDaily(days)
	if capBps > 0 {
		m.CapacityBps = capBps
	}
	// Cada día contra la capacidad vigente ese día: un upgrade no rebaja la utilización pasada.
	w := &WindowStats{Days: n, From: days[0].Day, To: days[len(days)-1].Day, Samples: m.Samples, RxP95Bps: m.RxP95Bps, TxP95Bps: m.TxP95Bps,
		UtilP95: domain.WindowUtilP95(days, capBps), HoursAboveWarning: m.HoursAboveWarning, HoursAboveCritical: m.HoursAboveCritical, Errors: m.Errors, Drops: m.Drops}
	return w
}

// overviewData son las series leídas para una vista.
type overviewData struct {
	daily  map[uuid.UUID][]domain.DailyStats
	probes []clickhouse.ProbeAgg
	meta   map[uuid.UUID]clickhouse.IfaceMeta
}

// windowDays lee los últimos n días completos de uplink_daily y calcula hoy
// al vuelo desde interface_5m (dato fresco sin esperar al trabajo diario).
func (c *Capacity) windowDays(ctx context.Context, tenant uuid.UUID, ups []*uplinkRef, egress []uuid.UUID, n int, loc *time.Location,
	st domain.CapacitySettings, now time.Time) (*overviewData, error) {
	out := &overviewData{daily: map[uuid.UUID][]domain.DailyStats{}, meta: map[uuid.UUID]clickhouse.IfaceMeta{}}
	s := c.series()
	if s == nil {
		return out, nil
	}
	today, start, _ := localDay(now, loc)
	subjects := make([]uuid.UUID, 0, len(ups)+1)
	ifaces := make([]uuid.UUID, 0, len(ups))
	for _, u := range ups {
		subjects = append(subjects, u.InterfaceID)
		ifaces = append(ifaces, u.InterfaceID)
	}
	egressID := domain.EgressSubjectID(tenant)
	if len(egress) > 0 {
		subjects = append(subjects, egressID)
	}
	daily, err := s.DailySeries(ctx, tenant, subjects, today.AddDate(0, 0, -(n-1)), today.AddDate(0, 0, -1))
	if err != nil {
		return nil, err
	}
	out.daily = daily
	buckets, meta, err := s.Buckets(ctx, tenant, ifaces, start, now)
	if err != nil {
		return nil, err
	}
	out.meta = meta
	var egressBuckets [][]domain.Bucket5m
	var egressCap int64
	for _, u := range ups {
		capBps := u.capacity()
		if capBps == 0 && meta[u.InterfaceID].SpeedBps > 0 {
			var contracted *int64
			if u.Config != nil {
				contracted = u.Config.ContractedBps
			}
			sp := meta[u.InterfaceID].SpeedBps
			capBps = domain.CapacityBps(contracted, &sp)
		}
		if d, ok := domain.ComputeDaily(today, buckets[u.InterfaceID], capBps, st.SaturationWarning, st.SaturationCritical); ok {
			out.daily[u.InterfaceID] = append(out.daily[u.InterfaceID], d)
		}
		if slices.Contains(egress, u.InterfaceID) {
			egressBuckets = append(egressBuckets, buckets[u.InterfaceID])
			egressCap += capBps
		}
	}
	if len(egress) > 0 {
		if d, ok := domain.ComputeDaily(today, domain.SumBuckets(egressBuckets), egressCap, st.SaturationWarning, st.SaturationCritical); ok {
			out.daily[egressID] = append(out.daily[egressID], d)
		}
	}
	out.probes, err = s.ProbeSummaries(ctx, tenant, now.Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Overview construye GET /capacity/overview para el tenant (todos los
// nodos; el handler filtra por alcance). Se memoriza 60 s por ISP.
func (c *Capacity) Overview(ctx context.Context, tenant uuid.UUID, days int) (*Overview, error) {
	if days <= 0 {
		days = defaultWindowDays
	}
	now := c.now().UTC()
	if days == defaultWindowDays {
		c.mu.RLock()
		cached, ok := c.cache[tenant]
		c.mu.RUnlock()
		if ok && now.Sub(cached.at) < overviewTTL {
			return cached.ov, nil
		}
	}
	ov, err := c.buildOverview(ctx, tenant, days, now)
	if err != nil {
		return nil, err
	}
	if days == defaultWindowDays {
		c.mu.Lock()
		c.cache[tenant] = cachedOverview{at: now, ov: ov}
		c.mu.Unlock()
	}
	return ov, nil
}

func (c *Capacity) buildOverview(ctx context.Context, tenant uuid.UUID, days int, now time.Time) (*Overview, error) {
	// Configuración fresca del ISP (otra réplica pudo cambiarla; el sondeador la recarga cada minuto).
	c.reloadTenant(ctx, tenant)
	st := c.settingsFor(tenant)
	info := c.tenantInfo(ctx, tenant)
	loc := location(info.Timezone)
	routers := c.routersOf(tenant, now)
	ups := c.uplinksOf(tenant, routers)
	var egressMembers []uuid.UUID
	for _, u := range ups {
		if u.Config != nil && u.Config.InternationalEgress {
			egressMembers = append(egressMembers, u.InterfaceID)
		}
	}
	data, err := c.windowDays(ctx, tenant, ups, egressMembers, days, loc, st, now)
	if err != nil {
		c.log.WarnContext(ctx, "snmp: capacity history unavailable (live data only)", "error", err)
		data = &overviewData{daily: map[uuid.UUID][]domain.DailyStats{}, meta: map[uuid.UUID]clickhouse.IfaceMeta{}}
	}
	var forecasts []domain.ForecastRow
	var conditions []domain.ConditionRow
	if c.o.Repo != nil {
		if forecasts, err = c.o.Repo.ListForecasts(ctx, tenant); err != nil {
			c.log.WarnContext(ctx, "snmp: forecasts not read", "error", err)
		}
		if conditions, err = c.o.Repo.ListConditions(ctx, tenant); err != nil {
			c.log.WarnContext(ctx, "snmp: conditions not read", "error", err)
		}
	}
	fcBy := map[uuid.UUID]*domain.ForecastRow{}
	for i := range forecasts {
		fcBy[forecasts[i].SubjectID] = &forecasts[i]
	}
	condBy := map[uuid.UUID][]ConditionView{}
	for _, r := range conditions {
		condBy[r.InterfaceID] = append(condBy[r.InterfaceID], ConditionView{Condition: r.Condition, Level: r.Level, Since: r.Since, Value: r.Value})
	}
	c.mu.RLock()
	latest := map[uuid.UUID]domain.ProbeSample{}
	for id, s := range c.latest {
		if s.TenantID == tenant {
			latest[id] = s
		}
	}
	probeErr := map[uuid.UUID]probeFailure{}
	for id, f := range c.probeErr {
		probeErr[id] = f
	}
	c.mu.RUnlock()
	// Sondas: resumen de 24 h (ClickHouse) + última lectura en memoria.
	probes := map[uuid.UUID]ProbeView{}
	for _, p := range data.probes {
		var last *domain.ProbeSample
		if s, ok := latest[p.ProbeID]; ok {
			last = &s
		}
		probes[p.ProbeID] = probeView(p, last, now)
	}
	for id, s := range latest {
		if _, ok := probes[id]; !ok {
			probes[id] = probeViewFromSample(s, now)
		}
	}
	gwProbe := map[uuid.UUID]ProbeView{}
	var horus []ProbeView
	routerProbes := map[uuid.UUID][]ProbeView{}
	for _, p := range probes {
		switch {
		case p.Source == domain.ProbeSourceHorus:
			horus = append(horus, p)
		case p.InterfaceID != nil && p.Ref == domain.ProbeRefGateway:
			gwProbe[*p.InterfaceID] = p
		case p.RouterID != nil:
			routerProbes[*p.RouterID] = append(routerProbes[*p.RouterID], p)
		}
	}
	sortProbes(horus)
	sites := map[uuid.UUID]*SiteView{}
	siteOrder := []uuid.UUID{}
	siteFor := func(id uuid.UUID, name string) *SiteView {
		s := sites[id]
		if s == nil {
			s = &SiteView{SiteName: name, Uplinks: []UplinkView{}, RouterProbes: []ProbeView{}, Routers: []RouterBrief{}}
			if id != uuid.Nil {
				sid := id
				s.SiteID = &sid
			}
			sites[id] = s
			siteOrder = append(siteOrder, id)
		}
		if s.SiteName == "" {
			s.SiteName = name
		}
		return s
	}
	for _, r := range routers {
		s := siteFor(r.SiteID, r.SiteName)
		b := RouterBrief{RouterID: r.RouterID, Name: r.Name, Status: r.Status}
		if f, ok := probeErr[r.RouterID]; ok {
			reason := f.Reason
			b.ProbesError = &reason
		}
		s.Routers = append(s.Routers, b)
		rp := routerProbes[r.RouterID]
		sortProbes(rp)
		s.RouterProbes = append(s.RouterProbes, rp...)
	}
	healthIn := map[uuid.UUID]*domain.SiteHealthInput{}
	for _, u := range ups {
		siteID := u.SiteID
		if siteID == uuid.Nil {
			siteID = data.meta[u.InterfaceID].SiteID
		}
		s := siteFor(siteID, u.SiteName)
		v := uplinkView(u, data, days, gwProbe, condBy[u.InterfaceID], fcBy[u.InterfaceID], st)
		if v.SiteID == nil && siteID != uuid.Nil {
			sid := siteID
			v.SiteID = &sid
		}
		s.Uplinks = append(s.Uplinks, v)
		hi := healthIn[siteID]
		if hi == nil {
			hi = &domain.SiteHealthInput{Settings: st}
			healthIn[siteID] = hi
		}
		hi.Uplinks = append(hi.Uplinks, uplinkHealth(u, v))
	}
	var services []domain.Service
	if c.o.Infra != nil {
		if services, err = c.o.Infra.TenantServices(ctx, tenant); err != nil {
			c.log.WarnContext(ctx, "snmp: infra services not read for site health", "error", err)
		}
	}
	for _, sid := range siteOrder {
		s := sites[sid]
		hi := healthIn[sid]
		if hi == nil {
			hi = &domain.SiteHealthInput{Settings: st}
		}
		for _, r := range s.Routers {
			reason := ""
			for _, rr := range routers {
				if rr.RouterID == r.RouterID {
					reason = rr.Reason
				}
			}
			hi.Routers = append(hi.Routers, domain.RouterHealthInput{Name: r.Name, Status: r.Status, Reason: reason})
		}
		for _, sv := range services {
			if sv.Enabled && sv.SiteID != nil && *sv.SiteID == sid {
				hi.Services = append(hi.Services, domain.ServiceHealthInput{Name: sv.Name, State: sv.Status.State})
			}
		}
		s.Health = domain.ComposeSiteHealth(*hi)
	}
	ov := &Overview{Data: make([]SiteView, 0, len(siteOrder)), HorusProbes: horus, Settings: st,
		Meta: OverviewMeta{GeneratedAt: now, WindowDays: days, History: c.series() != nil, Store: c.o.Repo != nil}}
	if ov.HorusProbes == nil {
		ov.HorusProbes = []ProbeView{}
	}
	for _, sid := range siteOrder {
		ov.Data = append(ov.Data, *sites[sid])
	}
	slices.SortStableFunc(ov.Data, func(a, b SiteView) int {
		if d := healthOrder(b.Health.Status) - healthOrder(a.Health.Status); d != 0 {
			return d
		}
		return strings.Compare(strings.ToLower(a.SiteName), strings.ToLower(b.SiteName))
	})
	if len(egressMembers) > 0 {
		ov.Egress = c.egressView(tenant, ups, egressMembers, data, days, fcBy)
	}
	return ov, nil
}

var healthOrderRank = map[string]int{api.StatusOffline: 5, api.StatusCritical: 4, api.StatusDegraded: 3, api.StatusWarning: 2,
	api.StatusUnknown: 1, api.StatusOnline: 0}

func healthOrder(s string) int { return healthOrderRank[s] }

func sortProbes(p []ProbeView) {
	slices.SortFunc(p, func(a, b ProbeView) int {
		if c := strings.Compare(a.Label, b.Label); c != 0 {
			return c
		}
		return strings.Compare(a.Target, b.Target)
	})
}

func uplinkView(u *uplinkRef, data *overviewData, days int, gw map[uuid.UUID]ProbeView, conds []ConditionView,
	fc *domain.ForecastRow, st domain.CapacitySettings) UplinkView {
	v := UplinkView{InterfaceID: u.InterfaceID, RouterID: u.RouterID, RouterName: u.RouterName, SiteName: u.SiteName, InterfaceName: u.Name,
		Configured: u.Config != nil, AutoDetected: u.Auto, Label: u.label(), Conditions: conds, Forecast: summarize(fc)}
	if v.Conditions == nil {
		v.Conditions = []ConditionView{}
	}
	if u.SiteID != uuid.Nil {
		sid := u.SiteID
		v.SiteID = &sid
	}
	if v.InterfaceName == "" {
		v.InterfaceName = data.meta[u.InterfaceID].Name
	}
	if u.Config != nil {
		k := u.Config.Kind
		v.Provider, v.Kind, v.InternationalEgress, v.ContractedBps, v.Version = u.Config.Provider, &k, u.Config.InternationalEgress,
			u.Config.ContractedBps, u.Config.Version
	}
	if u.Iface != nil {
		v.SpeedBps, v.OperStatus, v.RxBps, v.TxBps, v.SampledAt = u.Iface.SpeedBps, u.Iface.OperStatus, u.Iface.RxBps, u.Iface.TxBps, u.Iface.SampledAt
	} else if sp := data.meta[u.InterfaceID].SpeedBps; sp > 0 {
		v.SpeedBps = &sp
	}
	capBps := domain.CapacityBps(v.ContractedBps, v.SpeedBps)
	switch {
	case v.ContractedBps != nil:
		v.CapacitySource = "contracted"
	case capBps > 0:
		v.CapacitySource = "port_speed"
	default:
		v.CapacitySource = "unknown"
	}
	if capBps > 0 {
		v.CapacityBps = &capBps
	}
	ref := *u
	if ref.Iface == nil && v.SpeedBps != nil {
		ref.Iface = &IfaceSnapshot{SpeedBps: v.SpeedBps}
	}
	v.Utilization = liveUtil(&ref)
	daily := data.daily[u.InterfaceID]
	v.Window = windowOf(daily, days, capBps)
	if len(daily) > 0 {
		m := domain.MergeDaily(daily)
		// «1 h/día sobre el crítico» se juzga con la media diaria de la ventana; la utilización, cada día
		// contra la capacidad vigente ese día.
		v.Diagnosis = domain.DiagnoseWithUtil(domain.DailyStats{Samples: m.Samples, RxP95Bps: m.RxP95Bps, TxP95Bps: m.TxP95Bps,
			CapacityBps: capBps, HoursAboveCritical: m.HoursAboveCritical / float64(len(daily)), Errors: m.Errors,
			Drops: m.Drops, Packets: m.Packets}, domain.WindowUtilP95(daily, capBps), st.SaturationWarning, st.SaturationCritical)
	} else {
		v.Diagnosis = domain.Diagnosis{Kind: domain.DiagnosisNoData}
	}
	if p, ok := gw[u.InterfaceID]; ok {
		v.Probe = &p
	}
	return v
}

func uplinkHealth(u *uplinkRef, v UplinkView) domain.UplinkHealthInput {
	h := domain.UplinkHealthInput{Label: v.Label, UtilNow: v.Utilization, Diagnosis: v.Diagnosis.Kind, SaturationState: domain.LevelOK}
	if u.Iface != nil {
		h.OperUp = u.Iface.OperUp()
	}
	if v.Window != nil {
		h.UtilP95 = v.Window.UtilP95
	}
	for _, c := range v.Conditions {
		if c.Condition == domain.ConditionSaturation {
			h.SaturationState = c.Level
		}
	}
	if v.Probe != nil {
		// Pérdida y latencia: la media de 24 h; si la última lectura es peor, manda.
		h.LossPct, h.LatencyMs = v.Probe.LossPct, v.Probe.RTTAvgMs
		if v.Probe.LastLossPct != nil && (h.LossPct == nil || *v.Probe.LastLossPct > *h.LossPct) && v.Probe.Status != "unknown" {
			h.LossPct = v.Probe.LastLossPct
		}
	}
	return h
}

func (c *Capacity) egressView(tenant uuid.UUID, ups []*uplinkRef, members []uuid.UUID, data *overviewData, days int,
	fc map[uuid.UUID]*domain.ForecastRow) *EgressView {
	id := domain.EgressSubjectID(tenant)
	e := &EgressView{SubjectID: id, Members: members, Forecast: summarize(fc[id])}
	var rx, tx float64
	live := false
	for _, u := range ups {
		if !slices.Contains(members, u.InterfaceID) {
			continue
		}
		capBps := u.capacity()
		if capBps == 0 {
			if sp := data.meta[u.InterfaceID].SpeedBps; sp > 0 {
				capBps = sp
				if u.Config != nil && u.Config.ContractedBps != nil {
					capBps = *u.Config.ContractedBps
				}
			}
		}
		e.CapacityBps += capBps
		if u.Iface != nil && (u.Iface.RxBps != nil || u.Iface.TxBps != nil) {
			live = true
			if u.Iface.RxBps != nil {
				rx += *u.Iface.RxBps
			}
			if u.Iface.TxBps != nil {
				tx += *u.Iface.TxBps
			}
		}
	}
	if live {
		e.RxBps, e.TxBps = &rx, &tx
		if e.CapacityBps > 0 {
			ut := max(rx, tx) / float64(e.CapacityBps)
			e.Utilization = &ut
		}
	}
	e.Window = windowOf(data.daily[id], days, e.CapacityBps)
	return e
}

// FilterSites deja los nodos visibles (nil = todos) y, si site no es nil,
// solo ese nodo. La salida internacional solo con alcance completo.
func FilterSites(ov *Overview, visible []uuid.UUID, site *uuid.UUID) *Overview {
	out := *ov
	out.Data = make([]SiteView, 0, len(ov.Data))
	for _, s := range ov.Data {
		if len(visible) > 0 && (s.SiteID == nil || !slices.Contains(visible, *s.SiteID)) {
			continue
		}
		if site != nil && (s.SiteID == nil || *s.SiteID != *site) {
			continue
		}
		out.Data = append(out.Data, s)
	}
	if len(visible) > 0 {
		out.Egress = nil
		out.HorusProbes = []ProbeView{}
	}
	return &out
}

var _ api.CapacityReader = (*Capacity)(nil)

// SiteHealth implementa api.CapacityReader (widget site_health).
func (c *Capacity) SiteHealth(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID) ([]api.SiteHealthSummary, error) {
	ov, err := c.Overview(ctx, tenant, defaultWindowDays)
	if err != nil {
		return nil, err
	}
	ov = FilterSites(ov, sites, nil)
	out := make([]api.SiteHealthSummary, 0, len(ov.Data))
	for _, s := range ov.Data {
		sum := api.SiteHealthSummary{SiteName: s.SiteName, Status: s.Health.Status, Summary: s.Health.Summary,
			Reasons: apiSiteHealth(s.Health), Uplinks: len(s.Uplinks)}
		if s.SiteID != nil {
			sum.SiteID = *s.SiteID
		}
		fcRank := map[string]int{"": 0, domain.ForecastInsufficient: 1, domain.ForecastNoCapacity: 1, domain.ForecastOK: 2,
			domain.ForecastPlanning: 3, domain.ForecastUrgent: 4}
		for _, u := range s.Uplinks {
			if u.OperStatus != nil && *u.OperStatus != api.OperUp {
				sum.UplinksDown++
			}
			if u.Window != nil && u.Window.UtilP95 != nil && (sum.WorstUtilP95 == nil || *u.Window.UtilP95 > *sum.WorstUtilP95) {
				x := *u.Window.UtilP95
				sum.WorstUtilP95 = &x
			}
			if u.Probe != nil && u.Probe.LossPct != nil && (sum.WorstLossPct == nil || *u.Probe.LossPct > *sum.WorstLossPct) {
				x := *u.Probe.LossPct
				sum.WorstLossPct = &x
			}
			if u.Forecast != nil {
				if fcRank[u.Forecast.Level] > fcRank[sum.ForecastLevel] {
					sum.ForecastLevel = u.Forecast.Level
				}
				if d := u.Forecast.Planning.Date; d != nil && (sum.NextCrossing == nil || d.Before(*sum.NextCrossing)) {
					x := *d
					sum.NextCrossing = &x
				}
			}
		}
		out = append(out, sum)
	}
	return out, nil
}
