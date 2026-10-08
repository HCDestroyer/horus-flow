-- I0-13 · Señales de detección (D5, ADR-0024; contrato C3; docs/database.md §6.2.1,
-- docs/traffic-model.md §8). Tablas del contrato + sus MV, que el contrato delega en I0-13.
--
-- Convenciones de las MV de seguridad:
--   * Solo tráfico de clientes: attribution_status IN ('attributed', 'internal').
--   * "Saliente" (_out) = direction 'upload'; "entrante" = 'download'. Un flujo cuenta merged_flows.
--   * SYN sin ACK: protocol = 6 AND tcp_flags & 0x02 AND NOT tcp_flags & 0x10 (OR acumulado de flags).
--   * Puertos vigilados: (protocol, remote_port) en dim.watch_port. Resolvers del ISP:
--     (tenant_id, remote_ip) en dim.tenant_resolver. Se leen con subconsultas IN por bloque (tablas
--     pequeñas) en lugar de dictGet para que un diccionario caído no bloquee la ingesta.

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS flows.client_security_1m
(
    tenant_id UUID, site_id UUID, realm_id UUID, client_ip IPv6, bucket DateTime('UTC'),
    up_bytes SimpleAggregateFunction(sum, UInt64), down_bytes SimpleAggregateFunction(sum, UInt64),
    up_packets SimpleAggregateFunction(sum, UInt64), down_packets SimpleAggregateFunction(sum, UInt64),
    flows_out SimpleAggregateFunction(sum, UInt64), flows_in SimpleAggregateFunction(sum, UInt64),
    remote_ips_out AggregateFunction(uniq, IPv6),
    remote_nets24_out AggregateFunction(uniq, IPv6),
    remote_ports_out AggregateFunction(uniq, UInt16),
    remote_asns_out AggregateFunction(uniq, UInt32),
    syn_only_out SimpleAggregateFunction(sum, UInt64),
    small_flows_out SimpleAggregateFunction(sum, UInt64),
    watch_port_flows SimpleAggregateFunction(sumMap, Map(UInt16, UInt64)),
    inbound_service_ports AggregateFunction(uniq, UInt16),
    dns_flows_isp SimpleAggregateFunction(sum, UInt64), dns_flows_other SimpleAggregateFunction(sum, UInt64),
    dns_resolvers AggregateFunction(uniq, IPv6),
    smtp_flows_out SimpleAggregateFunction(sum, UInt64), smtp_remote_ips AggregateFunction(uniq, IPv6),
    icmp_flows_out SimpleAggregateFunction(sum, UInt64),
    min_sampling_rate SimpleAggregateFunction(min, UInt32), max_sampling_rate SimpleAggregateFunction(max, UInt32)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMMDD(bucket)
ORDER BY (tenant_id, site_id, realm_id, client_ip, bucket)
TTL bucket + INTERVAL 7 DAY DELETE;

-- remote_nets24_out: /24 de IPv4 (IPv4 mapeada, /120) o /48 de IPv6.
-- inbound_service_ports: client_port de flujos TCP de subida con SYN+ACK cuyo client_port es menor
-- que un remote_port efímero (≥ 1024): el cliente responde a una conexión entrante (heurística v0;
-- con flags acumulados no se distingue quién abrió la sesión).
CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_client_security_1m TO flows.client_security_1m
DEFINER = horus_mv SQL SECURITY DEFINER AS
WITH
    direction = 'upload' AS is_up,
    direction = 'download' AS is_down,
    toUInt64(merged_flows) AS n,
    protocol = 6 AND bitAnd(tcp_flags, 2) = 2 AND bitAnd(tcp_flags, 16) = 0 AS is_syn_only,
    protocol = 6 AND bitAnd(tcp_flags, 18) = 18 AS is_syn_ack,
    remote_port IN (53, 853) AS is_dns,
    (tenant_id, remote_ip) IN (SELECT tenant_id, resolver_ip FROM dim.tenant_resolver FINAL WHERE deleted = 0) AS is_isp_resolver,
    (protocol, remote_port) IN (SELECT protocol, port FROM dim.watch_port FINAL) AS is_watch_port,
    remote_ip BETWEEN toIPv6('::ffff:0.0.0.0') AND toIPv6('::ffff:255.255.255.255') AS is_v4
SELECT
    tenant_id, site_id, realm_id, client_ip, toStartOfMinute(ts) AS bucket,
    sumIf(bytes, is_up) AS up_bytes, sumIf(bytes, is_down) AS down_bytes,
    sumIf(packets, is_up) AS up_packets, sumIf(packets, is_down) AS down_packets,
    sumIf(n, is_up) AS flows_out, sumIf(n, is_down) AS flows_in,
    uniqStateIf(remote_ip, is_up) AS remote_ips_out,
    uniqStateIf(tupleElement(IPv6CIDRToRange(remote_ip, if(is_v4, 120, 48)), 1), is_up) AS remote_nets24_out,
    uniqStateIf(remote_port, is_up) AS remote_ports_out,
    uniqStateIf(remote_asn, is_up) AS remote_asns_out,
    sumIf(n, is_up AND is_syn_only) AS syn_only_out,
    sumIf(n, is_up AND packets <= 3) AS small_flows_out,
    sumMapIf(map(remote_port, n), is_up AND is_watch_port) AS watch_port_flows,
    uniqStateIf(client_port, is_up AND is_syn_ack AND remote_port >= 1024 AND client_port < remote_port) AS inbound_service_ports,
    sumIf(n, is_up AND is_dns AND is_isp_resolver) AS dns_flows_isp,
    sumIf(n, is_up AND is_dns AND NOT is_isp_resolver) AS dns_flows_other,
    uniqStateIf(remote_ip, is_up AND is_dns) AS dns_resolvers,
    sumIf(n, is_up AND protocol = 6 AND remote_port = 25) AS smtp_flows_out,
    uniqStateIf(remote_ip, is_up AND protocol = 6 AND remote_port = 25) AS smtp_remote_ips,
    sumIf(n, is_up AND protocol IN (1, 58)) AS icmp_flows_out,
    min(sampling_rate) AS min_sampling_rate, max(sampling_rate) AS max_sampling_rate
FROM flows.flows_raw
WHERE attribution_status IN ('attributed', 'internal')
GROUP BY tenant_id, site_id, realm_id, client_ip, bucket;

-- site_id queda fuera del ORDER BY del contrato: ClickHouse ≥ 25 lo rechaza en AggregatingMergeTree
-- salvo con allow_dimensions_outside_sorting_key (el cliente (realm_id, client_ip) pertenece a un
-- nodo, así que site_id depende funcionalmente de la clave; tras un merge queda uno cualquiera).
CREATE TABLE IF NOT EXISTS flows.client_security_1h AS flows.client_security_1m
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, realm_id, client_ip, bucket)
TTL bucket + INTERVAL 13 MONTH DELETE
SETTINGS allow_dimensions_outside_sorting_key = 1;

