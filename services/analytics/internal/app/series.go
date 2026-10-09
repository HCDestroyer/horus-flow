package app

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/traffic/api/catalog"
)

// Point es un punto de una serie; Value nil = hueco (ausencia ≠ cero).
type Point struct {
	T     time.Time
	Value *float64
}

// Serie es una métrica en el tiempo.
type Serie struct {
	Metric string
	Unit   string
	Group  *string
	Points []Point
}

// SeriesResult incluye la meta de cobertura.
type SeriesResult struct {
	Series   []Serie
	Step     time.Duration
	Range    Range
	Partial  bool
	Coverage float64
}

// Métricas de la serie de tráfico.
var seriesUnits = map[string]string{"down_bps": "bps", "up_bps": "bps", "flows_per_second": "1/s", "active_customers": "customers"}

// Timeseries devuelve la serie ↓/↑ del ISP o de unos nodos con agregados de
// 5 min / 1 h / 1 d según el rango; los buckets sin datos son huecos (null)
// y marcan partial (I1-08 criterios 2 y 3).
func (s *Service) Timeseries(ctx context.Context, sc Scope, metrics []string, r Range, step time.Duration) (*SeriesResult, error) {
	if len(metrics) == 0 {
		metrics = []string{"down_bps", "up_bps"}
	}
	for _, m := range metrics {
		if _, ok := seriesUnits[m]; !ok {
			return nil, fmt.Errorf("%w: metric %q", ErrBadRequest, m)
		}
	}
	g := Pick(r, step)
	stepSec := int64(g.Step.Seconds())
	args := []any{stepSec, sc.Tenant, r.From, r.To}
	sql := fmt.Sprintf(`
		SELECT toStartOfInterval(bucket, toIntervalSecond(?)) AS t,
		       sumIf(bytes, direction = 'download') AS d, sumIf(bytes, direction = 'upload') AS u,
		       sum(flows) AS f, uniqMerge(clients) AS c
		FROM flows.site_%s WHERE tenant_id = ? AND bucket >= ? AND bucket < ?%s
		GROUP BY t ORDER BY t`, g.Suffix, siteFilter(sc.Sites, &args))
	rows, cancel, err := s.Q.Query(ctx, sc.Tenant, sql, args...)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	type vals struct{ d, u, f, c float64 }
	got := map[int64]vals{}
	for rows.Next() {
		var t time.Time
		var d, u, f, c uint64
		if err := rows.Scan(&t, &d, &u, &f, &c); err != nil {
			return nil, err
		}
		got[t.Unix()] = vals{float64(d), float64(u), float64(f), float64(c)}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res := &SeriesResult{Step: g.Step, Range: r}
	start := r.From.Truncate(g.Step)
	now := s.now()
	var total, present int
	out := make([]Serie, len(metrics))
	for i, m := range metrics {
		out[i] = Serie{Metric: m, Unit: seriesUnits[m]}
	}
	sec := g.Step.Seconds()
	for t := start; t.Before(r.To); t = t.Add(g.Step) {
		// El bucket en curso aún no está completo: no cuenta como hueco.
		if t.Add(g.Step).After(now) {
			break
		}
		total++
		v, ok := got[t.Unix()]
		if ok {
			present++
		}
		for i, m := range metrics {
			var p *float64
			if ok {
				var x float64
				switch m {
				case "down_bps":
					x = v.d * 8 / sec
				case "up_bps":
					x = v.u * 8 / sec
				case "flows_per_second":
					x = v.f / sec
				case "active_customers":
					x = v.c
				}
				p = &x
			}
			out[i].Points = append(out[i].Points, Point{T: t, Value: p})
		}
	}
	res.Series = out
	if total > 0 {
		res.Coverage = float64(present) / float64(total)
	}
	res.Partial = present < total
	return res, nil
}

// Attribution devuelve las proporciones de bytes atribuidos, de
// infraestructura y fuera de prefijos (flows_raw, ≤ retención cruda).
func (s *Service) Attribution(ctx context.Context, sc Scope, r Range) (attributed, infra, unattributed float64, err error) {
	args := []any{sc.Tenant, r.From, r.To}
	sql := `SELECT sumIf(bytes, attribution_status IN ('attributed', 'internal')), sumIf(bytes, attribution_status = 'infrastructure'),
		sumIf(bytes, attribution_status IN ('unknown', 'transit')), sum(bytes)
		FROM flows.flows_raw WHERE tenant_id = ? AND ts >= ? AND ts < ?` + siteFilter(sc.Sites, &args)
	rows, cancel, err := s.Q.Query(ctx, sc.Tenant, sql, args...)
	if err != nil {
		return 0, 0, 0, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	var a, i, u, t uint64
	if rows.Next() {
		if err := rows.Scan(&a, &i, &u, &t); err != nil {
			return 0, 0, 0, err
		}
	}
	if t == 0 {
		return 0, 0, 0, rows.Err()
	}
	return float64(a) / float64(t), float64(i) / float64(t), float64(u) / float64(t), rows.Err()
}

// Customer resuelve un customer_id con dim.customer (proyección de devices).
func (s *Service) Customer(ctx context.Context, tenant, id uuid.UUID) (*CustomerKey, error) {
	rows, cancel, err := s.Q.Query(ctx, tenant, `SELECT realm_id, address, site_id, alias, kind, reset_at
		FROM dim.customer FINAL WHERE tenant_id = ? AND customer_id = ? AND deleted = 0 LIMIT 1`, tenant, id)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	c := &CustomerKey{ID: id}
	if err := rows.Scan(&c.RealmID, &c.Address, &c.SiteID, &c.Alias, &c.Kind, &c.ResetAt); err != nil {
		return nil, err
	}
	c.Address = c.Address.Unmap()
	return c, nil
}

// CustomerTraffic devuelve las series ↓/↑ de un cliente (desde reset_at),
// opcionalmente agrupadas por servicio, categoría o ASN.
func (s *Service) CustomerTraffic(ctx context.Context, tenant uuid.UUID, c *CustomerKey, groupBy string, r Range, step time.Duration) (*SeriesResult, error) {
	if c.ResetAt != nil && r.From.Before(*c.ResetAt) {
		r.From = *c.ResetAt
		if !r.From.Before(r.To) {
			return &SeriesResult{Range: r, Step: Pick(r, step).Step, Coverage: 0}, nil
		}
	}
	g := Pick(r, step)
	tbl := "flows.customer_" + g.Suffix
	col := "''"
	switch groupBy {
	case "", "none":
	case "service", "category":
		col = "toString(service_id)"
	case "asn":
		col = "toString(remote_asn)"
		if g.Suffix == "5m" {
			tbl = "flows.customer_1h"
			if g.Step < time.Hour {
				g.Step = time.Hour
			}
		}
	default:
		return nil, fmt.Errorf("%w: group_by %q", ErrBadRequest, groupBy)
	}
	rows, cancel, err := s.Q.Query(ctx, tenant, fmt.Sprintf(`
		SELECT toStartOfInterval(bucket, toIntervalSecond(?)) AS t, %s AS g,
		       sumIf(bytes, direction = 'download') AS d, sumIf(bytes, direction IN ('upload', 'internal')) AS u
		FROM %s WHERE tenant_id = ? AND realm_id = ? AND client_ip = ? AND bucket >= ? AND bucket < ?
		GROUP BY t, g ORDER BY t`, col, tbl),
		int64(g.Step.Seconds()), tenant, c.RealmID, netip.AddrFrom16(c.Address.As16()), r.From, r.To)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	type key struct {
		t int64
		g string
	}
	vals := map[key][2]float64{}
	groups := map[string]bool{}
	cat := s.catalog()
	for rows.Next() {
		var t time.Time
		var grp string
		var d, u uint64
		if err := rows.Scan(&t, &grp, &d, &u); err != nil {
			return nil, err
		}
		if groupBy == "category" {
			grp = categoryOf(cat, grp)
		}
		k := key{t.Unix(), grp}
		v := vals[k]
		v[0] += float64(d)
		v[1] += float64(u)
		vals[k] = v
		groups[grp] = true
	}
	if len(groups) == 0 {
		groups[""] = true
	}
	res := &SeriesResult{Step: g.Step, Range: r}
	sec := g.Step.Seconds()
	now := s.now()
	for grp := range groups {
		var label *string
		if groupBy != "" && groupBy != "none" {
			l := s.label(map[string]string{"service": DimServices, "category": DimCategories, "asn": DimASNs}[groupBy], grp)
			if grp == "" || strings.HasPrefix(grp, "000000") || grp == "0" {
				l = "Sin clasificar"
			}
			label = &l
		}
		down, up := Serie{Metric: "down_bps", Unit: "bps", Group: label}, Serie{Metric: "up_bps", Unit: "bps", Group: label}
		for t := r.From.Truncate(g.Step); t.Before(r.To) && !t.Add(g.Step).After(now); t = t.Add(g.Step) {
			v, ok := vals[key{t.Unix(), grp}]
			var dp, upp *float64
			if ok {
				x, y := v[0]*8/sec, v[1]*8/sec
				dp, upp = &x, &y
			} else if groupBy != "" && groupBy != "none" {
				z := 0.0 // en un desglose, un grupo sin tráfico en un bucket es 0
				dp, upp = &z, &z
			}
			down.Points = append(down.Points, Point{T: t, Value: dp})
			up.Points = append(up.Points, Point{T: t, Value: upp})
		}
		res.Series = append(res.Series, down, up)
	}
	res.Coverage = 1
	return res, rows.Err()
}

func categoryOf(c *catalog.Catalog, serviceID string) string {
	for _, sv := range c.Def.Services {
		if catalog.ID("service", sv.Slug).String() == serviceID {
			return catalog.ID("category", sv.Category).String()
		}
	}
	return ""
}
