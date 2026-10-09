package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/hcdestroyer/horus-flow/packages/go/flowinv"
	tw "github.com/hcdestroyer/horus-flow/services/analytics/api/trafficwidgets"
	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
	collectorapi "github.com/hcdestroyer/horus-flow/services/collector/api"
)

// Tipos de widget que resuelve FLOW (packages/schemas/dashboard/v0/widget-types.json).
var widgetTypes = []string{"traffic_now", "customers_active", "exporters_status", "traffic_timeseries",
	"top_customers", "top_services", "top_categories", "top_organizations"}

var widgetKind = map[string]string{
	"traffic_now": "state", "customers_active": "state", "exporters_status": "table", "traffic_timeseries": "series",
	"top_customers": "table", "top_services": "table", "top_categories": "table", "top_organizations": "table",
}

// widgetConfig es la unión de los config_schema de los tipos de FLOW.
type widgetConfig struct {
	SiteIDs   []uuid.UUID `json:"site_ids"`
	Range     string      `json:"range"`
	N         int         `json:"n"`
	Direction string      `json:"direction"`
	Metrics   []string    `json:"metrics"`
	Compare   string      `json:"compare"`
	Show      []string    `json:"show"`
}

type cacheEntry struct {
	data    *tw.WidgetData
	expires time.Time
}