-- En cascada desde el minuto: combina estados (uniqMergeState) en vez de repetir la lógica.
-- site_id no está en la clave de 1 h (contrato): se guarda el de la primera fila del grupo.
CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_client_security_1h TO flows.client_security_1h
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT
    tenant_id, any(site_id) AS site_id, realm_id, client_ip, toStartOfHour(m.bucket) AS bucket,
    sum(up_bytes) AS up_bytes, sum(down_bytes) AS down_bytes,
    sum(up_packets) AS up_packets, sum(down_packets) AS down_packets,
    sum(flows_out) AS flows_out, sum(flows_in) AS flows_in,
    uniqMergeState(remote_ips_out) AS remote_ips_out,
    uniqMergeState(remote_nets24_out) AS remote_nets24_out,
    uniqMergeState(remote_ports_out) AS remote_ports_out,
    uniqMergeState(remote_asns_out) AS remote_asns_out,
    sum(syn_only_out) AS syn_only_out, sum(small_flows_out) AS small_flows_out,
    sumMap(watch_port_flows) AS watch_port_flows,
    uniqMergeState(inbound_service_ports) AS inbound_service_ports,
    sum(dns_flows_isp) AS dns_flows_isp, sum(dns_flows_other) AS dns_flows_other,
    uniqMergeState(dns_resolvers) AS dns_resolvers,
    sum(smtp_flows_out) AS smtp_flows_out, uniqMergeState(smtp_remote_ips) AS smtp_remote_ips,
    sum(icmp_flows_out) AS icmp_flows_out,
    min(min_sampling_rate) AS min_sampling_rate, max(max_sampling_rate) AS max_sampling_rate
FROM flows.client_security_1m AS m
GROUP BY tenant_id, realm_id, client_ip, bucket;

