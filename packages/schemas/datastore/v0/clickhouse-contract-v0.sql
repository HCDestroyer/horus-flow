-- =============================================================================================
-- C3 — Esquema ClickHouse v0 de flujos crudos y agregados (DDL DE REFERENCIA del gate G0).
-- Fuente: docs/database.md §4–§7, docs/traffic-model.md §3–§8, ADR-0015, ADR-0018, ADR-0024, D4, D12.
-- I0-05 lo sitúa en infrastructure/clickhouse/contract-v0.sql; INT no escribe en infrastructure/, así que
-- vive aquí y FLOW lo copia/traduce a migraciones goose (I0-13). Se congela: nombres, tipos, ORDER BY con
-- tenant_id primero, clave natural (tenant_id, realm_id, client_ip) sin customer_id, particiones y TTL.
-- Fuera de v0 (I0-13 los añade): diccionarios dim.*_dict (fuente ClickHouse, credenciales de despliegue),
-- ROW POLICY por SQL_horus_tenant, usuarios por módulo, snmp.* (I2), detection.customer_scores_1d (I2).
-- Verificación: aplicado en un ClickHouse vacío (chdb 3.1 / clickhouse-server 24.8).
-- =============================================================================================
CREATE DATABASE IF NOT EXISTS flows;
CREATE DATABASE IF NOT EXISTS dim;

-- Flujos crudos enriquecidos en ingesta. Escritor único: rol ingester. Pre-NAT (D12): client_ip es la IP
-- privada del cliente en subida y bajada cuando el NAT está en el router principal.
CREATE TABLE flows.flows_raw
(
    tenant_id                 UUID,
    ts                        DateTime64(3, 'UTC') CODEC(DoubleDelta, ZSTD(1)),
    flow_start                DateTime64(3, 'UTC') CODEC(DoubleDelta, ZSTD(1)),
    received_at               DateTime64(3, 'UTC') CODEC(DoubleDelta, ZSTD(1)),
    site_id                   UUID,
    router_id                 UUID,
    input_interface_id        UUID,
    output_interface_id       UUID,
    realm_id                  UUID,
    attribution_status        Enum8('unknown' = 0, 'attributed' = 1, 'infrastructure' = 2, 'transit' = 3, 'internal' = 4),
    direction                 Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    client_ip                 IPv6,
    client_port               UInt16,
    remote_ip                 IPv6,
    remote_port               UInt16,
    protocol                  UInt8,
    tcp_flags                 UInt8,
    icmp_type_code            UInt16,
    bytes                     UInt64 CODEC(T64, ZSTD(1)),
    packets                   UInt64 CODEC(T64, ZSTD(1)),
    duration_ms               UInt32 CODEC(T64, ZSTD(1)),
    sampling_rate             UInt32 CODEC(T64, ZSTD(1)),
    merged_flows              UInt16,
    flow_source               Enum8('netflow_v5' = 1, 'netflow_v9' = 2, 'ipfix' = 3, 'sflow_v5' = 4),
    remote_asn                UInt32,
    remote_prefix             IPv6,
    remote_prefix_len         UInt8,
    remote_org_id             UUID,
    remote_country            LowCardinality(FixedString(2)),
    service_id                UUID,
    classification_method     Enum8('none' = 0, 'local_override' = 1, 'prefix' = 2, 'asn_port' = 3, 'asn' = 4, 'port' = 5, 'sni' = 6, 'dns' = 7, 'heuristic' = 8),
    classification_confidence UInt8,
    catalog_version           UInt32,
    tenant_rules_version      UInt32,
    category_id               UUID,
    reputation_category       Enum8('none' = 0, 'botnet_cc' = 1, 'scanner' = 2, 'malware_dist' = 3, 'mining_pool' = 4, 'proxy_vpn' = 5, 'tor_exit' = 6, 'blocklist' = 7),
    reputation_source_id      UInt16,
    reputation_confidence     UInt8,
    reputation_version        UInt32,
    batch_id                  UUID,
    INDEX ix_remote_ip   remote_ip   TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX ix_remote_port remote_port TYPE set(1024)          GRANULARITY 4,
    INDEX ix_asn         remote_asn  TYPE set(1024)          GRANULARITY 4
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(ts)
ORDER BY (tenant_id, site_id, realm_id, client_ip, ts)
TTL toDateTime(ts) + INTERVAL 7 DAY DELETE
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- ---------------------------------------------------------------- agregados de consumo
CREATE TABLE flows.customer_5m
(
    tenant_id UUID, site_id UUID, realm_id UUID, client_ip IPv6, bucket DateTime('UTC'), service_id UUID,
    direction Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    bytes SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64),
    flows SimpleAggregateFunction(sum, UInt64), remote_ips AggregateFunction(uniq, IPv6)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, realm_id, client_ip, bucket, service_id, direction)
