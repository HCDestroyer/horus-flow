-- FLOW · Capacidad de enlaces de subida (NOC; docs/database.md §6.3). Escritor único: el rol snmp (usuario
-- horus_snmp, rol horus_snmp_role). Lectores: el propio rol snmp por tenant (horus_tenant_reader, política p_tenant
-- con SQL_horus_tenant) y los procesos de plataforma.
--   snmp.probe_metrics   lecturas de las sondas de pérdida/latencia/jitter (netwatch del router, leído por la API
--                        REST de solo lectura; o sondas de respaldo desde el host de Horus). 30 días.
--   snmp.probe_5m / 1h   agregados en cascada (90 días / 13 meses), SimpleAggregateFunction como interface_5m.
--   snmp.uplink_daily    pico diario (p95 de las medias de 5 min), horas sobre umbral, errores y descartes por
--                        enlace y de la salida internacional agregada; lo escribe el trabajo diario de capacidad
--                        (ReplacingMergeTree: recalcular un día lo sustituye). 25 meses (pronóstico de 26 semanas e
--                        interanual).

-- +goose NO TRANSACTION
-- +goose Up
CREATE USER IF NOT EXISTS horus_mv IDENTIFIED WITH no_password HOST NONE;
CREATE USER IF NOT EXISTS horus_dict IDENTIFIED WITH no_password HOST LOCAL;
CREATE ROLE IF NOT EXISTS horus_tenant_reader;
CREATE ROLE IF NOT EXISTS horus_platform_reader;
CREATE ROLE IF NOT EXISTS horus_snmp_role;

CREATE TABLE IF NOT EXISTS snmp.probe_metrics
(
    tenant_id UUID, site_id UUID, router_id UUID, interface_id UUID, probe_id UUID,
    source LowCardinality(String), ref LowCardinality(String), target String, label String,
    ts DateTime('UTC'), up Nullable(UInt8), sent Nullable(UInt16), received Nullable(UInt16),
    loss_pct Nullable(Float32), rtt_avg_ms Nullable(Float32), rtt_min_ms Nullable(Float32),
    rtt_max_ms Nullable(Float32), jitter_ms Nullable(Float32)
)
ENGINE = MergeTree PARTITION BY toYYYYMMDD(ts)
ORDER BY (tenant_id, probe_id, ts)
TTL ts + INTERVAL 30 DAY DELETE;

CREATE TABLE IF NOT EXISTS snmp.probe_5m
(
    tenant_id UUID, probe_id UUID, bucket DateTime('UTC'),
    site_id SimpleAggregateFunction(anyLast, UUID), router_id SimpleAggregateFunction(anyLast, UUID),
    interface_id SimpleAggregateFunction(anyLast, UUID), source SimpleAggregateFunction(anyLast, String),
    ref SimpleAggregateFunction(anyLast, String), target SimpleAggregateFunction(anyLast, String),
    label SimpleAggregateFunction(anyLast, String),
    samples SimpleAggregateFunction(sum, UInt64), up_total SimpleAggregateFunction(sum, UInt64), up_count SimpleAggregateFunction(sum, UInt64),
    loss_pct_sum SimpleAggregateFunction(sum, Float64), loss_pct_count SimpleAggregateFunction(sum, UInt64), loss_pct_max SimpleAggregateFunction(max, Float32),
    rtt_avg_ms_sum SimpleAggregateFunction(sum, Float64), rtt_avg_ms_count SimpleAggregateFunction(sum, UInt64), rtt_avg_ms_max SimpleAggregateFunction(max, Float32),
    rtt_max_ms_max SimpleAggregateFunction(max, Float32),
    jitter_ms_sum SimpleAggregateFunction(sum, Float64), jitter_ms_count SimpleAggregateFunction(sum, UInt64), jitter_ms_max SimpleAggregateFunction(max, Float32)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, probe_id, bucket)
TTL bucket + INTERVAL 90 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS snmp.mv_probe_5m TO snmp.probe_5m
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, probe_id, toStartOfFiveMinutes(ts) AS bucket, anyLast(site_id) AS site_id, anyLast(router_id) AS router_id,
       anyLast(interface_id) AS interface_id, anyLast(toString(source)) AS source, anyLast(toString(ref)) AS ref,
       anyLast(target) AS target, anyLast(label) AS label,
       count() AS samples, countIf(isNotNull(up)) AS up_total, sum(ifNull(up, 0)) AS up_count,
       sum(ifNull(toFloat64(loss_pct), 0)) AS loss_pct_sum, countIf(isNotNull(loss_pct)) AS loss_pct_count, maxIf(assumeNotNull(loss_pct), isNotNull(loss_pct)) AS loss_pct_max,
       sum(ifNull(toFloat64(rtt_avg_ms), 0)) AS rtt_avg_ms_sum, countIf(isNotNull(rtt_avg_ms)) AS rtt_avg_ms_count, maxIf(assumeNotNull(rtt_avg_ms), isNotNull(rtt_avg_ms)) AS rtt_avg_ms_max,
       maxIf(assumeNotNull(rtt_max_ms), isNotNull(rtt_max_ms)) AS rtt_max_ms_max,
       sum(ifNull(toFloat64(jitter_ms), 0)) AS jitter_ms_sum, countIf(isNotNull(jitter_ms)) AS jitter_ms_count, maxIf(assumeNotNull(jitter_ms), isNotNull(jitter_ms)) AS jitter_ms_max
