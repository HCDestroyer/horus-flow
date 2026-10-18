package app

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/outbox"
	"github.com/hcdestroyer/horus-flow/packages/go/pgdb"
	devapi "github.com/hcdestroyer/horus-flow/services/devices/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/api"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/clickhouse"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/icmp"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/adapters/pgstore"
	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Errores del análisis de capacidad.
var (
	// ErrCapacityNoStore: sin PostgreSQL no hay configuración por enlace ni ajustes.
	ErrCapacityNoStore = errors.New("snmp: capacity store not configured")
	// ErrUplinkNotFound: la interfaz no es de un router del tenant (o no se ve con su alcance).
	ErrUplinkNotFound = errors.New("snmp: uplink not found")
	// ErrCapacityVersion: If-Match no coincide.
	ErrCapacityVersion = pgstore.ErrVersion
	// ErrTooManyUplinks: máximo de enlaces configurados alcanzado.
	ErrTooManyUplinks = pgstore.ErrTooMany
	// ErrCapacityValidFrom: el cambio de capacidad se fecha antes del anterior.
	ErrCapacityValidFrom = pgstore.ErrCapacityValidFrom
)

// Tipos de evento del análisis de capacidad (docs/events.md §8.4).
const (
	TypeUplinkConditionChanged = "horus.snmp.uplink.condition_changed"
	TypeForecastChanged        = "horus.snmp.capacity.forecast_changed"
	eventSource                = "horus/snmp"
	overviewTTL                = 60 * time.Second
	defaultWindowDays          = 7
	maxWindowDays              = 30
	probeStaleAfter            = 5 * time.Minute
)

// CapacityRepo es el almacén PostgreSQL del análisis de capacidad.
type CapacityRepo interface {
	ListUplinkConfigs(ctx context.Context, tenant uuid.UUID) ([]domain.UplinkConfig, error)
	GetUplinkConfig(ctx context.Context, tenant, iface uuid.UUID) (*domain.UplinkConfig, error)
	PutUplinkConfig(ctx context.Context, tenant, iface uuid.UUID, in domain.UplinkConfigInput, actor string, expect int) (*domain.UplinkConfig, error)
	DeleteUplinkConfig(ctx context.Context, tenant, iface uuid.UUID) (bool, error)
	ListCapacityHistory(ctx context.Context, tenant uuid.UUID) (map[uuid.UUID][]domain.CapacityPeriod, error)
	GetCapacitySettings(ctx context.Context, tenant uuid.UUID) (*domain.SettingsRow, error)
	PutCapacitySettings(ctx context.Context, tenant uuid.UUID, v domain.CapacitySettings, actor string, expect int) (*domain.SettingsRow, error)
	DeleteCapacitySettings(ctx context.Context, tenant uuid.UUID) error
	LoadCapacity(ctx context.Context) (*pgstore.CapacityState, error)
	SaveCondition(ctx context.Context, c domain.ConditionRow, ev *outbox.Event) error
	ListConditions(ctx context.Context, tenant uuid.UUID) ([]domain.ConditionRow, error)
	SaveForecast(ctx context.Context, f domain.ForecastRow, ev *outbox.Event) error
	ListForecasts(ctx context.Context, tenant uuid.UUID) ([]domain.ForecastRow, error)
}

// CapacitySeries son las series ClickHouse del análisis de capacidad.
type CapacitySeries interface {
	InsertProbes(ctx context.Context, samples []domain.ProbeSample) error
	InsertDaily(ctx context.Context, tenant uuid.UUID, rows []clickhouse.DailyRow, at time.Time) error
	Buckets(ctx context.Context, tenant uuid.UUID, ifaces []uuid.UUID, from, to time.Time) (map[uuid.UUID][]domain.Bucket5m, map[uuid.UUID]clickhouse.IfaceMeta, error)
	LastDaily(ctx context.Context, tenant uuid.UUID) (map[uuid.UUID]time.Time, error)
	DailySeries(ctx context.Context, tenant uuid.UUID, subjects []uuid.UUID, from, to time.Time) (map[uuid.UUID][]domain.DailyStats, error)
	ProbeSummaries(ctx context.Context, tenant uuid.UUID, from time.Time) ([]clickhouse.ProbeAgg, error)
	ProbeSeries(ctx context.Context, tenant uuid.UUID, probes []uuid.UUID, from, to time.Time) (map[uuid.UUID][]clickhouse.ProbePoint, error)
	TrafficByCountry(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, localASNs []uint32, from, to time.Time) ([]domain.ASNVolume, error)
	TrafficByLocalPrefix(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, localASNs []uint32, prefixes []netip.Prefix,
		from, to time.Time) ([]domain.ASNVolume, error)
}

