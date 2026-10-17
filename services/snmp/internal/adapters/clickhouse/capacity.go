package clickhouse

import (
	"context"
	"fmt"
	"net/url"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// Capacity lee y escribe las series del análisis de capacidad (usuario
// horus_snmp): sondas (snmp.probe_metrics/5m/1h), picos diarios
// (snmp.uplink_daily), intervalos de 5 min de interfaz (snmp.interface_5m) y
// volumen remoto por país (flows.site_1d + dim.asn_dict/organization_dict).
// Toda lectura lleva `tenant_id = ?` y fija SQL_horus_tenant (política
// p_tenant, fail-closed): dos barreras.
type Capacity struct {
	conn    driver.Conn
	timeout time.Duration
}

// OpenCapacity abre la conexión (user vacío = el del DSN).
func OpenCapacity(dsn, user, password string) (*Capacity, error) {
	if user != "" {
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, fmt.Errorf("snmp: clickhouse dsn: %w", err)
		}
		u.User = url.User(user)
		dsn = u.String()
	}
	opts, err := ch.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("snmp: clickhouse dsn: %w", err)
	}
	if password != "" {
		opts.Auth.Password = password
	}
	opts.DialTimeout = 5 * time.Second
	conn, err := ch.Open(opts)
	if err != nil {
		return nil, err
	}
	return &Capacity{conn: conn, timeout: 30 * time.Second}, nil
}

// Close cierra la conexión.
func (c *Capacity) Close() error { return c.conn.Close() }

