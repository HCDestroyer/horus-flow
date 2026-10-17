package clickhouse

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/domain"
	"github.com/hcdestroyer/horus-flow/services/detection/internal/engine"
)

var _ app.ScoringSource = (*Reader)(nil)

// ScoringFeatures implementa app.ScoringSource: por cliente, la subida y la
// bajada de la ventana, el tráfico en horario laboral local (tz del ISP),
// las horas con al menos ActiveHourBytes, los puertos de servicio con
// conexiones entrantes y los ASN remotos distintos (flows.client_security_1h,
// 13 meses). La subconsulta agrupa por hora porque el AggregatingMergeTree
// puede tener varias filas sin fusionar de la misma hora.
func (r *Reader) ScoringFeatures(ctx context.Context, tenant uuid.UUID, from, to time.Time, tz string, p domain.ScoringParams) ([]app.ScoringRow, error) {
	if _, err := time.LoadLocation(tz); err != nil || tz == "" {
		tz = "UTC"
	}
	days := make([]uint8, 0, len(p.BusinessDays))
	for _, d := range p.BusinessDays {
		if d >= 1 && d <= 7 {
			days = append(days, uint8(d)) //nolint:gosec // 1..7
		}
	}
	if len(days) == 0 {
		days = []uint8{0}
	}
	var out []app.ScoringRow
	err := r.query(ctx, tenant, `SELECT realm_id, client_ip, any(site), sum(upb), sum(downb), sumIf(upb + downb, biz),
			countIf(upb + downb >= ?), uniqMerge(isp), uniqMerge(asns)
		FROM (
			SELECT realm_id, client_ip, bucket, any(site_id) AS site, sum(up_bytes) AS upb, sum(down_bytes) AS downb,
				uniqMergeState(inbound_service_ports) AS isp, uniqMergeState(remote_asns_out) AS asns,
				toTimeZone(bucket, ?) AS lt, has(?, toDayOfWeek(lt)) AND toHour(lt) >= ? AND toHour(lt) < ? AS biz
			FROM flows.client_security_1h WHERE tenant_id = ? AND bucket >= ? AND bucket < ?
			GROUP BY realm_id, client_ip, bucket
		)
		GROUP BY realm_id, client_ip`,
		func(rows driver.Rows) error {
			var s app.ScoringRow
			var realm uuid.UUID
			var ip netip.Addr
			var active uint64
			if err := rows.Scan(&realm, &ip, &s.Site, &s.UpBytes, &s.DownBytes, &s.BusinessBytes, &active, &s.InboundPorts, &s.RemoteASNs); err != nil {
				return err
			}
			s.Key, s.ActiveHours = key(realm, ip), int(active) //nolint:gosec // ≤ horas de la ventana
			out = append(out, s)
			return nil
		}, p.ActiveHourBytes, tz, days, p.BusinessFromHour, p.BusinessToHour, tenant, from.UTC(), to.UTC())
	return out, err
}

// InboundPeers implementa app.ScoringSource: IPs remotas distintas que
// conectan con un puerto de servicio del cliente (misma heurística que
// inbound_service_ports: SYN+ACK de subida desde un puerto menor que el
// remoto efímero), sobre flows_raw (7 días) y solo para los candidatos.
func (r *Reader) InboundPeers(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []engine.ClientKey) (map[engine.ClientKey]uint64, error) {
	out := map[engine.ClientKey]uint64{}
	if len(keys) == 0 {
		return out, nil
	}
	realms, ips := keyArgs(keys)
	err := r.query(ctx, tenant, `SELECT realm_id, client_ip, uniq(remote_ip)
		FROM flows.flows_raw WHERE tenant_id = ? AND ts >= ? AND ts < ? AND attribution_status IN ('attributed', 'internal')
			AND direction = 'upload' AND protocol = 6 AND bitAnd(tcp_flags, 18) = 18 AND remote_port >= 1024 AND client_port < remote_port`+
		byKeys+` GROUP BY realm_id, client_ip`,
		func(rows driver.Rows) error {
			var realm uuid.UUID
			var ip netip.Addr
			var n uint64
			if err := rows.Scan(&realm, &ip, &n); err != nil {
				return err
			}
			out[key(realm, ip)] = n
			return nil
		}, tenant, from.UTC(), to.UTC(), realms, ips)
	return out, err
}