// TenantInfo es lo que el análisis necesita de un ISP (auth).
type TenantInfo struct {
	ID       uuid.UUID
	Timezone string
	Country  string
	Active   bool
}

// CapacityOptions configura el análisis.
type CapacityOptions struct {
	Repo    CapacityRepo   // nil = sin PostgreSQL
	Series  CapacitySeries // nil = sin ClickHouse
	Engine  *Engine
	Infra   *Infra
	Probes  func() (devapi.RouterProbeReader, bool)
	Tenants func(ctx context.Context) (map[uuid.UUID]TenantInfo, bool)
	// ClientPrefixes devuelve los prefijos de clientes del ISP (inventario de
	// flujos) para avisar de solapes con la lista local por prefijos.
	ClientPrefixes func(tenant uuid.UUID) []netip.Prefix
	Pinger         icmp.Pinger
	Deny           []netip.Prefix
	// ProbeInterval y ProbeConcurrency del lector de sondas.
	ProbeInterval    time.Duration
	ProbeConcurrency int
	Logger           *slog.Logger
	Now              func() time.Time
}

// Capacity es el análisis de capacidad de enlaces de subida: configuración,
// sondas, monitor de condiciones sostenidas, trabajo diario de picos y
// pronósticos, y las vistas de la API.
type Capacity struct {
	o   CapacityOptions
	log *slog.Logger
	now func() time.Time

	mu       sync.RWMutex
	configs  map[uuid.UUID][]domain.UplinkConfig
	settings map[uuid.UUID]domain.CapacitySettings
	history  map[uuid.UUID]map[uuid.UUID][]domain.CapacityPeriod // tenant → interfaz → vigencias
	loadedAt time.Time
	latest   map[uuid.UUID]domain.ProbeSample // probe_id → última lectura
	probeErr map[uuid.UUID]probeFailure       // router_id → último fallo de lectura
	conds    map[condKey]*condEntry
	dailyRun map[uuid.UUID]time.Time // tenant → último día local procesado
	cache    map[uuid.UUID]cachedOverview
}

type probeFailure struct {
	Reason string
	At     time.Time
	Retry  time.Time
}

type condKey struct {
	Tenant    uuid.UUID
	Interface uuid.UUID
	Condition string
}

type condEntry struct {
	st       domain.ConditionState
	routerID uuid.UUID
}

type cachedOverview struct {
	at time.Time
	ov *Overview
}

// NewCapacity crea el análisis.
func NewCapacity(o CapacityOptions) *Capacity {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ProbeInterval <= 0 {
		o.ProbeInterval = time.Minute
	}
	if o.ProbeConcurrency <= 0 {
		o.ProbeConcurrency = 8
	}
	if o.Probes == nil {
		o.Probes = func() (devapi.RouterProbeReader, bool) { return nil, false }
	}
	return &Capacity{o: o, log: o.Logger, now: o.Now, configs: map[uuid.UUID][]domain.UplinkConfig{},
		settings: map[uuid.UUID]domain.CapacitySettings{}, history: map[uuid.UUID]map[uuid.UUID][]domain.CapacityPeriod{}, latest: map[uuid.UUID]domain.ProbeSample{},
		probeErr: map[uuid.UUID]probeFailure{}, conds: map[condKey]*condEntry{}, dailyRun: map[uuid.UUID]time.Time{},
		cache: map[uuid.UUID]cachedOverview{}}
}

// SetSeries conecta ClickHouse (se abre en Start del módulo).
func (c *Capacity) SetSeries(s CapacitySeries) {
	c.mu.Lock()
	c.o.Series = s
	c.mu.Unlock()
}

