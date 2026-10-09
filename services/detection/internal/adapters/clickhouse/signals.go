package clickhouse

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/services/detection/internal/engine"
)

var _ engine.Signals = (*Reader)(nil)

// Expresiones comunes. Saliente = direction 'upload'; el tráfico internal
// (cliente → cliente del nodo) no entra en los detectores de I1 (ver
// engine). SYN sin ACK con el OR acumulado de flags, como las MV.
const (
	withExpr = `WITH protocol = 6 AND bitAnd(tcp_flags, 2) = 2 AND bitAnd(tcp_flags, 16) = 0 AS syn,
		greatest(toUInt64(merged_flows), 1) AS n,
		tupleElement(IPv6CIDRToRange(remote_ip, if(remote_ip BETWEEN toIPv6('::ffff:0.0.0.0') AND toIPv6('::ffff:255.255.255.255'), 120, 48)), 1) AS net `
	upload    = ` tenant_id = ? AND ts >= ? AND ts < ? AND attribution_status = 'attributed' AND direction = 'upload' `
	initiated = ` AND (client_port >= 1024 OR remote_port < 1024) `
	byKeys    = ` AND has(?, toString(realm_id)) AND has(?, toString(client_ip)) `
)

// v16 formatea una IP como la imprime ClickHouse para IPv6 (IPv4 mapeada).
func v16(a netip.Addr) string { return netip.AddrFrom16(a.As16()).String() }

func keyArgs(keys []engine.ClientKey) ([]string, []string) {
	realms, ips := make([]string, 0, len(keys)), make([]string, 0, len(keys))
	seenR, seenI := map[uuid.UUID]bool{}, map[netip.Addr]bool{}
	for _, k := range keys {
		if !seenR[k.Realm] {
			seenR[k.Realm] = true
			realms = append(realms, k.Realm.String())
		}
		if !seenI[k.IP] {
			seenI[k.IP] = true
			ips = append(ips, v16(k.IP))
		}
	}
	return realms, ips
}

func ipList(as []netip.Addr) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = v16(a)
	}
	return out
}

func ports(ps []uint16) []uint16 {
	if len(ps) == 0 {
		return []uint16{0}
	}
	return ps
}

func key(realm uuid.UUID, ip netip.Addr) engine.ClientKey {
	return engine.ClientKey{Realm: realm, IP: ip.Unmap()}
}

func utc(t time.Time) time.Time { return t.UTC() }

func unmapAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, a.Unmap().String())
		}
	}
	return out
}

// Security implementa engine.Signals.
func (r *Reader) Security(ctx context.Context, tenant uuid.UUID, from, to time.Time, h engine.Having) ([]engine.SecurityRow, error) {
	var conds []string
	if h.MinRemoteIPs > 0 {
		conds = append(conds, fmt.Sprintf("rips >= %d", h.MinRemoteIPs))
	}
	if h.MinUpPackets > 0 {
		conds = append(conds, fmt.Sprintf("upp >= %d", h.MinUpPackets))
	}
	if h.MinUpBytes > 0 {
		conds = append(conds, fmt.Sprintf("upb >= %d", h.MinUpBytes))
	}
	if h.MinSMTPRemoteIPs > 0 {
		conds = append(conds, fmt.Sprintf("smtp_ips >= %d", h.MinSMTPRemoteIPs))
	}
	having := ""
	if len(conds) > 0 {
		having = " HAVING " + strings.Join(conds, " OR ")
	}
	var out []engine.SecurityRow
	err := r.query(ctx, tenant, `SELECT realm_id, client_ip, any(site_id), min(bucket), max(bucket),
			sum(up_bytes) AS upb, sum(down_bytes), sum(up_packets) AS upp, sum(flows_out), sum(syn_only_out), sum(small_flows_out),
			uniqMerge(remote_ips_out) AS rips, uniqMerge(remote_nets24_out), uniqMerge(remote_ports_out), uniqMerge(inbound_service_ports),
			sum(smtp_flows_out), uniqMerge(smtp_remote_ips) AS smtp_ips, min(min_sampling_rate), max(max_sampling_rate)
		FROM flows.client_security_1m WHERE tenant_id = ? AND bucket >= ? AND bucket < ?
		GROUP BY realm_id, client_ip`+having,
		func(rows driver.Rows) error {
			var s engine.SecurityRow
			var realm uuid.UUID
			var ip netip.Addr
			if err := rows.Scan(&realm, &ip, &s.Site, &s.First, &s.Last, &s.UpBytes, &s.DownBytes, &s.UpPackets, &s.FlowsOut,
				&s.SynOnlyOut, &s.SmallFlowsOut, &s.RemoteIPs, &s.Nets24, &s.RemotePorts, &s.InboundPorts, &s.SMTPFlows, &s.SMTPRemoteIPs,
				&s.MinSampling, &s.MaxSampling); err != nil {
				return err
			}
			s.Key, s.First, s.Last = key(realm, ip), utc(s.First), utc(s.Last).Add(time.Minute)
			out = append(out, s)
			return nil
		}, tenant, from.UTC(), to.UTC())
	return out, err
}