CREATE TABLE IF NOT EXISTS flows.client_port_1m
(
    tenant_id UUID, realm_id UUID, client_ip IPv6, bucket DateTime('UTC'),
    direction Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    protocol UInt8, remote_port UInt16,          -- solo puertos vigilados o de amplificación; el resto en 0
    flows SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64),
    bytes SimpleAggregateFunction(sum, UInt64), syn_only SimpleAggregateFunction(sum, UInt64),
    remote_ips AggregateFunction(uniq, IPv6)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMMDD(bucket)
ORDER BY (tenant_id, realm_id, client_ip, bucket, direction, protocol, remote_port)
TTL bucket + INTERVAL 7 DAY DELETE;

-- Amplificación UDP (traffic-model.md §8): 53, 123, 1900, 11211, 19.
CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_client_port_1m TO flows.client_port_1m
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT
    tenant_id, realm_id, client_ip, toStartOfMinute(ts) AS bucket, direction, protocol,
    if((protocol, r.remote_port) IN (SELECT protocol, port FROM dim.watch_port FINAL)
           OR (protocol = 17 AND r.remote_port IN (53, 123, 1900, 11211, 19)),
       r.remote_port, toUInt16(0)) AS remote_port,
    sum(toUInt64(merged_flows)) AS flows, sum(packets) AS packets, sum(bytes) AS bytes,
    sum(toUInt64(merged_flows) * (protocol = 6 AND bitAnd(tcp_flags, 2) = 2 AND bitAnd(tcp_flags, 16) = 0)) AS syn_only,
    uniqState(remote_ip) AS remote_ips
FROM flows.flows_raw AS r
WHERE attribution_status IN ('attributed', 'internal')
GROUP BY tenant_id, realm_id, client_ip, bucket, direction, protocol, remote_port;

CREATE TABLE IF NOT EXISTS flows.reputation_hit
(
    tenant_id UUID, realm_id UUID, client_ip IPv6, remote_ip IPv6, bucket_1h DateTime('UTC'),
    flows SimpleAggregateFunction(sum, UInt64), bytes SimpleAggregateFunction(sum, UInt64),
    packets SimpleAggregateFunction(sum, UInt64), syn_only SimpleAggregateFunction(sum, UInt64),
    first_ts SimpleAggregateFunction(min, DateTime64(3, 'UTC')), last_ts SimpleAggregateFunction(max, DateTime64(3, 'UTC')),
    reputation_category SimpleAggregateFunction(any, Enum8('none' = 0, 'botnet_cc' = 1, 'scanner' = 2, 'malware_dist' = 3, 'mining_pool' = 4, 'proxy_vpn' = 5, 'tor_exit' = 6, 'blocklist' = 7)),
    reputation_source_id SimpleAggregateFunction(any, UInt16),
    reputation_confidence SimpleAggregateFunction(max, UInt8),
    reputation_version SimpleAggregateFunction(max, UInt32)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket_1h)
ORDER BY (tenant_id, realm_id, client_ip, remote_ip, bucket_1h)
TTL bucket_1h + INTERVAL 13 MONTH DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_reputation_hit TO flows.reputation_hit
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, realm_id, client_ip, remote_ip, toStartOfHour(ts) AS bucket_1h,
       sum(toUInt64(merged_flows)) AS flows, sum(bytes) AS bytes, sum(packets) AS packets,
       sum(toUInt64(protocol = 6 AND bitAnd(tcp_flags, 2) = 2 AND bitAnd(tcp_flags, 16) = 0)) AS syn_only,
       min(ts) AS first_ts, max(ts) AS last_ts, any(reputation_category) AS reputation_category,
       any(reputation_source_id) AS reputation_source_id, max(reputation_confidence) AS reputation_confidence,
       max(reputation_version) AS reputation_version
FROM (SELECT * FROM flows.flows_raw WHERE reputation_category != 'none')
GROUP BY tenant_id, realm_id, client_ip, remote_ip, bucket_1h;

-- +goose Down
DROP VIEW IF EXISTS flows.mv_reputation_hit;
DROP TABLE IF EXISTS flows.reputation_hit;
DROP VIEW IF EXISTS flows.mv_client_port_1m;
DROP TABLE IF EXISTS flows.client_port_1m;
DROP VIEW IF EXISTS flows.mv_client_security_1h;
DROP TABLE IF EXISTS flows.client_security_1h;
DROP VIEW IF EXISTS flows.mv_client_security_1m;
DROP TABLE IF EXISTS flows.client_security_1m;
