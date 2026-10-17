package reporting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/analytics/internal/adapters/chread"
)

// Querier ejecuta una consulta ClickHouse por tenant (chread.Reader).
type Querier interface {
	Query(ctx context.Context, tenant uuid.UUID, sql string, args ...any) (chread.Rows, context.CancelFunc, error)
}

// CHMetrics implementa Metrics sobre los agregados de ClickHouse que ya
// leen analytics y la ficha de router: snmp.device_1h (disponibilidad),
// flows.site_1h (volumen, 13 meses; horario para respetar los límites de la
// semana en la zona del ISP) y dim.router / dim.site (nombres).
type CHMetrics struct{ Q Querier }

var _ Metrics = CHMetrics{}

// Availability implementa Metrics.
func (m CHMetrics) Availability(ctx context.Context, tenant uuid.UUID, p Period) (*Availability, error) {
	type agg struct{ icmpOK, icmpTotal, snmpOK, snmpTotal uint64 }
	stats := map[uuid.UUID]agg{}
	err := m.each(ctx, tenant, `SELECT router_id, sum(icmp_ok_count), sum(icmp_total), sum(snmp_ok_count), sum(snmp_total)
		FROM snmp.device_1h WHERE tenant_id = ? AND bucket >= ? AND bucket < ? GROUP BY router_id`,
		[]any{tenant, p.From, p.To}, func(scan func(...any) error) error {
			var id uuid.UUID
			var a agg
			if err := scan(&id, &a.icmpOK, &a.icmpTotal, &a.snmpOK, &a.snmpTotal); err != nil {
				return err
			}
			stats[id] = a
			return nil
		})
	if err != nil {
		return nil, err
	}
	type router struct {
		name string
		site uuid.UUID
	}
	routers := map[uuid.UUID]router{}
	err = m.each(ctx, tenant, `SELECT router_id, name, site_id FROM dim.router FINAL WHERE tenant_id = ? AND deleted = 0`,
		[]any{tenant}, func(scan func(...any) error) error {
			var id, site uuid.UUID
			var name string
			if err := scan(&id, &name, &site); err != nil {
				return err
			}
			routers[id] = router{name, site}
			return nil
		})
	if err != nil {
		return nil, err
	}
	sites := map[uuid.UUID]string{}
	err = m.each(ctx, tenant, `SELECT site_id, name FROM dim.site FINAL WHERE tenant_id = ? AND deleted = 0`,
		[]any{tenant}, func(scan func(...any) error) error {
			var id uuid.UUID
			var name string
			if err := scan(&id, &name); err != nil {
				return err
			}
			sites[id] = name
			return nil
		})
	if err != nil {
		return nil, err
	}
	out := &Availability{Routers: []RouterAvailability{}}
	var okSum, totalSum uint64
	seen := map[uuid.UUID]bool{}
	add := func(id uuid.UUID) {
		if seen[id] {
			return
		}
		seen[id] = true
		r := RouterAvailability{RouterID: id, Name: id.String()}
		if info, ok := routers[id]; ok {
			r.Name = info.name
			r.Site = sites[info.site]
		}
		if a, ok := stats[id]; ok {
			r.Samples = a.icmpTotal
			r.ICMPRatio = ratio(a.icmpOK, a.icmpTotal)
			r.SNMPRatio = ratio(a.snmpOK, a.snmpTotal)
			okSum += a.icmpOK
			totalSum += a.icmpTotal
		}
		out.Routers = append(out.Routers, r)
	}
	for id := range routers {
		add(id)
	}
	for id := range stats {
		add(id)
	}
	out.Overall = ratio(okSum, totalSum)
	SortAvailability(out.Routers)
	return out, nil
}

// Traffic implementa Metrics: volumen de bajada y subida del periodo, del
// periodo anterior de igual duración y por día local.
func (m CHMetrics) Traffic(ctx context.Context, tenant uuid.UUID, p Period) (*Traffic, error) {
	loc := p.Loc
	if loc == nil {
		loc = time.UTC
	}
	out := &Traffic{}
	prev := p.Previous()
	err := m.each(ctx, tenant, `SELECT bucket >= ? AS cur, sumIf(bytes, direction = 'download'), sumIf(bytes, direction = 'upload')
		FROM flows.site_1h WHERE tenant_id = ? AND bucket >= ? AND bucket < ? GROUP BY cur`,
		[]any{p.From, tenant, prev.From, p.To}, func(scan func(...any) error) error {
			var cur bool
			var down, up uint64
			if err := scan(&cur, &down, &up); err != nil {
				return err
			}
			if cur {
				out.DownBytes, out.UpBytes = down, up
			} else {
				out.PrevDownBytes, out.PrevUpBytes = down, up
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	// Por día local: la zona es una constante validada (time.LoadLocation) y
	// va como literal ligado por el driver.
	byDay := map[string]DayVolume{}
	err = m.each(ctx, tenant, `SELECT toString(toDate(bucket, ?)) AS d, sumIf(bytes, direction = 'download'), sumIf(bytes, direction = 'upload')
		FROM flows.site_1h WHERE tenant_id = ? AND bucket >= ? AND bucket < ? GROUP BY d`,
		[]any{loc.String(), tenant, p.From, p.To}, func(scan func(...any) error) error {
			var d string
			var down, up uint64
			if err := scan(&d, &down, &up); err != nil {
				return err
			}
			byDay[d] = DayVolume{DownBytes: down, UpBytes: up, HasSamples: true}
			return nil
		})
	if err != nil {
		return nil, err
	}
	for day := startOfDay(p.From.In(loc)); day.Before(p.To); day = addDays(day, 1) {
		v := byDay[day.Format("2006-01-02")]
		v.Day = day
		out.Daily = append(out.Daily, v)
	}
	return out, nil
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func ratio(ok, total uint64) *float64 {
	if total == 0 {
		return nil
	}
	v := float64(ok) / float64(total)
	return &v
}

func (m CHMetrics) each(ctx context.Context, tenant uuid.UUID, sql string, args []any, row func(scan func(...any) error) error) error {
	rows, cancel, err := m.Q.Query(ctx, tenant, sql, args...)
	if err != nil {
		return fmt.Errorf("reporting: clickhouse: %w", err)
	}
	defer cancel()
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := row(rows.Scan); err != nil {
			return fmt.Errorf("reporting: clickhouse scan: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reporting: clickhouse: %w", err)
	}
	return nil
}