TTL bucket + INTERVAL 90 DAY DELETE;

CREATE MATERIALIZED VIEW flows.mv_customer_5m TO flows.customer_5m AS
SELECT tenant_id, site_id, realm_id, client_ip, toStartOfFiveMinutes(ts) AS bucket, service_id, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(remote_ip) AS remote_ips
FROM flows.flows_raw WHERE attribution_status IN ('attributed', 'internal')
GROUP BY tenant_id, site_id, realm_id, client_ip, bucket, service_id, direction;

CREATE TABLE flows.customer_1h
(
    tenant_id UUID, site_id UUID, realm_id UUID, client_ip IPv6, bucket DateTime('UTC'), service_id UUID, remote_asn UInt32,
    direction Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    bytes SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64),
    flows SimpleAggregateFunction(sum, UInt64), remote_ips AggregateFunction(uniq, IPv6)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, realm_id, client_ip, bucket, service_id, remote_asn, direction)
TTL bucket + INTERVAL 13 MONTH DELETE;

CREATE MATERIALIZED VIEW flows.mv_customer_1h TO flows.customer_1h AS
SELECT tenant_id, site_id, realm_id, client_ip, toStartOfHour(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(remote_ip) AS remote_ips
FROM flows.flows_raw WHERE attribution_status IN ('attributed', 'internal')
GROUP BY tenant_id, site_id, realm_id, client_ip, bucket, service_id, remote_asn, direction;

CREATE TABLE flows.customer_1d
(
    tenant_id UUID, realm_id UUID, client_ip IPv6, bucket Date, service_id UUID, remote_asn UInt32,
    direction Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    bytes SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64),
    flows SimpleAggregateFunction(sum, UInt64), remote_ips AggregateFunction(uniq, IPv6), remote_asns AggregateFunction(uniq, UInt32)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, realm_id, client_ip, bucket, service_id, remote_asn, direction)
TTL bucket + INTERVAL 25 MONTH DELETE;

CREATE MATERIALIZED VIEW flows.mv_customer_1d TO flows.customer_1d AS
SELECT tenant_id, realm_id, client_ip, toDate(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows,
       uniqState(remote_ip) AS remote_ips, uniqState(remote_asn) AS remote_asns
FROM flows.flows_raw WHERE attribution_status IN ('attributed', 'internal')
GROUP BY tenant_id, realm_id, client_ip, bucket, service_id, remote_asn, direction;

-- Consumo por nodo (incluye lo no atribuido: el nodo en modo descubrimiento sigue contando).
CREATE TABLE flows.site_5m
(
    tenant_id UUID, site_id UUID, bucket DateTime('UTC'), service_id UUID, remote_asn UInt32,
    direction Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    bytes SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64),
    flows SimpleAggregateFunction(sum, UInt64), clients AggregateFunction(uniq, UUID, IPv6)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, bucket, service_id, remote_asn, direction)
TTL bucket + INTERVAL 90 DAY DELETE;

CREATE MATERIALIZED VIEW flows.mv_site_5m TO flows.site_5m AS
SELECT tenant_id, site_id, toStartOfFiveMinutes(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(realm_id, client_ip) AS clients
FROM flows.flows_raw
GROUP BY tenant_id, site_id, bucket, service_id, remote_asn, direction;

CREATE TABLE flows.site_1h AS flows.site_5m
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, bucket, service_id, remote_asn, direction)
TTL bucket + INTERVAL 13 MONTH DELETE;

