package clickhouse

import (
	"context"
	"net/netip"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/app"
)

var _ app.Flows = (*Reader)(nil)

// Evidence implementa app.Flows: flujos del cliente en la ventana del
// hallazgo (sin payloads; cliente IPv6 = su prefijo delegado, D22).
func (r *Reader) Evidence(ctx context.Context, tenant, realm uuid.UUID, client netip.Prefix, from, to time.Time, offset, limit int) ([]app.EvidenceRow, error) {
	lo, hi := client.Masked().Addr(), lastAddr(client)
	var out []app.EvidenceRow
	err := r.query(ctx, tenant, `SELECT ts, remote_ip, remote_port, remote_asn, protocol, tcp_flags, toString(direction), bytes, packets,
			toString(reputation_category)
		FROM flows.flows_raw WHERE tenant_id = ? AND realm_id = ? AND client_ip BETWEEN toIPv6(?) AND toIPv6(?) AND ts >= ? AND ts <= ?
			AND attribution_status IN ('attributed', 'internal')
		ORDER BY ts, remote_ip, remote_port LIMIT ? OFFSET ?`,
		func(rows driver.Rows) error {
			var e app.EvidenceRow
			if err := rows.Scan(&e.TS, &e.RemoteIP, &e.RemotePort, &e.RemoteASN, &e.Protocol, &e.TCPFlags, &e.Direction, &e.Bytes, &e.Packets, &e.Reputation); err != nil {
				return err
			}
			e.TS = e.TS.UTC()
			out = append(out, e)
			return nil
		}, tenant, realm, v16(lo), v16(hi), from.UTC(), to.UTC(), limit, offset)
	return out, err
}

func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As16()
	bits := p.Bits()
	if p.Addr().Is4() {
		bits += 96
	}
	for i := bits; i < 128; i++ {
		a[i/8] |= 1 << (7 - uint(i%8)) //nolint:gosec // i < 128
	}
	return netip.AddrFrom16(a)
}

// WatchedPorts implementa app.Flows: widget watched_ports (puertos
// vigilados con tráfico saliente: clientes y flujos; sin IPs).
func (r *Reader) WatchedPorts(ctx context.Context, tenant uuid.UUID, sites []uuid.UUID, from, to time.Time) ([]app.WatchedPort, error) {
	siteCond := ""
	args := []any{tenant, from.UTC(), to.UTC()}
	if sites != nil {
		ss := make([]string, len(sites))
		for i, s := range sites {
			ss[i] = s.String()
		}
		// client_port_1m no lleva site_id: se resuelve por client_security_1m.
		siteCond = ` AND (realm_id, client_ip) IN (SELECT realm_id, client_ip FROM flows.client_security_1m
			WHERE tenant_id = ? AND bucket >= ? AND bucket < ? AND has(?, toString(site_id)))`
		args = append(args, tenant, from.UTC(), to.UTC(), ss)
	}
	var out []app.WatchedPort
	err := r.query(ctx, tenant, `SELECT remote_port, protocol, uniqExact((realm_id, client_ip)), sum(flows) AS f
		FROM flows.client_port_1m WHERE tenant_id = ? AND bucket >= ? AND bucket < ? AND direction = 'upload' AND remote_port != 0
			AND (protocol, remote_port) IN (SELECT protocol, port FROM dim.watch_port FINAL)`+siteCond+`
		GROUP BY remote_port, protocol ORDER BY f DESC LIMIT 20`,
		func(rows driver.Rows) error {
			var w app.WatchedPort
			if err := rows.Scan(&w.Port, &w.Protocol, &w.Customers, &w.Flows); err != nil {
				return err
			}
			out = append(out, w)
			return nil
		}, args...)
	return out, err
}