// Initiated implementa engine.Signals.
func (r *Reader) Initiated(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []engine.ClientKey) (map[engine.ClientKey]engine.InitiatedStats, error) {
	out := map[engine.ClientKey]engine.InitiatedStats{}
	if len(keys) == 0 {
		return out, nil
	}
	realms, ips := keyArgs(keys)
	err := r.query(ctx, tenant, withExpr+`SELECT realm_id, client_ip, sumIf(n, protocol = 6), sumIf(n, syn),
			uniqExactIf(remote_ip, syn), uniqExactIf(net, syn), uniqExact(remote_ip), uniqExact(net), uniqExact(remote_asn),
			if(countIf(syn) > 0, groupUniqArrayIf(10)(toString(remote_ip), syn), groupUniqArray(10)(toString(remote_ip))),
			min(flow_start), max(ts), min(sampling_rate), max(sampling_rate)
		FROM flows.flows_raw WHERE`+upload+initiated+byKeys+`GROUP BY realm_id, client_ip`,
		func(rows driver.Rows) error {
			var s engine.InitiatedStats
			var realm uuid.UUID
			var ip netip.Addr
			if err := rows.Scan(&realm, &ip, &s.TCP, &s.SynOnly, &s.SynDests, &s.SynNets24, &s.Dests, &s.Nets24, &s.ASNs, &s.Sample,
				&s.First, &s.Last, &s.MinSampling, &s.MaxSampling); err != nil {
				return err
			}
			s.Key, s.Sample, s.First, s.Last = key(realm, ip), unmapAll(s.Sample), utc(s.First), utc(s.Last)
			out[s.Key] = s
			return nil
		}, tenant, from.UTC(), to.UTC(), realms, ips)
	return out, err
}

// PortsByClient implementa engine.Signals.
func (r *Reader) PortsByClient(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []engine.ClientKey, synOnly bool, limit int) (map[engine.ClientKey][]engine.PortStat, error) {
	out := map[engine.ClientKey][]engine.PortStat{}
	if len(keys) == 0 {
		return out, nil
	}
	realms, ips := keyArgs(keys)
	cond := ""
	if synOnly {
		cond = " AND syn "
	}
	err := r.query(ctx, tenant, withExpr+`SELECT realm_id, client_ip, remote_port, protocol, uniqExact(remote_ip) AS d, sum(n), sumIf(n, syn)
		FROM flows.flows_raw WHERE`+upload+initiated+byKeys+cond+`
		GROUP BY realm_id, client_ip, remote_port, protocol
		ORDER BY realm_id, client_ip, d DESC, remote_port LIMIT ? BY realm_id, client_ip`,
		func(rows driver.Rows) error {
			var p engine.PortStat
			var realm uuid.UUID
			var ip netip.Addr
			if err := rows.Scan(&realm, &ip, &p.Port, &p.Protocol, &p.Destinations, &p.Flows, &p.SynOnly); err != nil {
				return err
			}
			k := key(realm, ip)
			out[k] = append(out[k], p)
			return nil
		}, tenant, from.UTC(), to.UTC(), realms, ips, limit)
	return out, err
}