func (c *Capacity) tenantCtx(ctx context.Context, tenant uuid.UUID) (context.Context, context.CancelFunc, error) {
	if tenant == uuid.Nil {
		return nil, nil, ErrNoTenantFilter
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	return ch.Context(ctx, ch.WithSettings(ch.Settings{
		"SQL_horus_tenant":   ch.CustomSetting{Value: tenant.String()},
		"max_execution_time": int(c.timeout.Seconds()),
	})), cancel, nil
}

func u8p(b *bool) *uint8 {
	if b == nil {
		return nil
	}
	v := b2u(*b)
	return &v
}

func u16p(v *int) *uint16 {
	if v == nil || *v < 0 {
		return nil
	}
	x := uint16(min(*v, 65535)) //nolint:gosec // acotado
	return &x
}

// uuidStrings pasa UUID como texto (el driver no infiere Array(UUID) en los
// parámetros; la consulta los convierte con toUUID).
func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// InsertProbes escribe lecturas de sondas.
func (c *Capacity) InsertProbes(ctx context.Context, samples []domain.ProbeSample) error {
	if len(samples) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, err := c.conn.PrepareBatch(ctx, `INSERT INTO snmp.probe_metrics (tenant_id, site_id, router_id, interface_id, probe_id, source, ref,
		target, label, ts, up, sent, received, loss_pct, rtt_avg_ms, rtt_min_ms, rtt_max_ms, jitter_ms)`)
	if err != nil {
		return fmt.Errorf("prepare probe_metrics: %w", err)
	}
	for _, s := range samples {
		if err := b.Append(s.TenantID, s.SiteID, s.RouterID, s.InterfaceID, s.ProbeID, s.Source, s.Ref, s.Target, s.Label, s.At.UTC(),
			u8p(s.Up), u16p(s.Sent), u16p(s.Received), f32(s.LossPct), f32(s.RTTAvgMs), f32(s.RTTMinMs), f32(s.RTTMaxMs), f32(s.JitterMs)); err != nil {
			_ = b.Abort()
			return fmt.Errorf("append probe_metrics: %w", err)
		}
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("insert probe_metrics: %w", err)
	}
	return nil
}

func f32(v *float64) *float32 {
	if v == nil {
		return nil
	}
	x := float32(*v)
	return &x
}

// DailyRow es una fila de snmp.uplink_daily.
type DailyRow struct {
	SubjectID   uuid.UUID
	SubjectKind string
	RouterID    uuid.UUID
	SiteID      uuid.UUID
	Stats       domain.DailyStats
}

// InsertDaily escribe (o sustituye: ReplacingMergeTree) picos diarios.
func (c *Capacity) InsertDaily(ctx context.Context, tenant uuid.UUID, rows []DailyRow, at time.Time) error {
	if len(rows) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, err := c.conn.PrepareBatch(ctx, `INSERT INTO snmp.uplink_daily (tenant_id, subject_id, subject_kind, router_id, site_id, day, samples,
		rx_p95_bps, tx_p95_bps, rx_max_bps, tx_max_bps, capacity_bps, hours_above_warning, hours_above_critical, errors, drops, packets, computed_at)`)
	if err != nil {
		return fmt.Errorf("prepare uplink_daily: %w", err)
	}
	for _, r := range rows {
		s := r.Stats
		if err := b.Append(tenant, r.SubjectID, r.SubjectKind, r.RouterID, r.SiteID, s.Day, uint32(s.Samples), //nolint:gosec // ≤ 288
			s.RxP95Bps, s.TxP95Bps, s.RxMaxBps, s.TxMaxBps, uint64(max(s.CapacityBps, 0)), float32(s.HoursAboveWarning), //nolint:gosec // ≥ 0
			float32(s.HoursAboveCritical), s.Errors, s.Drops, s.Packets, at.UTC()); err != nil {
			_ = b.Abort()
			return fmt.Errorf("append uplink_daily: %w", err)
		}
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("insert uplink_daily: %w", err)
	}
	return nil
}

// IfaceMeta es lo que se sabe de una interfaz por sus agregados.
type IfaceMeta struct {
	RouterID uuid.UUID
	SiteID   uuid.UUID
	Name     string
	SpeedBps int64
}

const bucketsSQL = `SELECT interface_id, bucket, any(router_id), anyLast(site_id), anyLast(name), max(speed_bps_max),
  sum(rx_bps_sum) / nullIf(sum(rx_bps_count), 0), sum(tx_bps_sum) / nullIf(sum(tx_bps_count), 0),
  sum(rx_pps_sum) / nullIf(sum(rx_pps_count), 0), sum(tx_pps_sum) / nullIf(sum(tx_pps_count), 0),
  sum(rx_errors) + sum(tx_errors), sum(rx_drops) + sum(tx_drops)
FROM snmp.interface_5m
WHERE tenant_id = ? AND bucket >= ? AND bucket < ? AND has(arrayMap(x -> toUUID(x), ?), interface_id)
GROUP BY interface_id, bucket
ORDER BY interface_id, bucket`

// Buckets devuelve los intervalos de 5 min de las interfaces en [from, to).
func (c *Capacity) Buckets(ctx context.Context, tenant uuid.UUID, ifaces []uuid.UUID, from, to time.Time) (map[uuid.UUID][]domain.Bucket5m, map[uuid.UUID]IfaceMeta, error) {
	out, meta := map[uuid.UUID][]domain.Bucket5m{}, map[uuid.UUID]IfaceMeta{}
	if len(ifaces) == 0 {
		return out, meta, nil
	}
	ctx, cancel, err := c.tenantCtx(ctx, tenant)
	if err != nil {
		return nil, nil, err
	}
	defer cancel()
	rows, err := c.conn.Query(ctx, bucketsSQL, tenant, from.UTC(), to.UTC(), uuidStrings(ifaces))
	if err != nil {
		return nil, nil, fmt.Errorf("snmp: interface buckets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, router, site uuid.UUID
		var at time.Time
		var name string
		var speed uint64
		var rx, tx, rxp, txp *float64
		var errs, drops uint64
		if err := rows.Scan(&id, &at, &router, &site, &name, &speed, &rx, &tx, &rxp, &txp, &errs, &drops); err != nil {
			return nil, nil, fmt.Errorf("snmp: scan buckets: %w", err)
		}
		out[id] = append(out[id], domain.Bucket5m{At: at.UTC(), RxBps: rx, TxBps: tx, RxPps: rxp, TxPps: txp, Errors: errs, Drops: drops})
		m := meta[id]
		m.RouterID, m.SiteID, m.Name = router, site, name
		if int64(speed) > m.SpeedBps { //nolint:gosec // velocidades < 2^63
			m.SpeedBps = int64(speed) //nolint:gosec // idem
		}
		meta[id] = m
	}
	return out, meta, rows.Err()
}

// LastDaily devuelve el último día escrito de cada sujeto del ISP.
func (c *Capacity) LastDaily(ctx context.Context, tenant uuid.UUID) (map[uuid.UUID]time.Time, error) {
	ctx, cancel, err := c.tenantCtx(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer cancel()
	rows, err := c.conn.Query(ctx, `SELECT subject_id, max(day) FROM snmp.uplink_daily WHERE tenant_id = ? GROUP BY subject_id`, tenant)
	if err != nil {
		return nil, fmt.Errorf("snmp: last daily: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[uuid.UUID]time.Time{}
	for rows.Next() {
		var id uuid.UUID
		var d time.Time
		if err := rows.Scan(&id, &d); err != nil {
			return nil, fmt.Errorf("snmp: scan last daily: %w", err)
		}
		out[id] = d.UTC()
	}
	return out, rows.Err()
}

// DailySeries devuelve los picos diarios de los sujetos en [from, to] (días).
func (c *Capacity) DailySeries(ctx context.Context, tenant uuid.UUID, subjects []uuid.UUID, from, to time.Time) (map[uuid.UUID][]domain.DailyStats, error) {
	out := map[uuid.UUID][]domain.DailyStats{}
	if len(subjects) == 0 {
		return out, nil
	}
	ctx, cancel, err := c.tenantCtx(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer cancel()
	rows, err := c.conn.Query(ctx, `SELECT subject_id, day, samples, rx_p95_bps, tx_p95_bps, rx_max_bps, tx_max_bps, capacity_bps,
		hours_above_warning, hours_above_critical, errors, drops, packets
		FROM snmp.uplink_daily FINAL
		WHERE tenant_id = ? AND has(arrayMap(x -> toUUID(x), ?), subject_id) AND day >= toDate(?) AND day <= toDate(?)
		ORDER BY subject_id, day`, tenant, uuidStrings(subjects), from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("snmp: daily series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id uuid.UUID
		var d domain.DailyStats
		var samples uint32
		var capacity uint64
		var hw, hc float32
		if err := rows.Scan(&id, &d.Day, &samples, &d.RxP95Bps, &d.TxP95Bps, &d.RxMaxBps, &d.TxMaxBps, &capacity, &hw, &hc,
			&d.Errors, &d.Drops, &d.Packets); err != nil {
			return nil, fmt.Errorf("snmp: scan daily: %w", err)
		}
		d.Day, d.Samples, d.CapacityBps = d.Day.UTC(), int(samples), int64(capacity) //nolint:gosec // < 2^63
		d.HoursAboveWarning, d.HoursAboveCritical = float64(hw), float64(hc)
		out[id] = append(out[id], d)
	}
	return out, rows.Err()
}

// ProbeAgg resume una sonda en una ventana.
type ProbeAgg struct {
	ProbeID     uuid.UUID
	SiteID      uuid.UUID
	RouterID    uuid.UUID
	InterfaceID uuid.UUID
	Source      string
	Ref         string
	Target      string
	Label       string
	LastAt      time.Time
	Samples     uint64
	UpRatio     *float64
	LossPct     *float64
	LossPctMax  *float64
	RTTAvgMs    *float64
	JitterMs    *float64
}

// ProbeSummaries resume las sondas del ISP desde from.
func (c *Capacity) ProbeSummaries(ctx context.Context, tenant uuid.UUID, from time.Time) ([]ProbeAgg, error) {
	ctx, cancel, err := c.tenantCtx(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer cancel()
	rows, err := c.conn.Query(ctx, `SELECT probe_id, anyLast(site_id), anyLast(router_id), anyLast(interface_id), anyLast(source),
		anyLast(ref), anyLast(target), anyLast(label), max(bucket), sum(samples),
		sum(up_count) / nullIf(sum(up_total), 0),
		sum(loss_pct_sum) / nullIf(sum(loss_pct_count), 0), if(sum(loss_pct_count) > 0, toFloat64(max(loss_pct_max)), NULL),
		sum(rtt_avg_ms_sum) / nullIf(sum(rtt_avg_ms_count), 0), sum(jitter_ms_sum) / nullIf(sum(jitter_ms_count), 0)
		FROM snmp.probe_5m WHERE tenant_id = ? AND bucket >= ?
		GROUP BY probe_id ORDER BY probe_id`, tenant, from.UTC())
	if err != nil {
		return nil, fmt.Errorf("snmp: probe summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ProbeAgg
	for rows.Next() {
		var p ProbeAgg
		if err := rows.Scan(&p.ProbeID, &p.SiteID, &p.RouterID, &p.InterfaceID, &p.Source, &p.Ref, &p.Target, &p.Label, &p.LastAt, &p.Samples,
			&p.UpRatio, &p.LossPct, &p.LossPctMax, &p.RTTAvgMs, &p.JitterMs); err != nil {
			return nil, fmt.Errorf("snmp: scan probe summary: %w", err)
		}
		p.LastAt = p.LastAt.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProbePoint es un punto horario de una sonda.
type ProbePoint struct {
	At       time.Time
	LossPct  *float64
	RTTAvgMs *float64
	JitterMs *float64
}

// ProbeSeries devuelve la serie horaria de las sondas en [from, to).
func (c *Capacity) ProbeSeries(ctx context.Context, tenant uuid.UUID, probes []uuid.UUID, from, to time.Time) (map[uuid.UUID][]ProbePoint, error) {
	out := map[uuid.UUID][]ProbePoint{}
	if len(probes) == 0 {
		return out, nil
	}
	ctx, cancel, err := c.tenantCtx(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer cancel()
	rows, err := c.conn.Query(ctx, `SELECT probe_id, bucket, sum(loss_pct_sum) / nullIf(sum(loss_pct_count), 0),
		sum(rtt_avg_ms_sum) / nullIf(sum(rtt_avg_ms_count), 0), sum(jitter_ms_sum) / nullIf(sum(jitter_ms_count), 0)
		FROM snmp.probe_1h WHERE tenant_id = ? AND has(arrayMap(x -> toUUID(x), ?), probe_id) AND bucket >= ? AND bucket < ?
		GROUP BY probe_id, bucket ORDER BY probe_id, bucket`, tenant, uuidStrings(probes), from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("snmp: probe series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id uuid.UUID
		var p ProbePoint
		if err := rows.Scan(&id, &p.At, &p.LossPct, &p.RTTAvgMs, &p.JitterMs); err != nil {
			return nil, fmt.Errorf("snmp: scan probe series: %w", err)
		}
		p.At = p.At.UTC()
		out[id] = append(out[id], p)
	}
	return out, rows.Err()
}

// trafficSQL agrega el volumen remoto por nodo, día, país de la organización
// del ASN y pertenencia a la lista local (la clasificación final la hace
// domain.ClassifyOrigin).
const trafficByCountrySQL = `SELECT site_id, toDate(bucket) AS d, remote_asn = 0 AS no_asn, has(?, remote_asn) AS local,
  dictGetOrDefault('dim.organization_dict', 'country',
    tuple(dictGetOrDefault('dim.asn_dict', 'org_id', toUInt64(remote_asn), toUUID('00000000-0000-0000-0000-000000000000'))), '') AS country,
  sumIf(bytes, direction = 'download') AS down, sumIf(bytes, direction = 'upload') AS up
FROM flows.site_1d
WHERE tenant_id = ? AND bucket >= ? AND bucket < ? AND direction IN ('download', 'upload') AND (length(?) = 0 OR has(arrayMap(x -> toUUID(x), ?), site_id))
GROUP BY site_id, d, no_asn, local, country`

// TrafficByCountry devuelve el volumen remoto agregado por país en [from, to).
func (c *Capacity) TrafficByCountry(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, localASNs []uint32, from, to time.Time) ([]domain.ASNVolume, error) {
	ctx, cancel, err := c.tenantCtx(ctx, tenant)
	if err != nil {
		return nil, err
	}
	defer cancel()
	ss := uuidStrings(sites)
	if localASNs == nil {
		localASNs = []uint32{}
	}
	rows, err := c.conn.Query(ctx, trafficByCountrySQL, localASNs, tenant, from.UTC(), to.UTC(), ss, ss)
	if err != nil {
		return nil, fmt.Errorf("snmp: traffic by country: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.ASNVolume
	for rows.Next() {
		var v domain.ASNVolume
		if err := rows.Scan(&v.SiteID, &v.Day, &v.NoASN, &v.Local, &v.Country, &v.BytesDown, &v.BytesUp); err != nil {
			return nil, fmt.Errorf("snmp: scan traffic: %w", err)
		}
		v.Day = v.Day.UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}