// ScoringCustomers implementa app.ScoringSource: clientes activos de
// dim.customer (proyección de devices) con su tipo y origen.
func (r *Reader) ScoringCustomers(ctx context.Context, tenant uuid.UUID) (map[engine.ClientKey]app.ScoringCustomer, error) {
	out := map[engine.ClientKey]app.ScoringCustomer{}
	err := r.query(ctx, tenant, `SELECT realm_id, address, customer_id, kind, kind_source, site_id
		FROM dim.customer FINAL WHERE tenant_id = ? AND deleted = 0 AND status = 'active'`,
		func(rows driver.Rows) error {
			var realm uuid.UUID
			var ip netip.Addr
			var c app.ScoringCustomer
			if err := rows.Scan(&realm, &ip, &c.ID, &c.Kind, &c.KindSource, &c.Site); err != nil {
				return err
			}
			out[key(realm, ip)] = c
			return nil
		}, tenant)
	return out, err
}

// InsertScores implementa app.ScoringSource: una fila por cliente y día en
// detection.customer_scores_1d (ReplacingMergeTree(computed_at): repetir el
// día sustituye la fila). Escritor único: el rol detection.
func (r *Reader) InsertScores(ctx context.Context, tenant uuid.UUID, rows []app.ScoreRecord) error {
	if len(rows) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{"SQL_horus_tenant": ch.CustomSetting{Value: tenant.String()}}))
	b, err := r.conn.PrepareBatch(ctx, `INSERT INTO detection.customer_scores_1d (tenant_id, realm_id, client_ip, day, model_version,
		customer_id, site_id, commercial_score, raw_score, confidence, kind_on_day, suggested_kind, level, dynamic_pool, reasons, computed_at)`)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	for _, s := range rows {
		var dyn uint8
		if s.DynamicPool {
			dyn = 1
		}
		if err := b.Append(tenant, s.Key.Realm, v16(s.Key.IP), s.Day.UTC(), s.Model, s.CustomerID, s.Site,
			uint8(s.CommercialScore), uint8(s.RawScore), float32(s.Confidence), s.KindOnDay, s.SuggestedKind, //nolint:gosec // 0–100
			s.Level, dyn, s.Reasons, s.ComputedAt.UTC()); err != nil {
			_ = b.Abort()
			return fmt.Errorf("detection: score batch: %w", err)
		}
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

// ScoreSeries implementa app.ScoringSource: la última puntuación de cada día
// del cliente desde from.
func (r *Reader) ScoreSeries(ctx context.Context, tenant, realm uuid.UUID, ip netip.Addr, from time.Time) ([]app.ScorePoint, error) {
	var out []app.ScorePoint
	err := r.query(ctx, tenant, `SELECT day, argMax(commercial_score, computed_at), argMax(confidence, computed_at),
			argMax(level, computed_at), argMax(suggested_kind, computed_at), argMax(kind_on_day, computed_at)
		FROM detection.customer_scores_1d WHERE tenant_id = ? AND realm_id = ? AND client_ip = toIPv6(?) AND day >= toDate(?)
		GROUP BY day ORDER BY day`,
		func(rows driver.Rows) error {
			var pt app.ScorePoint
			var score uint8
			var conf float32
			if err := rows.Scan(&pt.Day, &score, &conf, &pt.Level, &pt.SuggestedKind, &pt.KindOnDay); err != nil {
				return err
			}
			pt.CommercialScore, pt.Confidence = int(score), float64(conf)
			out = append(out, pt)
			return nil
		}, tenant, realm, v16(ip), from.UTC())
	return out, err
}
