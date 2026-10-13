package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	snmpapi "github.com/hcdestroyer/horus-flow/services/snmp/api"
)

// MaxRouterInterfaces es el máximo de interfaces por consulta de series
// (getRouterInterfaceMetrics: 422 si se piden más).
const MaxRouterInterfaces = 8

// Métricas de dispositivo (getRouterMetrics): nombre → unidad, expresión
// sobre el crudo snmp.device_metrics y sobre los agregados snmp.device_5m/1h
// (SimpleAggregateFunction: media = <m>_sum / <m>_count, máximos con max;
// services/snmp/migrations/clickhouse). Toda expresión se envuelve en
// toNullable(toFloat64(…)) para escanear siempre Nullable(Float64); una
// media sin muestras (count 0) es NULL, nunca 0.
type snmpMetric struct {
	unit string
	raw  string // expresión sobre el crudo
	agg  string // expresión sobre los agregados 5 min / 1 h
}

func aggAvg(col string) string { return "sum(" + col + "_sum) / nullIf(sum(" + col + "_count), 0)" }
func aggMax(col string) string {
	return "if(sum(" + col + "_count) > 0, max(" + col + "_max), NULL)"
}

var deviceMetrics = map[string]snmpMetric{
	"cpu_percent":             {"percent", "avg(cpu_percent)", aggAvg("cpu_percent")},
	"cpu_max_core_percent":    {"percent", "max(cpu_max_core_percent)", aggMax("cpu_max_core_percent")},
	"memory_percent":          {"percent", "avg(memory_percent)", aggAvg("memory_percent")},
	"temperature_celsius":     {"celsius", "avg(temperature_celsius)", aggAvg("temperature_celsius")},
	"cpu_temperature_celsius": {"celsius", "avg(cpu_temperature_celsius)", aggAvg("cpu_temperature_celsius")},
	"icmp_rtt_ms":             {"ms", "avg(icmp_rtt_ms)", aggAvg("icmp_rtt_ms")},
	"icmp_loss_ratio":         {"ratio", "avg(icmp_loss_ratio)", "1 - sum(icmp_ok_count) / nullIf(sum(icmp_total), 0)"},
	"availability_ratio":      {"ratio", "sum(icmp_ok) / count()", "sum(icmp_ok_count) / nullIf(sum(icmp_total), 0)"},
	"poll_duration_ms":        {"ms", "avg(poll_duration_ms)", "sum(poll_duration_ms_sum) / nullIf(sum(samples), 0)"},
	"uptime_seconds":          {"seconds", "max(uptime_seconds)", "nullIf(max(uptime_seconds_max), 0)"},
}

// DefaultDeviceMetrics es el `metrics` por defecto de getRouterMetrics.
var DefaultDeviceMetrics = []string{"cpu_percent", "memory_percent", "temperature_celsius"}

var interfaceMetrics = map[string]snmpMetric{
	"rx_bps":         {"bps", "avg(rx_bps)", aggAvg("rx_bps")},
	"tx_bps":         {"bps", "avg(tx_bps)", aggAvg("tx_bps")},
	"rx_pps":         {"pps", "avg(rx_pps)", aggAvg("rx_pps")},
	"tx_pps":         {"pps", "avg(tx_pps)", aggAvg("tx_pps")},
	"rx_errors":      {"count", "sum(rx_errors_delta)", "sum(rx_errors)"},
	"tx_errors":      {"count", "sum(tx_errors_delta)", "sum(tx_errors)"},
	"rx_drops":       {"count", "sum(rx_drops_delta)", "sum(rx_drops)"},
	"tx_drops":       {"count", "sum(tx_drops_delta)", "sum(tx_drops)"},
	"utilization_rx": {"ratio", "max(utilization_rx)", "max(utilization_rx_max)"},
	"utilization_tx": {"ratio", "max(utilization_tx)", "max(utilization_tx_max)"},
}

// DefaultInterfaceMetrics es el `metrics` por defecto de getRouterInterfaceMetrics.
var DefaultInterfaceMetrics = []string{"rx_bps", "tx_bps"}