// Vertical implementa engine.Signals: destinos con ≥ minPorts puertos TCP
// distintos de un mismo cliente (prefiltro por remote_ports_out del agregado).
func (r *Reader) Vertical(ctx context.Context, tenant uuid.UUID, from, to time.Time, minPorts int) ([]engine.VerticalRow, error) {
	var out []engine.VerticalRow
	err := r.query(ctx, tenant, withExpr+`SELECT realm_id, client_ip, remote_ip, any(remote_asn), uniqExact(remote_port) AS ports, sum(n),
			sumIf(n, packets <= 3), sumIf(n, syn), min(remote_port), max(remote_port), min(flow_start), max(ts)
		FROM flows.flows_raw WHERE`+upload+` AND protocol = 6
			AND (realm_id, client_ip) IN (SELECT realm_id, client_ip FROM flows.client_security_1m
				WHERE tenant_id = ? AND bucket >= ? AND bucket < ? GROUP BY realm_id, client_ip HAVING uniqMerge(remote_ports_out) >= ?)
		GROUP BY realm_id, client_ip, remote_ip HAVING ports >= ? ORDER BY ports DESC LIMIT 1 BY realm_id, client_ip`,
		func(rows driver.Rows) error {
			var v engine.VerticalRow
			var realm uuid.UUID
			var ip netip.Addr
			if err := rows.Scan(&realm, &ip, &v.Remote, &v.ASN, &v.Ports, &v.Flows, &v.Small, &v.SynOnly, &v.MinPort, &v.MaxPort, &v.First, &v.Last); err != nil {
				return err
			}
			v.Key, v.Remote, v.First, v.Last = key(realm, ip), v.Remote.Unmap(), utc(v.First), utc(v.Last)
			out = append(out, v)
			return nil
		}, tenant, from.UTC(), to.UTC(), tenant, from.UTC(), to.UTC(), minPorts, minPorts)
	return out, err
}

// WatchPorts implementa engine.Signals (flows.client_port_1m + dim.watch_port).
func (r *Reader) WatchPorts(ctx context.Context, tenant uuid.UUID, from, to time.Time, minDest int, exclude []uint16) ([]engine.WatchRow, error) {
	const where = ` FROM flows.client_port_1m WHERE tenant_id = ? AND bucket >= ? AND bucket < ? AND direction = 'upload' AND remote_port != 0
		AND (protocol, remote_port) IN (SELECT protocol, port FROM dim.watch_port FINAL) AND NOT has(?, remote_port) `
	rowsBy := map[engine.ClientKey]*engine.WatchRow{}
	var order []engine.ClientKey
	err := r.query(ctx, tenant, `SELECT realm_id, client_ip, uniqMerge(remote_ips) AS d, sum(flows), sum(syn_only)`+where+
		`GROUP BY realm_id, client_ip HAVING d >= ?`,
		func(rows driver.Rows) error {
			var w engine.WatchRow
			var realm uuid.UUID
			var ip netip.Addr
			if err := rows.Scan(&realm, &ip, &w.Destinations, &w.Flows, &w.SynOnly); err != nil {
				return err
			}
			w.Key = key(realm, ip)
			rowsBy[w.Key] = &w
			order = append(order, w.Key)
			return nil
		}, tenant, from.UTC(), to.UTC(), ports(exclude), minDest)
	if err != nil || len(order) == 0 {
		return nil, err
	}
	err = r.query(ctx, tenant, `SELECT realm_id, client_ip, remote_port, protocol, uniqMerge(remote_ips) AS d, sum(flows), sum(syn_only)`+where+
		`GROUP BY realm_id, client_ip, remote_port, protocol ORDER BY d DESC, remote_port`,
		func(rows driver.Rows) error {
			var p engine.PortStat
			var realm uuid.UUID
			var ip netip.Addr
			if err := rows.Scan(&realm, &ip, &p.Port, &p.Protocol, &p.Destinations, &p.Flows, &p.SynOnly); err != nil {
				return err
			}
			if w := rowsBy[key(realm, ip)]; w != nil {
				w.Ports = append(w.Ports, p)
			}
			return nil
		}, tenant, from.UTC(), to.UTC(), ports(exclude))
	out := make([]engine.WatchRow, 0, len(order))
	for _, k := range order {
		if len(rowsBy[k].Ports) > 0 {
			out = append(out, *rowsBy[k])
		}
	}
	return out, err
}

// DestinationsPerMinute implementa engine.Signals.
func (r *Reader) DestinationsPerMinute(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []engine.ClientKey) (map[engine.ClientKey][]uint64, error) {
	out := map[engine.ClientKey][]uint64{}
	if len(keys) == 0 {
		return out, nil
	}
	realms, ips := keyArgs(keys)
	err := r.query(ctx, tenant, `SELECT realm_id, client_ip, bucket, uniqMerge(remote_ips_out)
		FROM flows.client_security_1m WHERE tenant_id = ? AND bucket >= ? AND bucket < ?`+byKeys+`
		GROUP BY realm_id, client_ip, bucket ORDER BY realm_id, client_ip, bucket`,
		func(rows driver.Rows) error {
			var realm uuid.UUID
			var ip netip.Addr
			var b time.Time
			var n uint64
			if err := rows.Scan(&realm, &ip, &b, &n); err != nil {
				return err
			}
			k := key(realm, ip)
			out[k] = append(out[k], n)
			return nil
		}, tenant, from.UTC(), to.UTC(), realms, ips)
	return out, err
}