CREATE MATERIALIZED VIEW flows.mv_site_1h TO flows.site_1h AS
SELECT tenant_id, site_id, toStartOfHour(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(realm_id, client_ip) AS clients
FROM flows.flows_raw
GROUP BY tenant_id, site_id, bucket, service_id, remote_asn, direction;

CREATE TABLE flows.site_1d AS flows.site_5m
ENGINE = AggregatingMergeTree PARTITION BY toYear(bucket)
ORDER BY (tenant_id, site_id, bucket, service_id, remote_asn, direction)
TTL bucket + INTERVAL 5 YEAR DELETE;

CREATE MATERIALIZED VIEW flows.mv_site_1d TO flows.site_1d AS
SELECT tenant_id, site_id, toStartOfDay(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(realm_id, client_ip) AS clients
FROM flows.flows_raw
GROUP BY tenant_id, site_id, bucket, service_id, remote_asn, direction;

-- IPs que parecen de clientes fuera de todo prefijo: base del modo descubrimiento (I1-29).
CREATE TABLE flows.unattributed_1h
(
    tenant_id UUID, site_id UUID, bucket DateTime('UTC'), client_ip IPv6,
    bytes SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, bucket, client_ip)
TTL bucket + INTERVAL 30 DAY DELETE;

CREATE MATERIALIZED VIEW flows.mv_unattributed_1h TO flows.unattributed_1h AS
SELECT tenant_id, site_id, toStartOfHour(ts) AS bucket, client_ip, sum(bytes) AS bytes, sum(packets) AS packets
FROM flows.flows_raw WHERE attribution_status = 'unknown'
GROUP BY tenant_id, site_id, bucket, client_ip;

-- ---------------------------------------------------------------- señales de detección (D5, ADR-0024)
CREATE TABLE flows.client_security_1m
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
-- La MV de client_security_1m (con dim.watch_port y dim.tenant_resolver como diccionarios) la escribe FLOW en
-- I0-13; el contrato son las columnas y su semántica (database.md §6.2.1).

CREATE TABLE flows.client_security_1h AS flows.client_security_1m
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, realm_id, client_ip, bucket)
TTL bucket + INTERVAL 13 MONTH DELETE;

CREATE TABLE flows.client_port_1m
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

CREATE TABLE flows.reputation_hit
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

CREATE MATERIALIZED VIEW flows.mv_reputation_hit TO flows.reputation_hit AS
SELECT tenant_id, realm_id, client_ip, remote_ip, toStartOfHour(ts) AS bucket_1h,
       sum(toUInt64(merged_flows)) AS flows, sum(bytes) AS bytes, sum(packets) AS packets,
       sum(toUInt64(protocol = 6 AND bitAnd(tcp_flags, 2) = 2 AND bitAnd(tcp_flags, 16) = 0)) AS syn_only,
       min(ts) AS first_ts, max(ts) AS last_ts, any(reputation_category) AS reputation_category,
       any(reputation_source_id) AS reputation_source_id, max(reputation_confidence) AS reputation_confidence,
       max(reputation_version) AS reputation_version
FROM (SELECT * FROM flows.flows_raw WHERE reputation_category != 'none')
GROUP BY tenant_id, realm_id, client_ip, remote_ip, bucket_1h;

-- Cobertura (bytes de flujos vs uplink por SNMP); la llena jobs (I2 con SNMP).
CREATE TABLE flows.flows_coverage
(
    tenant_id UUID, site_id UUID, router_id UUID, bucket DateTime('UTC'),
    flow_bytes UInt64, snmp_bytes Nullable(UInt64), coverage_ratio Nullable(Float32)
)
ENGINE = ReplacingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, router_id, bucket)
TTL bucket + INTERVAL 13 MONTH DELETE;

-- ---------------------------------------------------------------- dimensiones (escritor único: analytics)
CREATE TABLE dim.tenant (tenant_id UUID, name String, timezone String, version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY tenant_id;
CREATE TABLE dim.site (tenant_id UUID, site_id UUID, name String, parent_id UUID, timezone String, version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY (tenant_id, site_id);
CREATE TABLE dim.router (tenant_id UUID, router_id UUID, site_id UUID, name String, is_primary UInt8, version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY (tenant_id, router_id);
CREATE TABLE dim.interface (tenant_id UUID, interface_id UUID, router_id UUID, name String, speed_bps UInt64,
    flow_role LowCardinality(String), version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY (tenant_id, interface_id);
CREATE TABLE dim.customer (tenant_id UUID, realm_id UUID, address IPv6, customer_id UUID, site_id UUID,
    kind LowCardinality(String), kind_source LowCardinality(String), alias Nullable(String), status LowCardinality(String),
    reset_at Nullable(DateTime('UTC')), version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY (tenant_id, realm_id, address);
CREATE TABLE dim.service (service_id UUID, name String, category_id UUID, catalog_version UInt32)
ENGINE = ReplacingMergeTree(catalog_version) ORDER BY service_id;
CREATE TABLE dim.category (category_id UUID, name String, catalog_version UInt32)
ENGINE = ReplacingMergeTree(catalog_version) ORDER BY category_id;
CREATE TABLE dim.organization (org_id UUID, name String, country FixedString(2), catalog_version UInt32)
ENGINE = ReplacingMergeTree(catalog_version) ORDER BY org_id;
CREATE TABLE dim.asn (asn UInt32, org_id UUID, name String, catalog_version UInt32)
ENGINE = ReplacingMergeTree(catalog_version) ORDER BY asn;
CREATE TABLE dim.watch_port (protocol UInt8, port UInt16, label String, version UInt32)
ENGINE = ReplacingMergeTree(version) ORDER BY (protocol, port);
