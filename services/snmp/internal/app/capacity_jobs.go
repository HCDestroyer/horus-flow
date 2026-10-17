package app

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Ritmos de los trabajos del análisis de capacidad.
const (
	probeRetryAfter   = 10 * time.Minute // tras un fallo de lectura de la API del router
	probeReadTimeout  = 15 * time.Second
	horusProbeCount   = 5
	horusProbeTimeout = 2 * time.Second
	monitorGap        = 5 * time.Minute // hueco que reinicia una racha
	dailyCheckEvery   = 30 * time.Minute
	dailyStartDelay   = 2 * time.Minute
	dailyLocalHour    = 0 // a partir de las 00:20 locales
	dailyLocalMinute  = 20
	backfillDays      = 89 // interface_5m guarda 90 días
	reloadEvery       = time.Minute
	satMargin         = 0.05 // histéresis de la saturación (5 puntos)
)

// Run ejecuta lector de sondas, monitor y trabajo diario hasta el apagado.
func (c *Capacity) Run(ctx context.Context, monitorEvery time.Duration) {
	if monitorEvery <= 0 {
		monitorEvery = time.Minute
	}
	var wg sync.WaitGroup
	for _, loop := range []struct {
		every time.Duration
		delay time.Duration
		fn    func(context.Context)
	}{
		{c.o.ProbeInterval, 5 * time.Second, c.collectProbes},
		{monitorEvery, 30 * time.Second, c.monitor},
		{reloadEvery, reloadEvery, func(ctx context.Context) {
			if err := c.Load(ctx); err != nil {
				c.log.WarnContext(ctx, "snmp: capacity config not reloaded (previous kept)", "error", err)
			}
		}},
		{dailyCheckEvery, dailyStartDelay, func(ctx context.Context) { c.RunDaily(ctx, false) }},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case <-time.After(loop.delay):
			}
			t := time.NewTicker(loop.every)
			defer t.Stop()
			for {
				loop.fn(ctx)
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	wg.Wait()
}

// collectProbes lee las sondas netwatch de cada router (API REST de solo
// lectura, vía devices) y hace las sondas de respaldo de Horus.
func (c *Capacity) collectProbes(ctx context.Context) {
	now := c.now().UTC()
	var routers []RouterSnapshot
	if c.o.Engine != nil {
		routers = c.o.Engine.Snapshot(now)
	}
	var mu sync.Mutex
	var samples []domain.ProbeSample
	add := func(s ...domain.ProbeSample) {
		mu.Lock()
		samples = append(samples, s...)
		mu.Unlock()
	}
	reader, ok := c.o.Probes()
	if ok {
		sem := make(chan struct{}, c.o.ProbeConcurrency)
		var wg sync.WaitGroup
		for i := range routers {
			r := routers[i]
			c.mu.RLock()
			f, failed := c.probeErr[r.RouterID]
			c.mu.RUnlock()
			if failed && now.Before(f.Retry) {
				continue
			}
			if r.Status == api.StatusOffline {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				rctx, cancel := context.WithTimeout(ctx, probeReadTimeout)
				defer cancel()
				probes, err := reader.ReadNetwatch(rctx, r.TenantID, r.RouterID)
				if err != nil {
					c.mu.Lock()
					c.probeErr[r.RouterID] = probeFailure{Reason: probeErrorText(err), At: now, Retry: now.Add(probeRetryAfter)}
					c.mu.Unlock()
					return
				}
				c.mu.Lock()
				delete(c.probeErr, r.RouterID)
				c.mu.Unlock()
				add(NetwatchSamples(r, probes, now)...)
			}()
		}
		wg.Wait()
	}
	add(c.horusProbes(ctx, now)...)
	if len(samples) == 0 {
		return
	}
	c.mu.Lock()
	for _, s := range samples {
		c.latest[s.ProbeID] = s
	}
	// Las sondas que desaparecen del router dejan de mostrarse a los 30 min.
	for id, s := range c.latest {
		if now.Sub(s.At) > 30*time.Minute {
			delete(c.latest, id)
		}
	}
	c.mu.Unlock()
	if s := c.series(); s != nil {
		if err := s.InsertProbes(ctx, samples); err != nil {
			c.log.WarnContext(ctx, "snmp: probe samples not written (dropped)", "error", err, "samples", len(samples))
		}
	}
}

func probeErrorText(err error) string {
	switch {
	case errors.Is(err, devapi.ErrRouterNotFound):
		return "router_not_found"
	case strings.Contains(err.Error(), "fingerprint not pinned"):
		return "tls_fingerprint_not_pinned"
	case strings.Contains(err.Error(), "no API credential"):
		return "no_api_credential"
	case strings.Contains(err.Error(), "no tunnel"):
		return "no_tunnel"
	case strings.Contains(err.Error(), "rejected"):
		return "auth_failed"
	case strings.Contains(err.Error(), "fingerprint"):
		return "tls_fingerprint_changed"
	}
	return "unreachable"
}

// NetwatchSamples convierte las sondas de Horus de un router en lecturas:
// solo las ICMP habilitadas con comentario `horus-uplink;…`; el enlace se
// empareja por el nombre saneado de la interfaz.
func NetwatchSamples(r RouterSnapshot, probes []devapi.NetwatchProbe, now time.Time) []domain.ProbeSample {
	byName := map[string]IfaceSnapshot{}
	for _, x := range r.Interfaces {
		byName[domain.SafeIfName(x.Name)] = x
	}
	var out []domain.ProbeSample
	for _, p := range probes {
		ref, ok := domain.ParseNetwatchComment(p.Comment)
		if !ok || p.Disabled || (p.Type != "icmp" && p.Type != "") {
			continue
		}
		s := domain.ProbeSample{TenantID: r.TenantID, SiteID: r.SiteID, RouterID: r.RouterID, Source: domain.ProbeSourceRouter, Ref: ref.Ref,
			Target: p.Host, At: now, Sent: p.SentCount, Received: p.ResponseCount, LossPct: p.LossPercent, RTTAvgMs: p.RTTAvgMs,
			RTTMinMs: p.RTTMinMs, RTTMaxMs: p.RTTMaxMs, JitterMs: p.RTTJitterMs}
		if s.JitterMs == nil {
			s.JitterMs = p.RTTStdevMs
		}
		switch p.Status {
		case "up":
			s.Up = boolPtr(true)
		case "down":
			s.Up = boolPtr(false)
			if s.LossPct == nil {
				s.LossPct = f64(100)
			}
		}
		label := "Internacional " + p.Host
		if ref.Interface != "" {
			if x, ok := byName[ref.Interface]; ok {
				s.InterfaceID = x.InterfaceID
				label = "Puerta de enlace por " + x.Name
			} else {
				label = "Puerta de enlace por " + ref.Interface + " (interfaz no encontrada)"
			}
		}
		s.Label = label
		s.ProbeID = domain.ProbeID(r.RouterID, p.Host, ref.Ref, ref.Interface)
		out = append(out, s)
	}
	return out
}

func boolPtr(v bool) *bool { return &v }

// horusProbes hace las sondas de respaldo desde el host de Horus («ruta
// Horus → destino») a los destinos configurados por cada ISP.
func (c *Capacity) horusProbes(ctx context.Context, now time.Time) []domain.ProbeSample {
	if c.o.Pinger == nil {
		return nil
	}
	c.mu.RLock()
	type job struct {
		tenant uuid.UUID
		t      domain.ProbeTarget
	}
	var jobs []job
	for tenant, st := range c.settings {
		for _, t := range st.ProbeTargets {
			jobs = append(jobs, job{tenant, t})
		}
	}
	c.mu.RUnlock()
	var mu sync.Mutex
	var out []domain.ProbeSample
	var wg sync.WaitGroup
	sem := make(chan struct{}, c.o.ProbeConcurrency)
	for _, j := range jobs {
		denied := false
		for _, p := range c.o.Deny {
			if p.Contains(j.t.Address) {
				denied = true
			}
		}
		if denied || !j.t.Address.Is4() {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := c.o.Pinger.Ping(ctx, j.t.Address, horusProbeCount, horusProbeTimeout)
			if err != nil {
				return
			}
			s := domain.ProbeSample{TenantID: j.tenant, Source: domain.ProbeSourceHorus, Ref: domain.ProbeSourceHorus, Target: j.t.Address.String(),
				Label: j.t.Label, At: now, ProbeID: domain.ProbeID(j.tenant, j.t.Address.String(), domain.ProbeSourceHorus, ""),
				Sent: intPtr(res.Sent), Received: intPtr(res.Received), LossPct: f64(res.Loss() * 100), Up: boolPtr(res.Received > 0)}
			if res.AvgRTT != nil {
				s.RTTAvgMs = f64(float64(*res.AvgRTT) / float64(time.Millisecond))
			}
			mu.Lock()
			out = append(out, s)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func intPtr(v int) *int { return &v }

// monitor evalúa las condiciones sostenidas de cada enlace (saturación con
// la última muestra SNMP; pérdida y latencia con la sonda de su puerta de
// enlace) y emite horus.snmp.uplink.condition_changed en cada cambio.
func (c *Capacity) monitor(ctx context.Context) {
	now := c.now().UTC()
	if c.o.Engine == nil {
		return
	}
	byTenant := map[uuid.UUID][]RouterSnapshot{}
	for _, r := range c.o.Engine.Snapshot(now) {
		byTenant[r.TenantID] = append(byTenant[r.TenantID], r)
	}
	c.mu.RLock()
	gw := map[uuid.UUID]domain.ProbeSample{}
	for _, s := range c.latest {
		if s.InterfaceID != uuid.Nil && s.Ref == domain.ProbeRefGateway && now.Sub(s.At) <= 3*c.o.ProbeInterval {
			gw[s.InterfaceID] = s
		}
	}
	c.mu.RUnlock()
	for tenant, routers := range byTenant {
		st := c.settingsFor(tenant)
		sustained := time.Duration(st.SustainedMinutes) * time.Minute
		recoverAfter := max(5*time.Minute, sustained/2)
		for _, u := range c.uplinksOf(tenant, routers) {
			if u.Iface == nil {
				continue
			}
			if util := liveUtil(u); util != nil && u.Iface.SampledAt != nil && now.Sub(*u.Iface.SampledAt) <= 3*time.Minute {
				c.step(ctx, tenant, u, domain.ConditionSaturation, *util, st.SaturationWarning, st.SaturationCritical, satMargin,
					*u.Iface.SampledAt, sustained, recoverAfter)
			}
			if p, ok := gw[u.InterfaceID]; ok {
				if p.LossPct != nil {
					c.step(ctx, tenant, u, domain.ConditionLoss, *p.LossPct, st.LossWarningPct, st.LossCriticalPct, st.LossWarningPct/2,
						p.At, sustained, recoverAfter)
				}
				if p.RTTAvgMs != nil {
					c.step(ctx, tenant, u, domain.ConditionLatency, *p.RTTAvgMs, st.LatencyWarningMs, st.LatencyCriticalMs,
						st.LatencyWarningMs*0.1, p.At, sustained, recoverAfter)
				}
			}
		}
	}
}

// conditionUnit es la unidad del valor de cada condición en el evento.
var conditionUnit = map[string]string{domain.ConditionSaturation: "ratio", domain.ConditionLoss: "percent", domain.ConditionLatency: "ms"}

func (c *Capacity) step(ctx context.Context, tenant uuid.UUID, u *uplinkRef, cond string, value, warn, crit, margin float64,
	at time.Time, sustained, recoverAfter time.Duration) {
	k := condKey{tenant, u.InterfaceID, cond}
	c.mu.Lock()
	e := c.conds[k]
	if e == nil {
		e = &condEntry{routerID: u.RouterID}
		c.conds[k] = e
	}
	before := e.st
	level := domain.LevelFor(value, warn, crit, margin, e.st.Level)
	prev, changed := e.st.Step(level, value, at, sustained, recoverAfter, monitorGap)
	after := e.st
	c.mu.Unlock()
	if !changed {
		return
	}
	threshold := warn
	if after.Level == domain.LevelCritical {
		threshold = crit
	}
	row := domain.ConditionRow{TenantID: tenant, InterfaceID: u.InterfaceID, RouterID: u.RouterID, Condition: cond, Level: after.Level,
		Since: after.Since, Value: value}
	tid := tenant
	data := map[string]any{"tenant_id": tenant.String(), "interface_id": u.InterfaceID.String(), "router_id": u.RouterID.String(),
		"site_id": nilUUID(u.SiteID), "router_name": u.RouterName, "site_name": u.SiteName, "interface_name": u.Name, "label": u.label(),
		"condition": cond, "previous_level": prev, "level": after.Level, "value": round3(value), "threshold": threshold,
		"unit": conditionUnit[cond], "since": after.Since.UTC().Format(outbox.TimeFormat), "observed_at": at.UTC().Format(outbox.TimeFormat)}
	ev := &outbox.Event{Type: TypeUplinkConditionChanged, Source: eventSource, TenantID: &tid, AggregateType: "uplink",
		AggregateID: u.InterfaceID, AggregateVersion: 1, Actor: outbox.Actor{Type: "service", ID: "svc:snmp"}, OccurredAt: at, Data: data}
	if c.o.Repo == nil {
		c.invalidate(tenant)
		return
	}
	if err := c.o.Repo.SaveCondition(ctx, row, ev); err != nil {
		// No se adopta el nivel: el siguiente ciclo lo reintenta.
		c.mu.Lock()
		e.st = before
		c.mu.Unlock()
		c.log.WarnContext(ctx, "snmp: uplink condition not saved (retried next cycle)", "error", err, "condition", cond)
		return
	}
	c.invalidate(tenant)
}

func nilUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id.String()
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// RunDaily calcula, para cada ISP cuyo día local ya cambió, los picos diarios
// que falten (hasta 90 días atrás) y los pronósticos de sus enlaces y de su
// salida internacional; emite horus.snmp.capacity.forecast_changed cuando un
// pronóstico cambia de nivel. force recalcula aunque ya se haya hecho hoy.
func (c *Capacity) RunDaily(ctx context.Context, force bool) {
	s := c.series()
	if s == nil || c.o.Engine == nil {
		return
	}
	now := c.now().UTC()
	tenants := map[uuid.UUID]TenantInfo{}
	if c.o.Tenants != nil {
		if m, ok := c.o.Tenants(ctx); ok {
			tenants = m
		}
	}
	snap := c.o.Engine.Snapshot(now)
	byTenant := map[uuid.UUID][]RouterSnapshot{}
	for _, r := range snap {
		byTenant[r.TenantID] = append(byTenant[r.TenantID], r)
	}
	c.mu.RLock()
	for t := range c.configs {
		if _, ok := byTenant[t]; !ok {
			byTenant[t] = nil
		}
	}
	c.mu.RUnlock()
	for tenant, routers := range byTenant {
		info, ok := tenants[tenant]
		if !ok {
			info = TenantInfo{ID: tenant, Timezone: "UTC", Active: true}
		}
		if !info.Active {
			continue
		}
		loc := location(info.Timezone)
		today, _, _ := localDay(now, loc)
		lt := now.In(loc)
		c.mu.RLock()
		done := c.dailyRun[tenant].Equal(today)
		c.mu.RUnlock()
		if !force && (done || lt.Hour() < dailyLocalHour || (lt.Hour() == dailyLocalHour && lt.Minute() < dailyLocalMinute)) {
			continue
		}
		if err := c.dailyTenant(ctx, s, tenant, routers, loc, today, now); err != nil {
			c.log.WarnContext(ctx, "snmp: capacity daily job failed (retried later)", "error", err)
			continue
		}
		c.mu.Lock()
		c.dailyRun[tenant] = today
		delete(c.cache, tenant)
		c.mu.Unlock()
	}
}

func (c *Capacity) dailyTenant(ctx context.Context, s CapacitySeries, tenant uuid.UUID, routers []RouterSnapshot, loc *time.Location,
	today, now time.Time) error {
	st := c.settingsFor(tenant)
	ups := c.uplinksOf(tenant, routers)
	if len(ups) == 0 {
		return nil
	}
	egressID := domain.EgressSubjectID(tenant)
	var egress []*uplinkRef
	ifaces := make([]uuid.UUID, 0, len(ups))
	for _, u := range ups {
		ifaces = append(ifaces, u.InterfaceID)
		if u.Config != nil && u.Config.InternationalEgress {
			egress = append(egress, u)
		}
	}
	last, err := s.LastDaily(ctx, tenant)
	if err != nil {
		return err
	}
	first := today.AddDate(0, 0, -backfillDays)
	need := func(id uuid.UUID, day time.Time) bool { l, ok := last[id]; return !ok || l.Before(day) }
	// Primer día pendiente de algún sujeto (como mucho 90 días atrás: lo que guarda interface_5m).
	start := today
	subjects := slices.Clone(ifaces)
	if len(egress) > 0 {
		subjects = append(subjects, egressID)
	}
	for _, id := range subjects {
		cand := first
		if l, ok := last[id]; ok && !l.Before(first) {
			cand = l.AddDate(0, 0, 1)
		}
		if cand.Before(start) {
			start = cand
		}
	}
	meta := map[uuid.UUID]clickhouse.IfaceMeta{}
	for day := start; day.Before(today); day = day.AddDate(0, 0, 1) {
		from, to := dayBounds(day, loc)
		buckets, m, err := s.Buckets(ctx, tenant, ifaces, from, to)
		if err != nil {
			return err
		}
		for k, v := range m {
			meta[k] = v
		}
		var rows []clickhouse.DailyRow
		var eb [][]domain.Bucket5m
		var ecap int64
		for _, u := range ups {
			capBps := capacityWithMeta(u, meta[u.InterfaceID])
			if need(u.InterfaceID, day) {
				if d, ok := domain.ComputeDaily(day, buckets[u.InterfaceID], capBps, st.SaturationWarning, st.SaturationCritical); ok {
					siteID := u.SiteID
					if siteID == uuid.Nil {
						siteID = meta[u.InterfaceID].SiteID
					}
					rows = append(rows, clickhouse.DailyRow{SubjectID: u.InterfaceID, SubjectKind: domain.SubjectUplink, RouterID: u.RouterID,
						SiteID: siteID, Stats: d})
				}
			}
			if u.Config != nil && u.Config.InternationalEgress {
				eb = append(eb, buckets[u.InterfaceID])
				ecap += capBps
			}
		}
		if len(egress) > 0 && need(egressID, day) {
			if d, ok := domain.ComputeDaily(day, domain.SumBuckets(eb), ecap, st.SaturationWarning, st.SaturationCritical); ok {
				rows = append(rows, clickhouse.DailyRow{SubjectID: egressID, SubjectKind: domain.SubjectInternationalEgress, Stats: d})
			}
		}
		if err := s.InsertDaily(ctx, tenant, rows, now); err != nil {
			return err
		}
	}
	return c.forecastTenant(ctx, s, tenant, ups, egress, meta, today, now, st)
}

func capacityWithMeta(u *uplinkRef, m clickhouse.IfaceMeta) int64 {
	if capBps := u.capacity(); capBps > 0 {
		return capBps
	}
	var contracted *int64
	if u.Config != nil {
		contracted = u.Config.ContractedBps
	}
	sp := m.SpeedBps
	return domain.CapacityBps(contracted, &sp)
}

func (c *Capacity) forecastTenant(ctx context.Context, s CapacitySeries, tenant uuid.UUID, ups, egress []*uplinkRef,
	meta map[uuid.UUID]clickhouse.IfaceMeta, today, now time.Time, st domain.CapacitySettings) error {
	egressID := domain.EgressSubjectID(tenant)
	subjects := make([]uuid.UUID, 0, len(ups)+1)
	for _, u := range ups {
		subjects = append(subjects, u.InterfaceID)
	}
	if len(egress) > 0 {
		subjects = append(subjects, egressID)
	}
	series, err := s.DailySeries(ctx, tenant, subjects, today.AddDate(0, 0, -domain.ForecastWindowDays), today.AddDate(0, 0, -1))
	if err != nil {
		return err
	}
	prev := map[uuid.UUID]string{}
	if c.o.Repo != nil {
		rows, err := c.o.Repo.ListForecasts(ctx, tenant)
		if err != nil {
			return err
		}
		for _, r := range rows {
			prev[r.SubjectID] = r.Level
		}
	}
	type subj struct {
		id     uuid.UUID
		kind   string
		label  string
		site   *uuid.UUID
		capBps int64
	}
	var all []subj
	var ecap int64
	for _, u := range ups {
		capBps := capacityWithMeta(u, meta[u.InterfaceID])
		var site *uuid.UUID
		if u.SiteID != uuid.Nil {
			sid := u.SiteID
			site = &sid
		}
		all = append(all, subj{u.InterfaceID, domain.SubjectUplink, u.label(), site, capBps})
		if u.Config != nil && u.Config.InternationalEgress {
			ecap += capBps
		}
	}
	if len(egress) > 0 {
		all = append(all, subj{egressID, domain.SubjectInternationalEgress, "Salida internacional", nil, ecap})
	}
	for _, x := range all {
		f := domain.RunForecast(domain.ForecastInput{Series: peaks(series[x.id]), CapacityBps: x.capBps, Planning: st.PlanningThreshold,
			Urgent: st.UrgentThreshold, LeadDays: st.LeadDays, Now: now})
		row := domain.ForecastRow{TenantID: tenant, SubjectID: x.id, SubjectKind: x.kind, SiteID: x.site, Label: x.label, Level: f.Level,
			ComputedAt: now, Forecast: f, Recommendation: domain.Recommendation(f, subjectLabel(x.kind, x.label))}
		if c.o.Repo == nil {
			continue
		}
		var ev *outbox.Event
		p, had := prev[x.id]
		if p != f.Level && (actionable(f.Level) || (had && actionable(p))) {
			tid := tenant
			ev = &outbox.Event{Type: TypeForecastChanged, Source: eventSource, TenantID: &tid, AggregateType: "capacity_forecast",
				AggregateID: x.id, AggregateVersion: 1, Actor: outbox.Actor{Type: "service", ID: "svc:snmp"}, OccurredAt: now,
				Data: forecastEventData(row, p)}
		}
		if err := c.o.Repo.SaveForecast(ctx, row, ev); err != nil {
			return err
		}
	}
	return nil
}

func actionable(level string) bool {
	return level == domain.ForecastPlanning || level == domain.ForecastUrgent
}

func fmtDate(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format("2006-01-02")
}

func forecastEventData(r domain.ForecastRow, prev string) map[string]any {
	f := r.Forecast
	d := map[string]any{"tenant_id": r.TenantID.String(), "subject_id": r.SubjectID.String(), "subject_kind": r.SubjectKind,
		"site_id": nil, "label": r.Label, "previous_level": prev, "level": r.Level, "capacity_bps": f.CapacityBps,
		"current_peak_bps": f.CurrentPeakBps, "monthly_growth": f.MonthlyGrowth, "confidence": f.Confidence,
		"planning_threshold": f.Planning.Threshold, "planning_date": fmtDate(f.Planning.Date), "days_until_planning": f.Planning.DaysUntil,
		"urgent_threshold": f.Urgent.Threshold, "urgent_date": fmtDate(f.Urgent.Date), "days_until_urgent": f.Urgent.DaysUntil,
		"lead_days": f.LeadDays, "recommendation": r.Recommendation, "computed_at": r.ComputedAt.UTC().Format(outbox.TimeFormat)}
	if r.SiteID != nil {
		d["site_id"] = r.SiteID.String()
	}
	return d
}