// ClientFlows implementa engine.Signals.
func (r *Reader) ClientFlows(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []engine.ClientKey, minBytes uint64) ([]engine.FlowRec, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	realms, ips := keyArgs(keys)
	var out []engine.FlowRec
	err := r.query(ctx, tenant, `SELECT realm_id, client_ip, direction = 'upload', remote_ip, remote_port, protocol, remote_asn,
			flow_start, ts, bytes, packets, sampling_rate
		FROM flows.flows_raw WHERE tenant_id = ? AND ts >= ? AND ts < ? AND attribution_status = 'attributed'
			AND direction IN ('upload', 'download') AND bytes >= ?`+byKeys+` LIMIT 500000`,
		func(rows driver.Rows) error {
			var f engine.FlowRec
			var realm uuid.UUID
			var ip netip.Addr
			if err := rows.Scan(&realm, &ip, &f.Up, &f.Remote, &f.RemotePort, &f.Protocol, &f.ASN, &f.Start, &f.End, &f.Bytes, &f.Packets, &f.Sampling); err != nil {
				return err
			}
			f.Key, f.Remote, f.Start, f.End = key(realm, ip), f.Remote.Unmap(), utc(f.Start), utc(f.End)
			out = append(out, f)
			return nil
		}, tenant, from.UTC(), to.UTC(), minBytes, realms, ips)
	return out, err
}

// ReputationPairs implementa engine.Signals (marca en ingesta).
func (r *Reader) ReputationPairs(ctx context.Context, tenant uuid.UUID, from, to time.Time, categories []string) ([]engine.RepPair, error) {
	var out []engine.RepPair
	err := r.query(ctx, tenant, `SELECT realm_id, client_ip, remote_ip, toString(any(reputation_category)), any(reputation_source_id),
			max(reputation_confidence), max(reputation_version), sum(flows), min(first_ts), max(last_ts) AS last
		FROM flows.reputation_hit WHERE tenant_id = ? AND bucket_1h >= toStartOfHour(?) AND bucket_1h < ?
			AND has(?, toString(reputation_category))
		GROUP BY realm_id, client_ip, remote_ip HAVING last >= ?`,
		func(rows driver.Rows) error {
			var p engine.RepPair
			var realm uuid.UUID
			var ip netip.Addr
			if err := rows.Scan(&realm, &ip, &p.Remote, &p.Category, &p.SourceID, &p.Confidence, &p.Version, &p.Conns, &p.First, &p.Last); err != nil {
				return err
			}
			p.Key, p.Remote, p.First, p.Last = key(realm, ip), p.Remote.Unmap(), utc(p.First), utc(p.Last)
			out = append(out, p)
			return nil
		}, tenant, from.UTC(), to.UTC(), categories, from.UTC())
	return out, err
}

// IndicatorFlows implementa engine.Signals: verifica en flows_raw el tráfico
// de cada cliente hacia las IPs dadas (conexiones, respuesta, puerto).
func (r *Reader) IndicatorFlows(ctx context.Context, tenant uuid.UUID, from, to time.Time, remotes []netip.Addr) ([]engine.RepPair, error) {
	if len(remotes) == 0 {
		return nil, nil
	}
	var out []engine.RepPair
	err := r.query(ctx, tenant, withExpr+`SELECT realm_id, client_ip, remote_ip, any(site_id), any(router_id),
			argMaxIf(remote_port, packets, direction = 'upload'), anyIf(protocol, direction = 'upload'), max(remote_asn),
			countIf(direction = 'upload' AND (protocol != 6 OR bitAnd(tcp_flags, 2) = 2)),
			countIf(direction = 'upload' AND syn),
			countIf((direction = 'download' AND packets > 0 AND (protocol != 6 OR bitAnd(tcp_flags, 16) = 16))
				OR (direction = 'upload' AND protocol = 6 AND bitAnd(tcp_flags, 16) = 16)),
			sum(bytes), sum(packets), min(flow_start), max(ts), max(sampling_rate)
		FROM flows.flows_raw WHERE tenant_id = ? AND ts >= ? AND ts < ? AND attribution_status = 'attributed'
			AND direction IN ('upload', 'download') AND has(?, toString(remote_ip))
		GROUP BY realm_id, client_ip, remote_ip`,
		func(rows driver.Rows) error {
			var p engine.RepPair
			var realm uuid.UUID
			var ip netip.Addr
			if err := rows.Scan(&realm, &ip, &p.Remote, &p.Site, &p.Router, &p.RemotePort, &p.Protocol, &p.ASN, &p.Conns, &p.SynOnly,
				&p.Responded, &p.Bytes, &p.Packets, &p.First, &p.Last, &p.Sampling); err != nil {
				return err
			}
			p.Key, p.Remote, p.First, p.Last = key(realm, ip), p.Remote.Unmap(), utc(p.First), utc(p.Last)
			out = append(out, p)
			return nil
		}, tenant, from.UTC(), to.UTC(), ipList(remotes))
	return out, err
}