// Series devuelve el adaptador ClickHouse (nil sin ClickHouse).
func (c *Capacity) Series() CapacitySeries { return c.series() }

func (c *Capacity) series() CapacitySeries {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.o.Series
}

// Load carga configuraciones, ajustes y el estado de las condiciones de todos
// los ISP (rol de plataforma).
func (c *Capacity) Load(ctx context.Context) error {
	if c.o.Repo == nil {
		return nil
	}
	st, err := c.o.Repo.LoadCapacity(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configs, c.settings, c.loadedAt = st.Configs, st.Settings, c.now()
	if st.History != nil {
		c.history = st.History
	}
	for _, r := range st.Conditions {
		k := condKey{r.TenantID, r.InterfaceID, r.Condition}
		if _, ok := c.conds[k]; !ok {
			c.conds[k] = &condEntry{st: domain.ConditionState{Level: r.Level, Since: r.Since, Value: r.Value}, routerID: r.RouterID}
		}
	}
	return nil
}

// settingsFor devuelve los ajustes vigentes del ISP.
func (c *Capacity) settingsFor(tenant uuid.UUID) domain.CapacitySettings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if s, ok := c.settings[tenant]; ok {
		return s
	}
	return domain.DefaultCapacitySettings()
}

func (c *Capacity) configsFor(tenant uuid.UUID) []domain.UplinkConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.configs[tenant])
}

func (c *Capacity) invalidate(tenant uuid.UUID) {
	c.mu.Lock()
	delete(c.cache, tenant)
	c.mu.Unlock()
}

// tenantInfo devuelve zona horaria y país del ISP (UTC y sin país si auth no
// está en el proceso).
func (c *Capacity) tenantInfo(ctx context.Context, tenant uuid.UUID) TenantInfo {
	if c.o.Tenants != nil {
		if m, ok := c.o.Tenants(ctx); ok {
			if t, ok := m[tenant]; ok {
				return t
			}
		}
	}
	return TenantInfo{ID: tenant, Timezone: "UTC", Active: true}
}

func location(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// localDay devuelve el día local de t como fecha (medianoche UTC) y el
// intervalo UTC [inicio, fin) de ese día en loc.
func localDay(t time.Time, loc *time.Location) (day, start, end time.Time) {
	l := t.In(loc)
	day = time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
	start = time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc).UTC()
	end = time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, loc).UTC()
	return day, start, end
}

// dayBounds devuelve el intervalo UTC de una fecha local.
func dayBounds(day time.Time, loc *time.Location) (time.Time, time.Time) {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc).UTC(),
		time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc).UTC()
}

// uplinkRef es un enlace de subida de un ISP (detectado o configurado).
type uplinkRef struct {
	InterfaceID uuid.UUID
	RouterID    uuid.UUID
	RouterName  string
	SiteID      uuid.UUID
	SiteName    string
	Name        string
	Config      *domain.UplinkConfig
	Auto        bool
	Iface       *IfaceSnapshot
	RouterKnown bool
	// Switch: enlace de subida de un switch (elegido por el operador).
	Switch bool
	// History es la capacidad contratada con vigencia (vacía sin configuración).
	History []domain.CapacityPeriod
}

// capacityAt es la función de capacidad del enlace en el tiempo: la
// contratada vigente en cada instante o la velocidad del puerto (speed, si
// no hay snapshot del sondeador).
func (u *uplinkRef) capacityAt(speed int64) domain.CapacityFunc {
	var contracted, sp *int64
	if u.Config != nil {
		contracted = u.Config.ContractedBps
	}
	if u.Iface != nil && u.Iface.SpeedBps != nil {
		sp = u.Iface.SpeedBps
	} else if speed > 0 {
		sp = &speed
	}
	return domain.CapacityFuncFor(u.History, contracted, sp)
}

func (u *uplinkRef) capacity() int64 {
	var contracted, speed *int64
	if u.Config != nil {
		contracted = u.Config.ContractedBps
	}
	if u.Iface != nil {
		speed = u.Iface.SpeedBps
	}
	return domain.CapacityBps(contracted, speed)
}

func (u *uplinkRef) label() string {
	if u.Config != nil && u.Config.Label != "" {
		return u.Config.Label
	}
	if u.Name != "" {
		return u.Name
	}
	return u.InterfaceID.String()[:8]
}