// Widgets resuelve los datos de los widgets de tráfico (C9, I1-08) con una
// caché por (tenant, tipo, config, rango, alcance, enmascarado): N kioscos
// con el mismo dashboard en la misma ventana ⇒ una sola consulta (criterio 6).
type Widgets struct {
	svc *Service
	ttl time.Duration
	// States lee el estado de los exportadores (NATS KV); nil = pendientes.
	States collectorapi.States

	sf    singleflight.Group
	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewWidgets crea el resolutor con caché de ttl.
func NewWidgets(svc *Service, ttl time.Duration) *Widgets {
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	return &Widgets{svc: svc, ttl: ttl, cache: map[string]cacheEntry{}}
}

// Types implementa tw.Provider.
func (w *Widgets) Types() []string { return slices.Clone(widgetTypes) }

func sitesKey(s []uuid.UUID) string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.String()
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// Resolve implementa tw.Provider.
func (w *Widgets) Resolve(ctx context.Context, req tw.Request) (*tw.WidgetData, error) {
	kind, ok := widgetKind[req.Type]
	if !ok {
		return nil, tw.ErrUnsupportedType
	}
	var cfg widgetConfig
	if len(req.Config) > 0 {
		if err := json.Unmarshal(req.Config, &cfg); err != nil {
			return nil, fmt.Errorf("%w: %v", tw.ErrInvalidConfig, err)
		}
	}
	sites := cfg.SiteIDs
	if len(req.SiteIDs) > 0 {
		sites = req.SiteIDs
	}
	if req.AllowedSites != nil {
		if len(sites) == 0 {
			sites = req.AllowedSites
		} else {
			sites = slices.DeleteFunc(slices.Clone(sites), func(s uuid.UUID) bool { return !slices.Contains(req.AllowedSites, s) })
			if len(sites) == 0 {
				return nil, fmt.Errorf("%w: no visible site", tw.ErrInvalidConfig)
			}
		}
	}
	rng := req.Range
	if rng == "" {
		rng = cfg.Range
	}
	var from, to string
	if req.From != nil {
		from = req.From.UTC().Format(time.RFC3339)
		rng = ""
	}
	if req.To != nil {
		to = req.To.UTC().Format(time.RFC3339)
	}
	key := strings.Join([]string{req.TenantID.String(), req.Type, string(req.Config), rng, from, to, sitesKey(sites),
		fmt.Sprint(req.ShowPersonalData)}, "|")
	now := w.svc.now()
	w.mu.Lock()
	if e, ok := w.cache[key]; ok && now.Before(e.expires) {
		w.mu.Unlock()
		d := *e.data
		d.Meta.Cache = "hit"
		return &d, nil
	}
	w.mu.Unlock()
	v, err, _ := w.sf.Do(key, func() (any, error) {
		d, err := w.resolve(ctx, req, kind, cfg, sites, rng, from, to)
		if err != nil {
			return nil, err
		}
		w.mu.Lock()
		w.cache[key] = cacheEntry{data: d, expires: w.svc.now().Add(w.ttl)}
		if len(w.cache) > 10000 {
			for k, e := range w.cache {
				if w.svc.now().After(e.expires) {
					delete(w.cache, k)
				}
			}
		}
		w.mu.Unlock()
		return d, nil
	})
	if err != nil {
		if errors.Is(err, chread.ErrUnavailable) || errors.Is(err, ErrNoBackend) {
			return nil, fmt.Errorf("%w: %v", tw.ErrUnavailable, err)
		}
		if errors.Is(err, ErrRange) || errors.Is(err, ErrBadRequest) || errors.Is(err, ErrRangeTooLarge) {
			return nil, fmt.Errorf("%w: %v", tw.ErrInvalidConfig, err)
		}
		return nil, err
	}
	d := *(v.(*tw.WidgetData))
	d.Meta.Cache = "miss"
	return &d, nil
}

func (w *Widgets) resolve(ctx context.Context, req tw.Request, kind string, cfg widgetConfig, sites []uuid.UUID, rng, from, to string) (*tw.WidgetData, error) {
	now := w.svc.now().UTC()
	sc := Scope{Tenant: req.TenantID, Sites: sites}
	out := &tw.WidgetData{Meta: tw.Meta{WidgetType: req.Type, DataEndpointKind: kind, GeneratedAt: now,
		MaskedPersonalData: false}}
	switch req.Type {
	case "traffic_now":
		return out, w.trafficNow(ctx, out, sc, cfg, now)
	case "customers_active":
		return out, w.customersActive(ctx, out, sc, now)
	case "exporters_status":
		return out, w.exporters(ctx, out, sc, cfg, now)
	case "traffic_timeseries":
		r, err := ParseRange(rng, from, to, "24h", now)
		if err != nil {
			return nil, err
		}
		res, err := w.svc.Timeseries(ctx, sc, cfg.Metrics, r, 0)
		if err != nil {
			return nil, err
		}
		out.Data = tw.Data{Kind: "series"}
		for _, s := range res.Series {
			ser := tw.Series{Metric: s.Metric, Unit: s.Unit, Group: s.Group, Points: make([]tw.Point, 0, len(s.Points))}
			for _, p := range s.Points {
				var v any
				if p.Value != nil {
					v = math.Round(*p.Value)
				}
				ser.Points = append(ser.Points, tw.Point{p.T.UTC().Format(time.RFC3339), v})
			}
			out.Data.Series = append(out.Data.Series, ser)
		}
		step := int(res.Step.Seconds())
		cov := res.Coverage
		out.Meta.From, out.Meta.To, out.Meta.Step, out.Meta.Partial, out.Meta.Coverage = &r.From, &r.To, &step, res.Partial, &cov
		return out, nil
	default: // top_*
		r, err := ParseRange(rng, from, to, "24h", now)
		if err != nil {
			return nil, err
		}
		return out, w.top(ctx, out, req, sc, cfg, r)
	}
}

func (w *Widgets) top(ctx context.Context, out *tw.WidgetData, req tw.Request, sc Scope, cfg widgetConfig, r Range) error {
	dim := map[string]string{"top_customers": DimCustomers, "top_services": DimServices,
		"top_categories": DimCategories, "top_organizations": DimOrganizations}[req.Type]
	n := cfg.N
	if n == 0 {
		n = 10
	}
	res, err := w.svc.Top(ctx, TopQuery{Scope: sc, Dimension: dim, Direction: cfg.Direction, N: n, Range: r})
	if err != nil {
		return err
	}
	out.Data = tw.Data{Kind: "table", Rows: []map[string]any{}}
	out.Meta.From, out.Meta.To = &r.From, &r.To
	if dim == DimCustomers {
		out.Data.Columns = []tw.Column{{Key: "customer_ip", Type: "ip", PersonalData: true}, {Key: "alias", Type: "string", PersonalData: true},
			{Key: "kind", Type: "string"}, {Key: "site", Type: "string"}, {Key: "down_bytes", Type: "bytes"}, {Key: "up_bytes", Type: "bytes"}}
		out.Meta.MaskedPersonalData = !req.ShowPersonalData
		inv := w.svc.Inv.Load()
		for _, row := range res.Rows {
			c := row.Customer
			ip := c.Address.String()
			if !req.ShowPersonalData {
				ip = maskIP(ip)
			}
			kind := c.Kind
			if kind != "commercial" {
				kind = "residential"
			}
			out.Data.Rows = append(out.Data.Rows, map[string]any{"customer_ip": ip, "alias": c.Alias, "kind": kind,
				"site": siteName(inv.Data().Exporters, c.SiteID), "down_bytes": row.Down, "up_bytes": row.Up})
		}
	} else {
		out.Data.Columns = []tw.Column{{Key: "label", Type: "string"}, {Key: "down_bytes", Type: "bytes"}, {Key: "up_bytes", Type: "bytes"}}
		for _, row := range res.Rows {
			out.Data.Rows = append(out.Data.Rows, map[string]any{"label": row.Label, "down_bytes": row.Down, "up_bytes": row.Up})
		}
	}
	out.Data.Others = map[string]any{"label": "Otros", "down_bytes": res.OthersDown, "up_bytes": res.OthersUp}
	return nil
}

// maskIP enmascara una IP de cliente (10.20.0.••• / 2001:db8:1000:••••::).
func maskIP(s string) string {
	if i := strings.LastIndexByte(s, '.'); i > 0 && !strings.Contains(s, ":") {
		return s[:i] + ".•••"
	}
	parts := strings.Split(strings.TrimSuffix(s, "::"), ":")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, ":") + ":••••::"
}