// SNMPAggregates indica si se leen los agregados snmp.device_5m/1h y
// snmp.interface_5m/1h (migración 20261013120000_snmp_metrics del rol snmp)
// para los rangos de más de 24 h (5 min hasta 2 d, 1 h después); en false
// todos los rangos leen el crudo (30 d).
var SNMPAggregates = true

// snmpGranularity es la tabla y el paso de una consulta SNMP.
type snmpGranularity struct {
	Step   time.Duration
	Suffix string // "" = crudo | 5m | 1h
}

// pickSNMP: crudo hasta 24 h (paso de 1 min en adelante), agregados de 5 min
// hasta 2 d y de 1 h después; ≤ ~500 puntos.
func pickSNMP(r Range, step time.Duration) snmpGranularity {
	span := r.To.Sub(r.From)
	g := snmpGranularity{Step: time.Minute}
	switch {
	case span > 2*24*time.Hour:
		g = snmpGranularity{Step: time.Hour, Suffix: "1h"}
	case span > 24*time.Hour:
		g = snmpGranularity{Step: 5 * time.Minute, Suffix: "5m"}
	}
	if !SNMPAggregates {
		g.Suffix = ""
	}
	if step > g.Step {
		g.Step = step.Truncate(g.Step)
	}
	for span/g.Step > 500 {
		g.Step *= 2
	}
	return g
}

func (g snmpGranularity) table(kind string) string {
	if g.Suffix == "" {
		return "snmp." + kind + "_metrics"
	}
	return "snmp." + kind + "_" + g.Suffix
}

// ts es la columna de tiempo: ts en el crudo, bucket en los agregados.
func (g snmpGranularity) ts() string {
	if g.Suffix == "" {
		return "ts"
	}
	return "bucket"
}

func (g snmpGranularity) expr(m snmpMetric) string {
	if g.Suffix == "" {
		return "toNullable(toFloat64(" + m.raw + "))"
	}
	return "toNullable(toFloat64(" + m.agg + "))"
}

// parseMetrics valida la lista pedida (vacía ⇒ def) contra known.
func parseMetrics(metrics []string, def []string, known map[string]snmpMetric) ([]string, error) {
	if len(metrics) == 0 {
		return def, nil
	}
	out := make([]string, 0, len(metrics))
	for _, m := range metrics {
		m = strings.TrimSpace(m)
		if _, ok := known[m]; !ok {
			return nil, fmt.Errorf("%w: metric %q", ErrBadRequest, m)
		}
		out = append(out, m)
	}
	return out, nil
}

// ParseInterfaceIDs valida `interface_id` (UUID separados por comas, máximo
// MaxRouterInterfaces); vacío ⇒ nil (selección automática).
func ParseInterfaceIDs(csv string) ([]uuid.UUID, error) {
	if strings.TrimSpace(csv) == "" {
		return nil, nil
	}
	var out []uuid.UUID
	for _, part := range strings.Split(csv, ",") {
		id, err := uuid.Parse(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("%w: interface_id %q", ErrBadRequest, part)
		}
		out = append(out, id)
	}
	if len(out) > MaxRouterInterfaces {
		return nil, fmt.Errorf("%w: interface_id: at most %d interfaces", ErrBadRequest, MaxRouterInterfaces)
	}
	return out, nil
}

func (s *Service) routers() snmpapi.RouterStatusReader {
	if s.Routers == nil {
		return nil
	}
	return s.Routers()
}

// Router resuelve el nodo de un router del tenant (ErrNotFound si no es suyo
// o no existe): inventario flowinv, estado del rol snmp o dim.router.
func (s *Service) Router(ctx context.Context, tenant, id uuid.UUID) (uuid.UUID, error) {
	if s.Inv != nil {
		if e, ok := s.Inv.Load().Router(id); ok {
			if e.TenantID == tenant {
				return e.SiteID, nil
			}
			return uuid.Nil, ErrNotFound
		}
	}
	if rd := s.routers(); rd != nil {
		live, ok, err := rd.Live(ctx, tenant, id)
		if err == nil && ok {
			return live.Status.SiteID, nil
		}
	}
	rows, cancel, err := s.Q.Query(ctx, tenant, `SELECT site_id FROM dim.router FINAL
		WHERE tenant_id = ? AND router_id = ? AND deleted = 0 LIMIT 1`, tenant, id)
	if err != nil {
		return uuid.Nil, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return uuid.Nil, err
		}
		return uuid.Nil, ErrNotFound
	}
	var site uuid.UUID
	if err := rows.Scan(&site); err != nil {
		return uuid.Nil, err
	}
	return site, nil
}

