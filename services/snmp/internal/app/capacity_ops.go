package app

import (
	"context"
	"net/netip"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Límites de las consultas de la API.
const (
	MinDetailDays     = 7
	MaxDetailDays     = 365
	DefaultDetailDays = 90
	MaxTrafficDays    = 90
	DefaultTraffic    = 30
)

// reloadTenant vuelve a leer configuraciones y ajustes de un ISP tras un
// cambio (en este proceso; las demás réplicas al recargar).
func (c *Capacity) reloadTenant(ctx context.Context, tenant uuid.UUID) {
	if c.o.Repo == nil {
		return
	}
	cfgs, err := c.o.Repo.ListUplinkConfigs(ctx, tenant)
	if err != nil {
		c.log.WarnContext(ctx, "snmp: uplink configs not reloaded", "error", err)
		return
	}
	row, err := c.o.Repo.GetCapacitySettings(ctx, tenant)
	if err != nil {
		c.log.WarnContext(ctx, "snmp: capacity settings not reloaded", "error", err)
		return
	}
	hist, err := c.o.Repo.ListCapacityHistory(ctx, tenant)
	if err != nil {
		c.log.WarnContext(ctx, "snmp: capacity history not reloaded", "error", err)
		return
	}
	c.mu.Lock()
	c.configs[tenant] = cfgs
	c.history[tenant] = hist
	if row != nil {
		c.settings[tenant] = row.Settings
	} else {
		delete(c.settings, tenant)
	}
	c.mu.Unlock()
	c.invalidate(tenant)
}

// findInterface busca la interfaz entre los routers sondeados del ISP.
func (c *Capacity) findInterface(tenant, iface uuid.UUID) (*RouterSnapshot, *IfaceSnapshot) {
	for _, r := range c.routersOf(tenant, c.now()) {
		for i := range r.Interfaces {
			if r.Interfaces[i].InterfaceID == iface {
				rr := r
				return &rr, &rr.Interfaces[i]
			}
		}
	}
	return nil, nil
}

func siteAllowed(sites []uuid.UUID, site uuid.UUID) bool {
	return len(sites) == 0 || slices.Contains(sites, site)
}

// PutUplink guarda la configuración de un enlace. La interfaz debe ser de un
// router sondeado del ISP (o tener ya configuración) y estar en su alcance.
// Devuelve también la configuración anterior (auditoría).
func (c *Capacity) PutUplink(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, iface uuid.UUID, in domain.UplinkConfigInput,
	actor string, expect int) (*domain.UplinkConfig, *domain.UplinkConfig, error) {
	if c.o.Repo == nil {
		return nil, nil, ErrCapacityNoStore
	}
	before, err := c.o.Repo.GetUplinkConfig(ctx, tenant, iface)
	if err != nil {
		return nil, nil, err
	}
	r, _ := c.findInterface(tenant, iface)
	switch {
	case r != nil:
		if r.RouterID != in.RouterID || !siteAllowed(sites, r.SiteID) {
			return nil, nil, ErrUplinkNotFound
		}
	case before != nil && before.RouterID == in.RouterID:
		if len(sites) > 0 {
			return nil, nil, ErrUplinkNotFound
		}
	default:
		return nil, nil, ErrUplinkNotFound
	}
	if in.CapacityValidFrom == nil {
		now := c.now().UTC()
		in.CapacityValidFrom = &now
	}
	after, err := c.o.Repo.PutUplinkConfig(ctx, tenant, iface, in, actor, expect)
	if err != nil {
		return nil, nil, err
	}
	c.reloadTenant(ctx, tenant)
	return after, before, nil
}

// DeleteUplink borra la configuración de un enlace (vuelve a solo detección).
func (c *Capacity) DeleteUplink(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, iface uuid.UUID) (*domain.UplinkConfig, error) {
	if c.o.Repo == nil {
		return nil, ErrCapacityNoStore
	}
	before, err := c.o.Repo.GetUplinkConfig(ctx, tenant, iface)
	if err != nil {
		return nil, err
	}
	if before == nil {
		return nil, ErrUplinkNotFound
	}
	if len(sites) > 0 {
		r, _ := c.findInterface(tenant, iface)
		if r == nil || !siteAllowed(sites, r.SiteID) {
			return nil, ErrUplinkNotFound
		}
	}
	if _, err := c.o.Repo.DeleteUplinkConfig(ctx, tenant, iface); err != nil {
		return nil, err
	}
	c.reloadTenant(ctx, tenant)
	return before, nil
}

// SettingsView es GET /capacity/settings.
type SettingsView struct {
	domain.CapacitySettings
	Defaults         domain.CapacitySettings `json:"defaults"`
	Customized       bool                    `json:"customized"`
	Version          int                     `json:"version"`
	UpdatedAt        *time.Time              `json:"updated_at"`
	TenantCountry    *string                 `json:"tenant_country"`
	EffectiveCountry *string                 `json:"effective_country"`
	// LocalPrefixWarnings: prefijos locales que se solapan con prefijos de
	// clientes del ISP o que se cuentan por su /24 o /48 (no impiden guardar).
	LocalPrefixWarnings []domain.LocalPrefixWarning `json:"local_prefix_warnings"`
}

func (c *Capacity) settingsView(ctx context.Context, tenant uuid.UUID, row *domain.SettingsRow) SettingsView {
	v := SettingsView{CapacitySettings: domain.DefaultCapacitySettings(), Defaults: domain.DefaultCapacitySettings()}
	if row != nil {
		at := row.UpdatedAt
		v.CapacitySettings, v.Customized, v.Version, v.UpdatedAt = row.Settings, true, row.Version, &at
	}
	if tc := c.tenantInfo(ctx, tenant).Country; tc != "" {
		v.TenantCountry = &tc
	}
	v.EffectiveCountry = v.TenantCountry
	if v.Country != nil {
		v.EffectiveCountry = v.Country
	}
	var clients []netip.Prefix
	if c.o.ClientPrefixes != nil {
		clients = c.o.ClientPrefixes(tenant)
	}
	v.LocalPrefixWarnings = domain.LocalPrefixWarnings(v.LocalPrefixes, clients)
	return v
}

// GetSettings devuelve los ajustes vigentes.
func (c *Capacity) GetSettings(ctx context.Context, tenant uuid.UUID) (SettingsView, error) {
	if c.o.Repo == nil {
		return c.settingsView(ctx, tenant, nil), nil
	}
	row, err := c.o.Repo.GetCapacitySettings(ctx, tenant)
	if err != nil {
		return SettingsView{}, err
	}
	return c.settingsView(ctx, tenant, row), nil
}

// PutSettings guarda (ya validados) los ajustes del ISP.
func (c *Capacity) PutSettings(ctx context.Context, tenant uuid.UUID, v domain.CapacitySettings, actor string, expect int) (SettingsView, error) {
	if c.o.Repo == nil {
		return SettingsView{}, ErrCapacityNoStore
	}
	row, err := c.o.Repo.PutCapacitySettings(ctx, tenant, v, actor, expect)
	if err != nil {
		return SettingsView{}, err
	}
	c.reloadTenant(ctx, tenant)
	return c.settingsView(ctx, tenant, row), nil
}

// ResetSettings vuelve a los ajustes por defecto.
func (c *Capacity) ResetSettings(ctx context.Context, tenant uuid.UUID) (SettingsView, error) {
	if c.o.Repo == nil {
		return SettingsView{}, ErrCapacityNoStore
	}
	if err := c.o.Repo.DeleteCapacitySettings(ctx, tenant); err != nil {
		return SettingsView{}, err
	}
	c.reloadTenant(ctx, tenant)
	return c.settingsView(ctx, tenant, nil), nil
}

// ProbePoint es un punto horario de una sonda (ProbeSeries del contrato).
type ProbePoint struct {
	At       time.Time `json:"at"`
	LossPct  *float64  `json:"loss_pct"`
	RTTAvgMs *float64  `json:"rtt_avg_ms"`
	JitterMs *float64  `json:"jitter_ms"`
}

// ProbeSeries es la serie horaria de una sonda.
type ProbeSeries struct {
	ProbeID uuid.UUID    `json:"probe_id"`
	Label   string       `json:"label"`
	Target  string       `json:"target"`
	Points  []ProbePoint `json:"points"`
}

// SubjectDetail es GET /capacity/uplinks/{id} y GET /capacity/egress.
type SubjectDetail struct {
	SubjectID      uuid.UUID               `json:"subject_id"`
	SubjectKind    string                  `json:"subject_kind"`
	Uplink         *UplinkView             `json:"uplink"`
	Egress         *EgressView             `json:"egress"`
	Days           int                     `json:"days"`
	Daily          []domain.DailyStats     `json:"daily"`
	Forecast       *domain.Forecast        `json:"forecast"`
	Recommendation *string                 `json:"recommendation"`
	ComputedAt     *time.Time              `json:"computed_at"`
	ProbeSeries    []ProbeSeries           `json:"probe_series"`
	Settings       domain.CapacitySettings `json:"settings"`
	// CapacityHistory es la capacidad contratada del enlace con su vigencia
	// (más reciente primero; vacía en la salida internacional o sin configuración).
	CapacityHistory []domain.CapacityPeriod `json:"capacity_history"`
}

// UplinkDetail devuelve la serie diaria, el pronóstico completo y las
// sondas de un enlace del alcance.
func (c *Capacity) UplinkDetail(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, iface uuid.UUID, days int) (*SubjectDetail, error) {
	ov, err := c.Overview(ctx, tenant, defaultWindowDays)
	if err != nil {
		return nil, err
	}
	var view *UplinkView
	for _, s := range FilterSites(ov, sites, nil).Data {
		for i := range s.Uplinks {
			if s.Uplinks[i].InterfaceID == iface {
				u := s.Uplinks[i]
				view = &u
			}
		}
	}
	if view == nil {
		return nil, ErrUplinkNotFound
	}
	d := &SubjectDetail{SubjectID: iface, SubjectKind: domain.SubjectUplink, Uplink: view, Settings: ov.Settings,
		CapacityHistory: c.historyOf(tenant, iface)}
	if err := c.fillDetail(ctx, tenant, d, days, view.Label, view.CapacityBps); err != nil {
		return nil, err
	}
	if view.Probe != nil {
		d.ProbeSeries, err = c.probeSeries(ctx, tenant, []ProbeView{*view.Probe}, days)
		if err != nil {
			c.log.WarnContext(ctx, "snmp: probe series not read", "error", err)
		}
	}
	if d.ProbeSeries == nil {
		d.ProbeSeries = []ProbeSeries{}
	}
	return d, nil
}

// EgressDetail es UplinkDetail para la salida internacional agregada
// (solo con alcance completo).
func (c *Capacity) EgressDetail(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, days int) (*SubjectDetail, error) {
	if len(sites) > 0 {
		return nil, ErrUplinkNotFound
	}
	ov, err := c.Overview(ctx, tenant, defaultWindowDays)
	if err != nil {
		return nil, err
	}
	if ov.Egress == nil {
		return nil, ErrUplinkNotFound
	}
	d := &SubjectDetail{SubjectID: ov.Egress.SubjectID, SubjectKind: domain.SubjectInternationalEgress, Egress: ov.Egress,
		Settings: ov.Settings, ProbeSeries: []ProbeSeries{}, CapacityHistory: []domain.CapacityPeriod{}}
	capBps := ov.Egress.CapacityBps
	if err := c.fillDetail(ctx, tenant, d, days, "Salida internacional", &capBps); err != nil {
		return nil, err
	}
	return d, nil
}

// historyOf devuelve la historia de capacidad de un enlace, la más reciente primero.
func (c *Capacity) historyOf(tenant, iface uuid.UUID) []domain.CapacityPeriod {
	c.mu.RLock()
	h := slices.Clone(c.history[tenant][iface])
	c.mu.RUnlock()
	slices.Reverse(h)
	if h == nil {
		h = []domain.CapacityPeriod{}
	}
	return h
}

func (c *Capacity) fillDetail(ctx context.Context, tenant uuid.UUID, d *SubjectDetail, days int, label string, capBps *int64) error {
	if days <= 0 {
		days = DefaultDetailDays
	}
	d.Days, d.Daily = days, []domain.DailyStats{}
	now := c.now().UTC()
	loc := location(c.tenantInfo(ctx, tenant).Timezone)
	today, _, _ := localDay(now, loc)
	if s := c.series(); s != nil {
		from := today.AddDate(0, 0, -days)
		if days < domain.ForecastWindowDays {
			from = today.AddDate(0, 0, -domain.ForecastWindowDays)
		}
		series, err := s.DailySeries(ctx, tenant, []uuid.UUID{d.SubjectID}, from, today)
		if err != nil {
			return err
		}
		all := series[d.SubjectID]
		cut := today.AddDate(0, 0, -days)
		for _, x := range all {
			if !x.Day.Before(cut) {
				d.Daily = append(d.Daily, x)
			}
		}
		// Pronóstico: el guardado por el trabajo diario; si aún no hay, al vuelo.
		if c.o.Repo != nil {
			if rows, err := c.o.Repo.ListForecasts(ctx, tenant); err == nil {
				for i := range rows {
					if rows[i].SubjectID == d.SubjectID {
						f := rows[i]
						d.Forecast, d.Recommendation, d.ComputedAt = &f.Forecast, &f.Recommendation, &f.ComputedAt
					}
				}
			}
		}
		if d.Forecast == nil && len(all) > 0 {
			st := c.settingsFor(tenant)
			var cb int64
			if capBps != nil {
				cb = *capBps
			}
			f := domain.RunForecast(domain.ForecastInput{Series: peaks(all), CapacityBps: cb, Planning: st.PlanningThreshold,
				Urgent: st.UrgentThreshold, LeadDays: st.LeadDays, Now: now})
			rec := domain.Recommendation(f, subjectLabel(d.SubjectKind, label))
			d.Forecast, d.Recommendation, d.ComputedAt = &f, &rec, &now
		}
	}
	return nil
}

func peaks(days []domain.DailyStats) []domain.DailyPeak {
	out := make([]domain.DailyPeak, 0, len(days))
	for _, d := range days {
		out = append(out, domain.DailyPeak{Day: d.Day, Bps: d.PeakP95Bps()})
	}
	return out
}

func (c *Capacity) probeSeries(ctx context.Context, tenant uuid.UUID, probes []ProbeView, days int) ([]ProbeSeries, error) {
	s := c.series()
	if s == nil || len(probes) == 0 {
		return []ProbeSeries{}, nil
	}
	ids := make([]uuid.UUID, 0, len(probes))
	for _, p := range probes {
		ids = append(ids, p.ProbeID)
	}
	if days <= 0 || days > 90 {
		days = min(max(days, 1), 90)
	}
	now := c.now().UTC()
	series, err := s.ProbeSeries(ctx, tenant, ids, now.AddDate(0, 0, -days), now)
	if err != nil {
		return nil, err
	}
	out := make([]ProbeSeries, 0, len(probes))
	for _, p := range probes {
		ps := ProbeSeries{ProbeID: p.ProbeID, Label: p.Label, Target: p.Target, Points: []ProbePoint{}}
		for _, x := range series[p.ProbeID] {
			ps.Points = append(ps.Points, ProbePoint{At: x.At, LossPct: x.LossPct, RTTAvgMs: x.RTTAvgMs, JitterMs: x.JitterMs})
		}
		out = append(out, ps)
	}
	return out, nil
}

// SiteTraffic es el reparto de un nodo.
type SiteTraffic struct {
	SiteID   uuid.UUID `json:"site_id"`
	SiteName string    `json:"site_name"`
	domain.TrafficSplit
}

// DailyTraffic es el reparto de un día (todo el alcance).
type DailyTraffic struct {
	Day                time.Time `json:"day"`
	LocalBytes         uint64    `json:"local_bytes"`
	InternationalBytes uint64    `json:"international_bytes"`
	UnknownBytes       uint64    `json:"unknown_bytes"`
}

// TrafficSplitView es GET /capacity/traffic-split.
type TrafficSplitView struct {
	From          time.Time           `json:"from"`
	To            time.Time           `json:"to"`
	Days          int                 `json:"days"`
	Country       *string             `json:"country"`
	CountrySource string              `json:"country_source"`
	LocalASNs     int                 `json:"local_asns"`
	LocalPrefixes int                 `json:"local_prefixes"`
	Total         domain.TrafficSplit `json:"total"`
	Sites         []SiteTraffic       `json:"sites"`
	Daily         []DailyTraffic      `json:"daily"`
}

// TrafficSplit reparte el tráfico remoto entre local e internacional por
// nodo y por día (flows.site_1d), con el país del ISP (o el de los ajustes)
// y su lista de ASN locales.
func (c *Capacity) TrafficSplit(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, days int) (*TrafficSplitView, error) {
	if days <= 0 {
		days = DefaultTraffic
	}
	st := c.settingsFor(tenant)
	info := c.tenantInfo(ctx, tenant)
	loc := location(info.Timezone)
	now := c.now().UTC()
	_, todayStart, _ := localDay(now, loc)
	from := todayStart.AddDate(0, 0, -days)
	out := &TrafficSplitView{From: from, To: todayStart, Days: days, LocalASNs: len(st.LocalASNs), LocalPrefixes: len(st.LocalPrefixes),
		CountrySource: "none",
		Sites:         []SiteTraffic{}, Daily: []DailyTraffic{}}
	country := ""
	switch {
	case st.Country != nil:
		country, out.CountrySource = *st.Country, "settings"
	case info.Country != "":
		country, out.CountrySource = info.Country, "tenant"
	}
	if country != "" {
		out.Country = &country
	}
	s := c.series()
	if s == nil {
		out.Total.Finish()
		return out, nil
	}
	// site_1d usa días UTC: se amplía un día a cada lado y se filtra por fecha.
	rows, err := s.TrafficByCountry(ctx, tenant, sites, st.LocalASNs, from.Add(-24*time.Hour).Truncate(24*time.Hour), todayStart.Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	names := map[uuid.UUID]string{}
	for _, r := range c.routersOf(tenant, now) {
		names[r.SiteID] = r.SiteName
	}
	bySite := map[uuid.UUID]*SiteTraffic{}
	byDay := map[time.Time]*DailyTraffic{}
	firstDay := from.Truncate(24 * time.Hour)
	for _, v := range rows {
		if v.Day.Before(firstDay) || !v.Day.Before(todayStart.Truncate(24*time.Hour)) {
			continue
		}
		out.Total.Add(v, country)
		x := bySite[v.SiteID]
		if x == nil {
			x = &SiteTraffic{SiteID: v.SiteID, SiteName: names[v.SiteID]}
			bySite[v.SiteID] = x
		}
		x.Add(v, country)
		dd := byDay[v.Day]
		if dd == nil {
			dd = &DailyTraffic{Day: v.Day}
			byDay[v.Day] = dd
		}
		var one domain.TrafficSplit
		one.Add(v, country)
		dd.LocalBytes += one.LocalBytes
		dd.InternationalBytes += one.InternationalBytes
		dd.UnknownBytes += one.UnknownBytes
	}
	// Lista local por prefijos: el volumen hacia esas redes pasa a local (manda sobre el ASN).
	byPrefix, err := s.TrafficByLocalPrefix(ctx, tenant, sites, st.LocalASNs, st.LocalPrefixes, from.Truncate(24*time.Hour),
		todayStart.Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	for _, v := range byPrefix {
		if v.Day.Before(firstDay) || !v.Day.Before(todayStart.Truncate(24*time.Hour)) {
			continue
		}
		out.Total.MoveToPrefix(v, country)
		if x := bySite[v.SiteID]; x != nil {
			x.MoveToPrefix(v, country)
		}
		if dd := byDay[v.Day]; dd != nil {
			one := domain.TrafficSplit{LocalBytes: dd.LocalBytes, InternationalBytes: dd.InternationalBytes, UnknownBytes: dd.UnknownBytes}
			one.MoveToPrefix(v, country)
			dd.LocalBytes, dd.InternationalBytes, dd.UnknownBytes = one.LocalBytes, one.InternationalBytes, one.UnknownBytes
		}
	}
	out.Total.Finish()
	for _, x := range bySite {
		x.Finish()
		out.Sites = append(out.Sites, *x)
	}
	slices.SortFunc(out.Sites, func(a, b SiteTraffic) int {
		ta, tb := a.LocalBytes+a.InternationalBytes+a.UnknownBytes, b.LocalBytes+b.InternationalBytes+b.UnknownBytes
		switch {
		case ta > tb:
			return -1
		case ta < tb:
			return 1
		}
		return 0
	})
	for _, d := range byDay {
		out.Daily = append(out.Daily, *d)
	}
	slices.SortFunc(out.Daily, func(a, b DailyTraffic) int { return a.Day.Compare(b.Day) })
	return out, nil
}

// SnippetView es GET /routers/{id}/netwatch-snippet.
type SnippetView struct {
	RouterID   uuid.UUID `json:"router_id"`
	RouterName string    `json:"router_name"`
	domain.NetwatchSnippet
	ProbesFound int     `json:"probes_found"`
	ProbesError *string `json:"probes_error"`
}

// NetwatchSnippet genera el script de sondas de un router del alcance con
// sus enlaces (detectado y configurados).
func (c *Capacity) NetwatchSnippet(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, routerID uuid.UUID) (*SnippetView, error) {
	routers := c.routersOf(tenant, c.now())
	var r *RouterSnapshot
	for i := range routers {
		if routers[i].RouterID == routerID {
			r = &routers[i]
		}
	}
	if r == nil || !siteAllowed(sites, r.SiteID) {
		return nil, ErrUplinkNotFound
	}
	var ups []domain.SnippetUplink
	for _, u := range c.uplinksOf(tenant, []RouterSnapshot{*r}) {
		if u.RouterID == routerID && u.Name != "" {
			ups = append(ups, domain.SnippetUplink{Name: u.Name, Label: u.label()})
		}
	}
	v := &SnippetView{RouterID: r.RouterID, RouterName: r.Name, NetwatchSnippet: domain.BuildNetwatchSnippet(r.Name, ups, domain.DefaultInternationalRefs)}
	c.mu.RLock()
	for _, s := range c.latest {
		if s.RouterID == routerID {
			v.ProbesFound++
		}
	}
	if f, ok := c.probeErr[routerID]; ok {
		reason := f.Reason
		v.ProbesError = &reason
	}
	c.mu.RUnlock()
	return v, nil
}

// compile-time: el adaptador ClickHouse cumple el puerto.
var _ CapacitySeries = (*clickhouse.Capacity)(nil)