// BeaconSeries implementa engine.Signals.
func (r *Reader) BeaconSeries(ctx context.Context, tenant uuid.UUID, from, to time.Time, minConns int, minSpan time.Duration, maxMeanBytes float64, exclude []uint16) ([]engine.BeaconRow, error) {
	var out []engine.BeaconRow
	err := r.query(ctx, tenant, `SELECT realm_id, client_ip, remote_ip, remote_port, protocol, max(remote_asn), any(site_id), any(router_id),
			groupUniqArray(5000)(flow_start), avg(bytes) AS mb, max(sampling_rate)
		FROM flows.flows_raw WHERE`+upload+` AND (protocol != 6 OR bitAnd(tcp_flags, 2) = 2) AND NOT has(?, remote_port)
		GROUP BY realm_id, client_ip, remote_ip, remote_port, protocol
		HAVING uniqExact(flow_start) >= ? AND dateDiff('second', min(flow_start), max(flow_start)) >= ? AND mb < ?`,
		func(rows driver.Rows) error {
			var b engine.BeaconRow
			var realm uuid.UUID
			var ip netip.Addr
			if err := rows.Scan(&realm, &ip, &b.Remote, &b.RemotePort, &b.Protocol, &b.ASN, &b.Site, &b.Router, &b.Starts, &b.MeanBytes, &b.Sampling); err != nil {
				return err
			}
			b.Key, b.Remote = key(realm, ip), b.Remote.Unmap()
			for i := range b.Starts {
				b.Starts[i] = b.Starts[i].UTC()
			}
			out = append(out, b)
			return nil
		}, tenant, from.UTC(), to.UTC(), ports(exclude), minConns, int64(minSpan.Seconds()), maxMeanBytes)
	return out, err
}

// Locate implementa engine.Signals.
func (r *Reader) Locate(ctx context.Context, tenant uuid.UUID, from, to time.Time, keys []engine.ClientKey) (map[engine.ClientKey]engine.Location, error) {
	out := map[engine.ClientKey]engine.Location{}
	if len(keys) == 0 {
		return out, nil
	}
	realms, ips := keyArgs(keys)
	err := r.query(ctx, tenant, `SELECT realm_id, client_ip, any(site_id), any(router_id)
		FROM flows.flows_raw WHERE tenant_id = ? AND ts >= ? AND ts < ?`+byKeys+` GROUP BY realm_id, client_ip`,
		func(rows driver.Rows) error {
			var realm uuid.UUID
			var ip netip.Addr
			var l engine.Location
			if err := rows.Scan(&realm, &ip, &l.Site, &l.Router); err != nil {
				return err
			}
			out[key(realm, ip)] = l
			return nil
		}, tenant, from.UTC(), to.UTC(), realms, ips)
	return out, err
}

// Customers implementa engine.Signals (dim.customer, proyectado por devices).
func (r *Reader) Customers(ctx context.Context, tenant uuid.UUID, keys []engine.ClientKey) (map[engine.ClientKey]engine.Customer, error) {
	out := map[engine.ClientKey]engine.Customer{}
	if len(keys) == 0 {
		return out, nil
	}
	realms, ips := keyArgs(keys)
	err := r.query(ctx, tenant, `SELECT realm_id, address, customer_id, kind, alias, site_id
		FROM dim.customer FINAL WHERE tenant_id = ? AND deleted = 0 AND has(?, toString(realm_id)) AND has(?, toString(address))`,
		func(rows driver.Rows) error {
			var realm uuid.UUID
			var ip netip.Addr
			var c engine.Customer
			if err := rows.Scan(&realm, &ip, &c.ID, &c.Kind, &c.Alias, &c.Site); err != nil {
				return err
			}
			c.Known = true
			out[key(realm, ip)] = c
			return nil
		}, tenant, realms, ips)
	return out, err
}

// Timezone implementa engine.Signals.
func (r *Reader) Timezone(ctx context.Context, tenant uuid.UUID) (string, error) {
	var tz string
	err := r.query(ctx, tenant, `SELECT timezone FROM dim.tenant FINAL WHERE tenant_id = ? AND deleted = 0 LIMIT 1`,
		func(rows driver.Rows) error { return rows.Scan(&tz) }, tenant)
	return tz, err
}