// routersOf devuelve los routers objetivo del ISP.
func (c *Capacity) routersOf(tenant uuid.UUID, now time.Time) []RouterSnapshot {
	if c.o.Engine == nil {
		return nil
	}
	var out []RouterSnapshot
	for _, r := range c.o.Engine.Snapshot(now) {
		if r.TenantID == tenant {
			out = append(out, r)
		}
	}
	return out
}

// uplinksOf devuelve los enlaces del ISP: el detectado de cada router más
// todas las interfaces configuradas (aunque su router no esté sondeándose).
func (c *Capacity) uplinksOf(tenant uuid.UUID, routers []RouterSnapshot) []*uplinkRef {
	cfgs := c.configsFor(tenant)
	c.mu.RLock()
	hist := c.history[tenant]
	c.mu.RUnlock()
	byIface := map[uuid.UUID]*domain.UplinkConfig{}
	for i := range cfgs {
		byIface[cfgs[i].InterfaceID] = &cfgs[i]
	}
	var out []*uplinkRef
	seen := map[uuid.UUID]bool{}
	for ri := range routers {
		r := &routers[ri]
		for ii := range r.Interfaces {
			x := &r.Interfaces[ii]
			cfg := byIface[x.InterfaceID]
			if !x.AutoUplink && cfg == nil {
				continue
			}
			seen[x.InterfaceID] = true
			out = append(out, &uplinkRef{InterfaceID: x.InterfaceID, RouterID: r.RouterID, RouterName: r.Name, SiteID: r.SiteID,
				SiteName: r.SiteName, Name: x.Name, Config: cfg, Auto: x.AutoUplink, Iface: x, RouterKnown: true, Switch: r.Switch})
		}
	}
	for i := range cfgs {
		cfg := &cfgs[i]
		if seen[cfg.InterfaceID] {
			continue
		}
		u := &uplinkRef{InterfaceID: cfg.InterfaceID, RouterID: cfg.RouterID, Config: cfg}
		for ri := range routers {
			if routers[ri].RouterID == cfg.RouterID {
				u.RouterName, u.SiteID, u.SiteName, u.RouterKnown = routers[ri].Name, routers[ri].SiteID, routers[ri].SiteName, true
				u.Switch = routers[ri].Switch
			}
		}
		out = append(out, u)
	}
	for _, u := range out {
		u.History = hist[u.InterfaceID]
	}
	slices.SortFunc(out, func(a, b *uplinkRef) int {
		if c := strings.Compare(a.SiteName, b.SiteName); c != 0 {
			return c
		}
		if c := strings.Compare(a.RouterName, b.RouterName); c != 0 {
			return c
		}
		return strings.Compare(a.label(), b.label())
	})
	return out
}

// TenantServices devuelve los servicios de infraestructura del ISP con su
// estado (para la salud de los nodos; el llamador ya autorizó la petición).
func (i *Infra) TenantServices(ctx context.Context, tenant uuid.UUID) ([]domain.Service, error) {
	if !i.Available() {
		return nil, nil
	}
	return i.store.List(ctx, pgdb.TenantID(tenant))
}

func liveUtil(u *uplinkRef) *float64 {
	capBps := u.capacity()
	if u.Iface == nil || capBps <= 0 || (u.Iface.RxBps == nil && u.Iface.TxBps == nil) {
		return nil
	}
	var m float64
	if u.Iface.RxBps != nil {
		m = *u.Iface.RxBps
	}
	if u.Iface.TxBps != nil {
		m = math.Max(m, *u.Iface.TxBps)
	}
	v := m / float64(capBps)
	return &v
}

// subjectLabel es el sujeto de la recomendación.
func subjectLabel(kind, label string) string {
	if kind == domain.SubjectInternationalEgress {
		return "de salida internacional"
	}
	return "del enlace «" + label + "»"
}

func apiSiteHealth(h domain.SiteHealth) []api.HealthReason {
	out := make([]api.HealthReason, 0, len(h.Reasons))
	for _, r := range h.Reasons {
		out = append(out, api.HealthReason{Status: r.Status, Code: r.Code, Text: r.Text})
	}
	return out
}