func siteName(exps []flowinv.Exporter, site uuid.UUID) string {
	for _, e := range exps {
		if e.SiteID == site && e.SiteName != "" {
			return e.SiteName
		}
	}
	return site.String()
}

func (w *Widgets) trafficNow(ctx context.Context, out *tw.WidgetData, sc Scope, cfg widgetConfig, now time.Time) error {
	end := now.Truncate(time.Minute)
	start := end.Add(-time.Hour)
	args := []any{sc.Tenant, start.Add(-time.Minute), end}
	rows, cancel, err := w.svc.Q.Query(ctx, sc.Tenant, `
		SELECT toStartOfMinute(ts) AS m, sumIf(bytes, direction = 'download'), sumIf(bytes, direction = 'upload'), count(), max(ts)
		FROM flows.flows_raw WHERE tenant_id = ? AND ts >= ? AND ts < ?`+siteFilter(sc.Sites, &args)+` GROUP BY m`, args...)
	if err != nil {
		return err
	}
	type mv struct{ d, u, n float64 }
	got := map[int64]mv{}
	var last time.Time
	for rows.Next() {
		var m, mx time.Time
		var d, u, n uint64
		if err := rows.Scan(&m, &d, &u, &n, &mx); err != nil {
			cancel()
			_ = rows.Close()
			return err
		}
		got[m.Unix()] = mv{float64(d), float64(u), float64(n)}
		if mx.After(last) {
			last = mx
		}
	}
	_ = rows.Close()
	cancel()
	var down, up []any
	for t := start; t.Before(end); t = t.Add(time.Minute) {
		if v, ok := got[t.Unix()]; ok {
			down, up = append(down, math.Round(v.d*8/60)), append(up, math.Round(v.u*8/60))
		} else {
			down, up = append(down, nil), append(up, nil)
		}
	}
	values := map[string]any{"down_bps": down[len(down)-1], "up_bps": up[len(up)-1], "down_bps_yesterday": nil,
		"up_bps_yesterday": nil, "flows_per_second": nil,
		"sparkline": map[string]any{"step_seconds": 60, "down_bps": down, "up_bps": up}}
	if v, ok := got[end.Add(-time.Minute).Unix()]; ok {
		values["flows_per_second"] = math.Round(v.n/60*10) / 10
	}
	if cfg.Compare != "none" {
		y := now.Add(-24 * time.Hour)
		args := []any{sc.Tenant, y.Truncate(5 * time.Minute)}
		yr, ycancel, err := w.svc.Q.Query(ctx, sc.Tenant, `SELECT sumIf(bytes, direction = 'download'), sumIf(bytes, direction = 'upload'), count()
			FROM flows.site_5m WHERE tenant_id = ? AND bucket = ?`+siteFilter(sc.Sites, &args), args...)
		if err == nil {
			var d, u, n uint64
			if yr.Next() && yr.Scan(&d, &u, &n) == nil && n > 0 {
				values["down_bps_yesterday"], values["up_bps_yesterday"] = math.Round(float64(d)*8/300), math.Round(float64(u)*8/300)
			}
			_ = yr.Close()
			ycancel()
		}
	}
	out.Data = tw.Data{Kind: "state", Values: values}
	if !last.IsZero() {
		f := int(now.Sub(last).Seconds())
		out.Meta.FreshnessSeconds = &f
	}
	out.Meta.Partial = values["down_bps"] == nil
	return nil
}