FROM snmp.probe_metrics
GROUP BY tenant_id, probe_id, bucket;

CREATE TABLE IF NOT EXISTS snmp.probe_1h AS snmp.probe_5m
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, probe_id, bucket)
TTL bucket + INTERVAL 13 MONTH DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS snmp.mv_probe_1h TO snmp.probe_1h
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, probe_id, toStartOfHour(p.bucket) AS bucket, anyLast(site_id) AS site_id, anyLast(router_id) AS router_id,
       anyLast(interface_id) AS interface_id, anyLast(source) AS source, anyLast(ref) AS ref, anyLast(target) AS target,
       anyLast(label) AS label,
       sum(samples) AS samples, sum(up_total) AS up_total, sum(up_count) AS up_count,
       sum(loss_pct_sum) AS loss_pct_sum, sum(loss_pct_count) AS loss_pct_count, max(loss_pct_max) AS loss_pct_max,
       sum(rtt_avg_ms_sum) AS rtt_avg_ms_sum, sum(rtt_avg_ms_count) AS rtt_avg_ms_count, max(rtt_avg_ms_max) AS rtt_avg_ms_max,
       max(rtt_max_ms_max) AS rtt_max_ms_max,
       sum(jitter_ms_sum) AS jitter_ms_sum, sum(jitter_ms_count) AS jitter_ms_count, max(jitter_ms_max) AS jitter_ms_max
FROM snmp.probe_5m AS p
GROUP BY tenant_id, probe_id, bucket;

CREATE TABLE IF NOT EXISTS snmp.uplink_daily
(
    tenant_id UUID, subject_id UUID, subject_kind LowCardinality(String), router_id UUID, site_id UUID, day Date,
    samples UInt32, rx_p95_bps Float64, tx_p95_bps Float64, rx_max_bps Float64, tx_max_bps Float64,
    capacity_bps UInt64, hours_above_warning Float32, hours_above_critical Float32,
    errors UInt64, drops UInt64, packets Float64, computed_at DateTime('UTC')
)
ENGINE = ReplacingMergeTree(computed_at) PARTITION BY toYYYYMM(day)
ORDER BY (tenant_id, subject_id, day)
TTL day + INTERVAL 25 MONTH DELETE;

GRANT SELECT, INSERT ON snmp.probe_metrics TO horus_mv;
GRANT SELECT, INSERT ON snmp.probe_5m TO horus_mv;
GRANT SELECT, INSERT ON snmp.probe_1h TO horus_mv;
GRANT INSERT ON snmp.probe_metrics TO horus_snmp_role;
GRANT INSERT ON snmp.uplink_daily TO horus_snmp_role;
GRANT SELECT ON snmp.probe_metrics TO horus_tenant_reader;
GRANT SELECT ON snmp.probe_5m TO horus_tenant_reader;
GRANT SELECT ON snmp.probe_1h TO horus_tenant_reader;
GRANT SELECT ON snmp.uplink_daily TO horus_tenant_reader;
GRANT SELECT ON snmp.probe_metrics TO horus_platform_reader;
GRANT SELECT ON snmp.probe_5m TO horus_platform_reader;
GRANT SELECT ON snmp.probe_1h TO horus_platform_reader;
GRANT SELECT ON snmp.uplink_daily TO horus_platform_reader;

CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.probe_metrics AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.probe_5m AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.probe_1h AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.uplink_daily AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_platform ON snmp.probe_metrics AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON snmp.probe_5m AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON snmp.probe_1h AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON snmp.uplink_daily AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;

-- +goose Down
DROP ROW POLICY IF EXISTS p_platform ON snmp.uplink_daily;
DROP ROW POLICY IF EXISTS p_tenant ON snmp.uplink_daily;
DROP ROW POLICY IF EXISTS p_platform ON snmp.probe_1h;
DROP ROW POLICY IF EXISTS p_tenant ON snmp.probe_1h;
DROP ROW POLICY IF EXISTS p_platform ON snmp.probe_5m;
DROP ROW POLICY IF EXISTS p_tenant ON snmp.probe_5m;
DROP ROW POLICY IF EXISTS p_platform ON snmp.probe_metrics;
DROP ROW POLICY IF EXISTS p_tenant ON snmp.probe_metrics;
DROP TABLE IF EXISTS snmp.uplink_daily;
DROP VIEW IF EXISTS snmp.mv_probe_1h;
DROP TABLE IF EXISTS snmp.probe_1h;
DROP VIEW IF EXISTS snmp.mv_probe_5m;
DROP TABLE IF EXISTS snmp.probe_5m;
DROP TABLE IF EXISTS snmp.probe_metrics;
