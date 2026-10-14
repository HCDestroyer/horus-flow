package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/snmp/internal/domain"
)

// FlowReader lee flows.flows_raw por tenant para los chequeos pasivos de
// los servicios de infraestructura (usuario horus_snmp con
// horus_tenant_reader). Toda consulta lleva `tenant_id = ?` y fija
// SQL_horus_tenant (política p_tenant, fail-closed): dos barreras.
//
// El objetivo aparece en client_ip (servidor sin prefijo o en un prefijo de
// clientes) o en remote_ip (un cliente habla con el servidor). Entrante =
// download si está en client_ip, upload si está en remote_ip. Los flujos
// entre un servidor de un prefijo `infrastructure` e Internet no guardan la
// IP del servidor (el ingester solo guarda el extremo remoto): no se ven.
type FlowReader struct {
	conn    driver.Conn
	timeout time.Duration
}

// ErrNoTenantFilter es un error de programación: SQL sin tenant_id.
var ErrNoTenantFilter = errors.New("snmp: query without tenant_id filter")

// OpenFlowReader abre la conexión de lectura (user vacío = el del DSN).
func OpenFlowReader(dsn, user, password string, timeout time.Duration) (*FlowReader, error) {
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
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &FlowReader{conn: conn, timeout: timeout}, nil
}

// Close cierra la conexión.
func (r *FlowReader) Close() error { return r.conn.Close() }

// trafficSQL suma bytes y paquetes por objetivo y minuto.
const trafficSQL = `SELECT ip, toStartOfMinute(ts) AS m, sum(bytes) AS b, sumIf(bytes, inbound) AS ib, sumIf(packets, inbound) AS ipk
FROM (
  SELECT ts, bytes, packets, client_ip AS ip, direction = 'download' AS inbound FROM flows.flows_raw
  WHERE tenant_id = ? AND ts >= ? AND ts < ? AND has(arrayMap(x -> toIPv6(x), ?), client_ip)
  UNION ALL
  SELECT ts, bytes, packets, remote_ip AS ip, direction = 'upload' AS inbound FROM flows.flows_raw
  WHERE tenant_id = ? AND ts >= ? AND ts < ? AND has(arrayMap(x -> toIPv6(x), ?), remote_ip)
)
GROUP BY ip, m`

// TargetTraffic implementa app.FlowReader.
func (r *FlowReader) TargetTraffic(ctx context.Context, tenant uuid.UUID, targets []netip.Addr, from, to time.Time) (map[netip.Addr][]domain.MinuteTraffic, error) {
	out := map[netip.Addr][]domain.MinuteTraffic{}
	if len(targets) == 0 {
		return out, nil
	}
	if tenant == uuid.Nil || !strings.Contains(trafficSQL, "tenant_id = ?") {
		return nil, ErrNoTenantFilter
	}
	// flows_raw guarda IPv6 (IPv4 como ::ffff:a.b.c.d).
	ips := make([]string, 0, len(targets))
	back := map[netip.Addr]netip.Addr{}
	for _, t := range targets {
		v6 := netip.AddrFrom16(t.As16())
		ips = append(ips, v6.String())
		back[v6] = t
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	ctx = ch.Context(ctx, ch.WithSettings(ch.Settings{
		"SQL_horus_tenant":   ch.CustomSetting{Value: tenant.String()},
		"max_execution_time": int(r.timeout.Seconds()),
	}))
	f, t := from.UTC(), to.UTC()
	rows, err := r.conn.Query(ctx, trafficSQL, tenant, f, t, ips, tenant, f, t, ips)
	if err != nil {
		return nil, fmt.Errorf("snmp: flows query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ip netip.Addr
		var m time.Time
		var b, ib, ipk uint64
		if err := rows.Scan(&ip, &m, &b, &ib, &ipk); err != nil {
			return nil, fmt.Errorf("snmp: flows scan: %w", err)
		}
		key, ok := back[netip.AddrFrom16(ip.As16())]
		if !ok {
			key = ip.Unmap()
		}
		out[key] = append(out[key], domain.MinuteTraffic{Minute: m.UTC(), Bytes: b, InBytes: ib, InPackets: ipk})
	}
	return out, rows.Err()
}