func (w *Widgets) customersActive(ctx context.Context, out *tw.WidgetData, sc Scope, now time.Time) error {
	args := []any{sc.Tenant, now.Add(-15 * time.Minute)}
	rows, cancel, err := w.svc.Q.Query(ctx, sc.Tenant, `SELECT uniqExact(realm_id, client_ip) FROM flows.customer_5m
		WHERE tenant_id = ? AND bucket >= toStartOfFiveMinutes(?)`+siteFilter(sc.Sites, &args), args...)
	if err != nil {
		return err
	}
	var active uint64
	if rows.Next() {
		_ = rows.Scan(&active)
	}
	_ = rows.Close()
	cancel()
	today := now.Truncate(24 * time.Hour)
	args = []any{sc.Tenant, today.Add(-30 * 24 * time.Hour)}
	// customer_1d no lleva site_id: con filtro de nodo se usa customer_1h (13 meses).
	tbl, site := "flows.customer_1d", ""
	if len(sc.Sites) > 0 {
		tbl, site = "flows.customer_1h", siteFilter(sc.Sites, &args)
	}
	rows, cancel, err = w.svc.Q.Query(ctx, sc.Tenant, `SELECT countIf(first >= ?), count() FROM (
		SELECT realm_id, client_ip, min(toDate(bucket)) AS first FROM `+tbl+`
		WHERE tenant_id = ? AND bucket >= ?`+site+` GROUP BY realm_id, client_ip)`, append([]any{today}, args...)...)
	if err != nil {
		return err
	}
	var newToday, total uint64
	if rows.Next() {
		_ = rows.Scan(&newToday, &total)
	}
	_ = rows.Close()
	cancel()
	out.Data = tw.Data{Kind: "state", Values: map[string]any{"active": active, "new_today": newToday, "total": total}}
	return nil
}

func (w *Widgets) exporters(ctx context.Context, out *tw.WidgetData, sc Scope, cfg widgetConfig, now time.Time) error {
	inv := w.svc.Inv.Load()
	list, err := collectorapi.Exporters(ctx, inv, w.States, sc.Tenant, sc.Sites, now)
	if err != nil {
		return fmt.Errorf("%w: exporter state: %v", chread.ErrUnavailable, err)
	}
	out.Data = tw.Data{Kind: "table", Rows: []map[string]any{}, Columns: []tw.Column{
		{Key: "router", Type: "string"}, {Key: "site", Type: "string"}, {Key: "state", Type: "state"},
		{Key: "state_since", Type: "timestamp"}, {Key: "last_flow_at", Type: "timestamp"},
		{Key: "flows_per_second", Type: "number"}, {Key: "loss_ratio", Type: "percent"}}}
	for _, v := range list {
		if len(cfg.Show) > 0 && !slices.Contains(cfg.Show, v.State) {
			continue
		}
		router, site := v.RouterID.String(), v.SiteID.String()
		if e, ok := inv.Router(v.RouterID); ok {
			if e.Name != "" {
				router = e.Name
			}
			if e.SiteName != "" {
				site = e.SiteName
			}
		}
		var last any
		if v.LastFlowAt != nil {
			last = v.LastFlowAt.UTC().Format(time.RFC3339)
		}
		out.Data.Rows = append(out.Data.Rows, map[string]any{"router": router, "site": site, "state": v.State,
			"state_since": v.StateSince.UTC().Format(time.RFC3339), "last_flow_at": last,
			"flows_per_second": v.FlowsPerSecond, "loss_ratio": v.LossRatio5m})
	}
	return nil
}
