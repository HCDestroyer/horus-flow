-- I0-13 · Agregados de consumo por cliente y por nodo (contrato C3, docs/database.md §6.2).
-- Las MV se ejecutan como horus_mv (SQL SECURITY DEFINER): quien inserta en flows_raw no necesita
-- permisos sobre los agregados. TTL según docs/database.md §7 / storage.md §5.

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS flows.customer_5m
(
    tenant_id UUID, site_id UUID, realm_id UUID, client_ip IPv6, bucket DateTime('UTC'), service_id UUID,
    direction Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    bytes SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64),
    flows SimpleAggregateFunction(sum, UInt64), remote_ips AggregateFunction(uniq, IPv6)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, realm_id, client_ip, bucket, service_id, direction)
TTL bucket + INTERVAL 90 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_customer_5m TO flows.customer_5m
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, realm_id, client_ip, toStartOfFiveMinutes(ts) AS bucket, service_id, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(remote_ip) AS remote_ips
FROM flows.flows_raw WHERE attribution_status IN ('attributed', 'internal')
GROUP BY tenant_id, site_id, realm_id, client_ip, bucket, service_id, direction;

CREATE TABLE IF NOT EXISTS flows.customer_1h
(
    tenant_id UUID, site_id UUID, realm_id UUID, client_ip IPv6, bucket DateTime('UTC'), service_id UUID, remote_asn UInt32,
    direction Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    bytes SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64),
    flows SimpleAggregateFunction(sum, UInt64), remote_ips AggregateFunction(uniq, IPv6)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, realm_id, client_ip, bucket, service_id, remote_asn, direction)
TTL bucket + INTERVAL 13 MONTH DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_customer_1h TO flows.customer_1h
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, realm_id, client_ip, toStartOfHour(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(remote_ip) AS remote_ips
FROM flows.flows_raw WHERE attribution_status IN ('attributed', 'internal')
GROUP BY tenant_id, site_id, realm_id, client_ip, bucket, service_id, remote_asn, direction;

CREATE TABLE IF NOT EXISTS flows.customer_1d
(
    tenant_id UUID, realm_id UUID, client_ip IPv6, bucket Date, service_id UUID, remote_asn UInt32,
    direction Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    bytes SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64),
    flows SimpleAggregateFunction(sum, UInt64), remote_ips AggregateFunction(uniq, IPv6), remote_asns AggregateFunction(uniq, UInt32)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, realm_id, client_ip, bucket, service_id, remote_asn, direction)
TTL bucket + INTERVAL 25 MONTH DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_customer_1d TO flows.customer_1d
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, realm_id, client_ip, toDate(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows,
       uniqState(remote_ip) AS remote_ips, uniqState(remote_asn) AS remote_asns
FROM flows.flows_raw WHERE attribution_status IN ('attributed', 'internal')
GROUP BY tenant_id, realm_id, client_ip, bucket, service_id, remote_asn, direction;

-- Consumo por nodo (incluye lo no atribuido: el nodo en modo descubrimiento sigue contando).
CREATE TABLE IF NOT EXISTS flows.site_5m
(
    tenant_id UUID, site_id UUID, bucket DateTime('UTC'), service_id UUID, remote_asn UInt32,
    direction Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    bytes SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64),
    flows SimpleAggregateFunction(sum, UInt64), clients AggregateFunction(uniq, UUID, IPv6)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, bucket, service_id, remote_asn, direction)
TTL bucket + INTERVAL 90 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_site_5m TO flows.site_5m
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, toStartOfFiveMinutes(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(realm_id, client_ip) AS clients
FROM flows.flows_raw
GROUP BY tenant_id, site_id, bucket, service_id, remote_asn, direction;

CREATE TABLE IF NOT EXISTS flows.site_1h AS flows.site_5m
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, bucket, service_id, remote_asn, direction)
TTL bucket + INTERVAL 13 MONTH DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_site_1h TO flows.site_1h
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, toStartOfHour(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(realm_id, client_ip) AS clients
FROM flows.flows_raw
GROUP BY tenant_id, site_id, bucket, service_id, remote_asn, direction;

CREATE TABLE IF NOT EXISTS flows.site_1d AS flows.site_5m
ENGINE = AggregatingMergeTree PARTITION BY toYear(bucket)
ORDER BY (tenant_id, site_id, bucket, service_id, remote_asn, direction)
TTL bucket + INTERVAL 5 YEAR DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_site_1d TO flows.site_1d
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, toStartOfDay(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(realm_id, client_ip) AS clients
FROM flows.flows_raw
GROUP BY tenant_id, site_id, bucket, service_id, remote_asn, direction;

-- IPs que parecen de clientes fuera de todo prefijo: base del modo descubrimiento (I1-29).
CREATE TABLE IF NOT EXISTS flows.unattributed_1h
(
    tenant_id UUID, site_id UUID, bucket DateTime('UTC'), client_ip IPv6,
    bytes SimpleAggregateFunction(sum, UInt64), packets SimpleAggregateFunction(sum, UInt64)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, bucket, client_ip)
TTL bucket + INTERVAL 30 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_unattributed_1h TO flows.unattributed_1h
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, toStartOfHour(ts) AS bucket, client_ip, sum(bytes) AS bytes, sum(packets) AS packets
FROM flows.flows_raw WHERE attribution_status = 'unknown'
GROUP BY tenant_id, site_id, bucket, client_ip;

-- +goose Down
DROP VIEW IF EXISTS flows.mv_unattributed_1h;
DROP TABLE IF EXISTS flows.unattributed_1h;
DROP VIEW IF EXISTS flows.mv_site_1d;
DROP TABLE IF EXISTS flows.site_1d;
DROP VIEW IF EXISTS flows.mv_site_1h;
DROP TABLE IF EXISTS flows.site_1h;
DROP VIEW IF EXISTS flows.mv_site_5m;
DROP TABLE IF EXISTS flows.site_5m;
DROP VIEW IF EXISTS flows.mv_customer_1d;
DROP TABLE IF EXISTS flows.customer_1d;
DROP VIEW IF EXISTS flows.mv_customer_1h;
DROP TABLE IF EXISTS flows.customer_1h;
DROP VIEW IF EXISTS flows.mv_customer_5m;
DROP TABLE IF EXISTS flows.customer_5m;