// bucketer reparte los buckets completos de un rango.
type bucketer struct {
	start, end, now time.Time
	step            time.Duration
}

func newBucketer(r Range, step time.Duration, now time.Time) bucketer {
	return bucketer{start: r.From.Truncate(step), end: r.To, now: now, step: step}
}

// each llama a f con cada bucket completo (el bucket en curso no cuenta).
func (b bucketer) each(f func(t time.Time)) {
	for t := b.start; t.Before(b.end); t = t.Add(b.step) {
		if t.Add(b.step).After(b.now) {
			break
		}
		f(t)
	}
}

// RouterMetrics devuelve las series de dispositivo de un router
// (getRouterMetrics): huecos como null (nunca 0), partial y coverage como
// en las series de tráfico. El router ya se validó con Router.
func (s *Service) RouterMetrics(ctx context.Context, tenant, routerID uuid.UUID, metrics []string, r Range, step time.Duration) (*SeriesResult, error) {
	metrics, err := parseMetrics(metrics, DefaultDeviceMetrics, deviceMetrics)
	if err != nil {
		return nil, err
	}
	g := pickSNMP(r, step)
	exprs := make([]string, len(metrics))
	for i, m := range metrics {
		exprs[i] = g.expr(deviceMetrics[m])
	}
	rows, cancel, err := s.Q.Query(ctx, tenant, fmt.Sprintf(`
		SELECT toStartOfInterval(%[1]s, toIntervalSecond(?)) AS t, %[2]s
		FROM %[3]s WHERE tenant_id = ? AND router_id = ? AND %[1]s >= ? AND %[1]s < ?
		GROUP BY t ORDER BY t`, g.ts(), strings.Join(exprs, ", "), g.table("device")),
		int64(g.Step.Seconds()), tenant, routerID, r.From, r.To)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	got := map[int64][]*float64{}
	for rows.Next() {
		var t time.Time
		vals := make([]*float64, len(metrics))
		dest := make([]any, 0, len(metrics)+1)
		dest = append(dest, &t)
		for i := range vals {
			dest = append(dest, &vals[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		got[t.Unix()] = vals
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res := &SeriesResult{Step: g.Step, Range: r, Series: make([]Serie, len(metrics))}
	for i, m := range metrics {
		res.Series[i] = Serie{Metric: m, Unit: deviceMetrics[m].unit, Points: []Point{}}
	}
	var total, present int
	newBucketer(r, g.Step, s.now()).each(func(t time.Time) {
		total++
		vals, ok := got[t.Unix()]
		if ok {
			present++
		}
		for i := range metrics {
			var v *float64
			if ok && vals[i] != nil {
				x := *vals[i]
				v = &x
			}
			res.Series[i].Points = append(res.Series[i].Points, Point{T: t, Value: v})
		}
	})
	if total > 0 {
		res.Coverage = float64(present) / float64(total)
	}
	res.Partial = present < total
	return res, nil
}

// RouterInterfaceMetrics devuelve una serie por métrica e interfaz
// (getRouterInterfaceMetrics), con group = nombre de la interfaz. Sin ifaces
// elige las de subida (uplink, estado vivo del rol snmp) y, si no hay, las 4
// con más tráfico medio en el rango.
func (s *Service) RouterInterfaceMetrics(ctx context.Context, tenant, routerID uuid.UUID, ifaces []uuid.UUID, metrics []string, r Range, step time.Duration) (*SeriesResult, error) {
	metrics, err := parseMetrics(metrics, DefaultInterfaceMetrics, interfaceMetrics)
	if err != nil {
		return nil, err
	}
	if len(ifaces) > MaxRouterInterfaces {
		return nil, fmt.Errorf("%w: at most %d interfaces", ErrBadRequest, MaxRouterInterfaces)
	}
	g := pickSNMP(r, step)
	names := map[uuid.UUID]string{}
	if rd := s.routers(); rd != nil {
		if live, ok, err := rd.Live(ctx, tenant, routerID); err == nil && ok {
			for _, it := range live.Interfaces {
				names[it.InterfaceID] = it.Name
				if len(ifaces) == 0 && it.Uplink {
					ifaces = append(ifaces, it.InterfaceID)
				}
			}
		}
	}
	if len(ifaces) > MaxRouterInterfaces {
		ifaces = ifaces[:MaxRouterInterfaces]
	}
	if len(ifaces) == 0 {
		if ifaces, err = s.busiestInterfaces(ctx, tenant, routerID, r, g, names); err != nil {
			return nil, err
		}
	}
	res := &SeriesResult{Step: g.Step, Range: r}
	if len(ifaces) == 0 {
		return res, nil
	}
	exprs := make([]string, len(metrics))
	for i, m := range metrics {
		exprs[i] = g.expr(interfaceMetrics[m])
	}
	rows, cancel, err := s.Q.Query(ctx, tenant, fmt.Sprintf(`
		SELECT toStartOfInterval(%[1]s, toIntervalSecond(?)) AS t, interface_id, anyLast(toString(name)), %[2]s
		FROM %[3]s WHERE tenant_id = ? AND router_id = ? AND interface_id IN ? AND %[1]s >= ? AND %[1]s < ?
		GROUP BY t, interface_id ORDER BY t`, g.ts(), strings.Join(exprs, ", "), g.table("interface")),
		int64(g.Step.Seconds()), tenant, routerID, ifaces, r.From, r.To)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	type key struct {
		t  int64
		id uuid.UUID
	}
	got := map[key][]*float64{}
	for rows.Next() {
		var t time.Time
		var id uuid.UUID
		var name string
		vals := make([]*float64, len(metrics))
		dest := make([]any, 0, len(metrics)+3)
		dest = append(dest, &t, &id, &name)
		for i := range vals {
			dest = append(dest, &vals[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		got[key{t.Unix(), id}] = vals
		if name != "" {
			names[id] = name
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	b := newBucketer(r, g.Step, s.now())
	var total, present int
	for _, id := range ifaces {
		name := names[id]
		if name == "" {
			name = id.String()
		}
		series := make([]Serie, len(metrics))
		for i, m := range metrics {
			label := name
			series[i] = Serie{Metric: m, Unit: interfaceMetrics[m].unit, Group: &label, Points: []Point{}}
		}
		b.each(func(t time.Time) {
			total++
			vals, ok := got[key{t.Unix(), id}]
			if ok {
				present++
			}
			for i := range metrics {
				var v *float64
				if ok && vals[i] != nil {
					x := *vals[i]
					v = &x
				}
				series[i].Points = append(series[i].Points, Point{T: t, Value: v})
			}
		})
		res.Series = append(res.Series, series...)
	}
	if total > 0 {
		res.Coverage = float64(present) / float64(total)
	}
	res.Partial = present < total
	return res, nil
}

// busiestInterfaces: las 4 interfaces con más tráfico medio (rx+tx) en el rango.
func (s *Service) busiestInterfaces(ctx context.Context, tenant, routerID uuid.UUID, r Range, g snmpGranularity, names map[uuid.UUID]string) ([]uuid.UUID, error) {
	rx, tx := g.expr(interfaceMetrics["rx_bps"]), g.expr(interfaceMetrics["tx_bps"])
	rows, cancel, err := s.Q.Query(ctx, tenant, fmt.Sprintf(`
		SELECT interface_id, anyLast(toString(name)), coalesce(%[1]s, 0) + coalesce(%[2]s, 0) AS traffic
		FROM %[3]s WHERE tenant_id = ? AND router_id = ? AND %[4]s >= ? AND %[4]s < ?
		GROUP BY interface_id ORDER BY traffic DESC, interface_id LIMIT 4`, rx, tx, g.table("interface"), g.ts()),
		tenant, routerID, r.From, r.To)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var name string
		var traffic float64
		if err := rows.Scan(&id, &name, &traffic); err != nil {
			return nil, err
		}
		out = append(out, id)
		if name != "" {
			names[id] = name
		}
	}
	return out, rows.Err()
}

// ErrRoutersUnavailable: el rol snmp no está en este proceso (el widget
// responde "no disponible", nunca una lista vacía).
var ErrRoutersUnavailable = errors.New("router status unavailable: snmp role not in this process")
