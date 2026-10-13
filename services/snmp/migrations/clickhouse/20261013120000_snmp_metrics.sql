-- FLOW I2 · Métricas SNMP (docs/database.md §6.3, §9; docs/events.md §8.4). Escritor único: el rol
-- snmp (usuario horus_snmp, rol horus_snmp_role). Lectores: analytics (horus_tenant_reader con la
-- política p_tenant = la de flows) y los procesos de plataforma. Los roles y usuarios internos se
-- crean IF NOT EXISTS para que esta migración funcione antes o después de las de flows (ingester).
-- Las MV se ejecutan como horus_mv (SQL SECURITY DEFINER), como en flows.
-- Agregados: SimpleAggregateFunction (sum/count/max; site_id anyLast, fuera de la clave): se leen con
-- sum()/max() normales; la media es
-- <métrica>_sum / <métrica>_count; availability_ratio = icmp_ok_count / icmp_total.

-- +goose NO TRANSACTION
-- +goose Up
CREATE DATABASE IF NOT EXISTS snmp;

CREATE USER IF NOT EXISTS horus_mv IDENTIFIED WITH no_password HOST NONE;
GRANT SELECT, INSERT ON snmp.* TO horus_mv;
CREATE USER IF NOT EXISTS horus_dict IDENTIFIED WITH no_password HOST LOCAL;
CREATE ROLE IF NOT EXISTS horus_tenant_reader;
CREATE ROLE IF NOT EXISTS horus_platform_reader;
CREATE ROLE IF NOT EXISTS horus_analytics_role;
CREATE ROLE IF NOT EXISTS horus_alerts_role;
CREATE ROLE IF NOT EXISTS horus_jobs_role;
CREATE ROLE IF NOT EXISTS horus_snmp_role;

CREATE TABLE IF NOT EXISTS snmp.device_metrics
(
    tenant_id UUID, site_id UUID, router_id UUID, ts DateTime('UTC'),
    poll_duration_ms UInt32, icmp_ok UInt8, snmp_ok UInt8,
    uptime_seconds Nullable(UInt64),
    cpu_percent Nullable(Float32), cpu_max_core_percent Nullable(Float32),
    memory_percent Nullable(Float32), memory_used_bytes Nullable(UInt64), memory_total_bytes Nullable(UInt64),
    temperature_celsius Nullable(Float32), cpu_temperature_celsius Nullable(Float32), voltage Nullable(Float32),
    icmp_rtt_ms Nullable(Float32), icmp_loss_ratio Nullable(Float32)
)
ENGINE = MergeTree PARTITION BY toYYYYMMDD(ts)
ORDER BY (tenant_id, router_id, ts)
TTL ts + INTERVAL 30 DAY DELETE;

CREATE TABLE IF NOT EXISTS snmp.interface_metrics
(
    tenant_id UUID, site_id UUID, router_id UUID, interface_id UUID, if_index UInt32, name LowCardinality(String),
    ts DateTime('UTC'), oper_up UInt8, speed_bps Nullable(UInt64),
    rx_bps Nullable(Float64), tx_bps Nullable(Float64), rx_pps Nullable(Float64), tx_pps Nullable(Float64),
    rx_errors_delta Nullable(UInt64), tx_errors_delta Nullable(UInt64),
    rx_drops_delta Nullable(UInt64), tx_drops_delta Nullable(UInt64),
    utilization_rx Nullable(Float32), utilization_tx Nullable(Float32)
)
ENGINE = MergeTree PARTITION BY toYYYYMMDD(ts)
ORDER BY (tenant_id, router_id, interface_id, ts)
TTL ts + INTERVAL 30 DAY DELETE;

CREATE TABLE IF NOT EXISTS snmp.device_5m
(
    tenant_id UUID, router_id UUID, bucket DateTime('UTC'), site_id SimpleAggregateFunction(anyLast, UUID),
    samples SimpleAggregateFunction(sum, UInt64),
    icmp_total SimpleAggregateFunction(sum, UInt64), icmp_ok_count SimpleAggregateFunction(sum, UInt64),
    snmp_total SimpleAggregateFunction(sum, UInt64), snmp_ok_count SimpleAggregateFunction(sum, UInt64),
    poll_duration_ms_sum SimpleAggregateFunction(sum, UInt64), poll_duration_ms_max SimpleAggregateFunction(max, UInt32),
    uptime_seconds_max SimpleAggregateFunction(max, UInt64),
    cpu_percent_sum SimpleAggregateFunction(sum, Float64), cpu_percent_count SimpleAggregateFunction(sum, UInt64), cpu_percent_max SimpleAggregateFunction(max, Float32),
    cpu_max_core_percent_sum SimpleAggregateFunction(sum, Float64), cpu_max_core_percent_count SimpleAggregateFunction(sum, UInt64), cpu_max_core_percent_max SimpleAggregateFunction(max, Float32),
    memory_percent_sum SimpleAggregateFunction(sum, Float64), memory_percent_count SimpleAggregateFunction(sum, UInt64), memory_percent_max SimpleAggregateFunction(max, Float32),
    temperature_celsius_sum SimpleAggregateFunction(sum, Float64), temperature_celsius_count SimpleAggregateFunction(sum, UInt64), temperature_celsius_max SimpleAggregateFunction(max, Float32),
    cpu_temperature_celsius_sum SimpleAggregateFunction(sum, Float64), cpu_temperature_celsius_count SimpleAggregateFunction(sum, UInt64), cpu_temperature_celsius_max SimpleAggregateFunction(max, Float32),
    icmp_rtt_ms_sum SimpleAggregateFunction(sum, Float64), icmp_rtt_ms_count SimpleAggregateFunction(sum, UInt64), icmp_rtt_ms_max SimpleAggregateFunction(max, Float32)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, router_id, bucket)
TTL bucket + INTERVAL 90 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS snmp.mv_device_5m TO snmp.device_5m
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, router_id, toStartOfFiveMinutes(ts) AS bucket, anyLast(site_id) AS site_id,
       count() AS samples,
       count() AS icmp_total, sum(icmp_ok) AS icmp_ok_count, count() AS snmp_total, sum(snmp_ok) AS snmp_ok_count,
       sum(poll_duration_ms) AS poll_duration_ms_sum, max(poll_duration_ms) AS poll_duration_ms_max,
       maxIf(assumeNotNull(uptime_seconds), isNotNull(uptime_seconds)) AS uptime_seconds_max,
       sum(ifNull(toFloat64(cpu_percent), 0)) AS cpu_percent_sum, countIf(isNotNull(cpu_percent)) AS cpu_percent_count, maxIf(assumeNotNull(cpu_percent), isNotNull(cpu_percent)) AS cpu_percent_max,
       sum(ifNull(toFloat64(cpu_max_core_percent), 0)) AS cpu_max_core_percent_sum, countIf(isNotNull(cpu_max_core_percent)) AS cpu_max_core_percent_count, maxIf(assumeNotNull(cpu_max_core_percent), isNotNull(cpu_max_core_percent)) AS cpu_max_core_percent_max,
       sum(ifNull(toFloat64(memory_percent), 0)) AS memory_percent_sum, countIf(isNotNull(memory_percent)) AS memory_percent_count, maxIf(assumeNotNull(memory_percent), isNotNull(memory_percent)) AS memory_percent_max,
       sum(ifNull(toFloat64(temperature_celsius), 0)) AS temperature_celsius_sum, countIf(isNotNull(temperature_celsius)) AS temperature_celsius_count, maxIf(assumeNotNull(temperature_celsius), isNotNull(temperature_celsius)) AS temperature_celsius_max,
       sum(ifNull(toFloat64(cpu_temperature_celsius), 0)) AS cpu_temperature_celsius_sum, countIf(isNotNull(cpu_temperature_celsius)) AS cpu_temperature_celsius_count, maxIf(assumeNotNull(cpu_temperature_celsius), isNotNull(cpu_temperature_celsius)) AS cpu_temperature_celsius_max,
       sum(ifNull(toFloat64(icmp_rtt_ms), 0)) AS icmp_rtt_ms_sum, countIf(isNotNull(icmp_rtt_ms)) AS icmp_rtt_ms_count, maxIf(assumeNotNull(icmp_rtt_ms), isNotNull(icmp_rtt_ms)) AS icmp_rtt_ms_max
FROM snmp.device_metrics
GROUP BY tenant_id, router_id, bucket;

CREATE TABLE IF NOT EXISTS snmp.device_1h AS snmp.device_5m
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, router_id, bucket)
TTL bucket + INTERVAL 13 MONTH DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS snmp.mv_device_1h TO snmp.device_1h
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, router_id, toStartOfHour(d.bucket) AS bucket, anyLast(site_id) AS site_id,
       sum(samples) AS samples, sum(icmp_total) AS icmp_total, sum(icmp_ok_count) AS icmp_ok_count,
       sum(snmp_total) AS snmp_total, sum(snmp_ok_count) AS snmp_ok_count,
       sum(poll_duration_ms_sum) AS poll_duration_ms_sum, max(poll_duration_ms_max) AS poll_duration_ms_max,
       max(uptime_seconds_max) AS uptime_seconds_max,
       sum(cpu_percent_sum) AS cpu_percent_sum, sum(cpu_percent_count) AS cpu_percent_count, max(cpu_percent_max) AS cpu_percent_max,
       sum(cpu_max_core_percent_sum) AS cpu_max_core_percent_sum, sum(cpu_max_core_percent_count) AS cpu_max_core_percent_count, max(cpu_max_core_percent_max) AS cpu_max_core_percent_max,
       sum(memory_percent_sum) AS memory_percent_sum, sum(memory_percent_count) AS memory_percent_count, max(memory_percent_max) AS memory_percent_max,
       sum(temperature_celsius_sum) AS temperature_celsius_sum, sum(temperature_celsius_count) AS temperature_celsius_count, max(temperature_celsius_max) AS temperature_celsius_max,
       sum(cpu_temperature_celsius_sum) AS cpu_temperature_celsius_sum, sum(cpu_temperature_celsius_count) AS cpu_temperature_celsius_count, max(cpu_temperature_celsius_max) AS cpu_temperature_celsius_max,
       sum(icmp_rtt_ms_sum) AS icmp_rtt_ms_sum, sum(icmp_rtt_ms_count) AS icmp_rtt_ms_count, max(icmp_rtt_ms_max) AS icmp_rtt_ms_max
FROM snmp.device_5m AS d
GROUP BY tenant_id, router_id, bucket;

CREATE TABLE IF NOT EXISTS snmp.interface_5m
(
    tenant_id UUID, router_id UUID, interface_id UUID, bucket DateTime('UTC'), site_id SimpleAggregateFunction(anyLast, UUID),
    if_index SimpleAggregateFunction(anyLast, UInt32), name SimpleAggregateFunction(anyLast, String),
    samples SimpleAggregateFunction(sum, UInt64), oper_up_count SimpleAggregateFunction(sum, UInt64),
    speed_bps_max SimpleAggregateFunction(max, UInt64),
    rx_bps_sum SimpleAggregateFunction(sum, Float64), rx_bps_count SimpleAggregateFunction(sum, UInt64), rx_bps_max SimpleAggregateFunction(max, Float64),
    tx_bps_sum SimpleAggregateFunction(sum, Float64), tx_bps_count SimpleAggregateFunction(sum, UInt64), tx_bps_max SimpleAggregateFunction(max, Float64),
    rx_pps_sum SimpleAggregateFunction(sum, Float64), rx_pps_count SimpleAggregateFunction(sum, UInt64), rx_pps_max SimpleAggregateFunction(max, Float64),
    tx_pps_sum SimpleAggregateFunction(sum, Float64), tx_pps_count SimpleAggregateFunction(sum, UInt64), tx_pps_max SimpleAggregateFunction(max, Float64),
    rx_errors SimpleAggregateFunction(sum, UInt64), tx_errors SimpleAggregateFunction(sum, UInt64),
    rx_drops SimpleAggregateFunction(sum, UInt64), tx_drops SimpleAggregateFunction(sum, UInt64),
    utilization_rx_max SimpleAggregateFunction(max, Float32), utilization_tx_max SimpleAggregateFunction(max, Float32)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, router_id, interface_id, bucket)
TTL bucket + INTERVAL 90 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS snmp.mv_interface_5m TO snmp.interface_5m
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, router_id, interface_id, toStartOfFiveMinutes(ts) AS bucket, anyLast(site_id) AS site_id,
       anyLast(if_index) AS if_index, anyLast(toString(name)) AS name,
       count() AS samples, sum(oper_up) AS oper_up_count,
       maxIf(assumeNotNull(speed_bps), isNotNull(speed_bps)) AS speed_bps_max,
       sum(ifNull(rx_bps, 0)) AS rx_bps_sum, countIf(isNotNull(rx_bps)) AS rx_bps_count, maxIf(assumeNotNull(rx_bps), isNotNull(rx_bps)) AS rx_bps_max,
       sum(ifNull(tx_bps, 0)) AS tx_bps_sum, countIf(isNotNull(tx_bps)) AS tx_bps_count, maxIf(assumeNotNull(tx_bps), isNotNull(tx_bps)) AS tx_bps_max,
       sum(ifNull(rx_pps, 0)) AS rx_pps_sum, countIf(isNotNull(rx_pps)) AS rx_pps_count, maxIf(assumeNotNull(rx_pps), isNotNull(rx_pps)) AS rx_pps_max,
       sum(ifNull(tx_pps, 0)) AS tx_pps_sum, countIf(isNotNull(tx_pps)) AS tx_pps_count, maxIf(assumeNotNull(tx_pps), isNotNull(tx_pps)) AS tx_pps_max,
       sum(ifNull(rx_errors_delta, 0)) AS rx_errors, sum(ifNull(tx_errors_delta, 0)) AS tx_errors,
       sum(ifNull(rx_drops_delta, 0)) AS rx_drops, sum(ifNull(tx_drops_delta, 0)) AS tx_drops,
       maxIf(assumeNotNull(utilization_rx), isNotNull(utilization_rx)) AS utilization_rx_max,
       maxIf(assumeNotNull(utilization_tx), isNotNull(utilization_tx)) AS utilization_tx_max
FROM snmp.interface_metrics
GROUP BY tenant_id, router_id, interface_id, bucket;

CREATE TABLE IF NOT EXISTS snmp.interface_1h AS snmp.interface_5m
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, router_id, interface_id, bucket)
TTL bucket + INTERVAL 13 MONTH DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS snmp.mv_interface_1h TO snmp.interface_1h
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, router_id, interface_id, toStartOfHour(i.bucket) AS bucket, anyLast(site_id) AS site_id,
       anyLast(if_index) AS if_index, anyLast(name) AS name,
       sum(samples) AS samples, sum(oper_up_count) AS oper_up_count, max(speed_bps_max) AS speed_bps_max,
       sum(rx_bps_sum) AS rx_bps_sum, sum(rx_bps_count) AS rx_bps_count, max(rx_bps_max) AS rx_bps_max,
       sum(tx_bps_sum) AS tx_bps_sum, sum(tx_bps_count) AS tx_bps_count, max(tx_bps_max) AS tx_bps_max,
       sum(rx_pps_sum) AS rx_pps_sum, sum(rx_pps_count) AS rx_pps_count, max(rx_pps_max) AS rx_pps_max,
       sum(tx_pps_sum) AS tx_pps_sum, sum(tx_pps_count) AS tx_pps_count, max(tx_pps_max) AS tx_pps_max,
       sum(rx_errors) AS rx_errors, sum(tx_errors) AS tx_errors, sum(rx_drops) AS rx_drops, sum(tx_drops) AS tx_drops,
       max(utilization_rx_max) AS utilization_rx_max, max(utilization_tx_max) AS utilization_tx_max
FROM snmp.interface_5m AS i
GROUP BY tenant_id, router_id, interface_id, bucket;

-- Escritor único y lectores (database.md §5.1).
GRANT INSERT ON snmp.device_metrics TO horus_snmp_role;
GRANT INSERT ON snmp.interface_metrics TO horus_snmp_role;
GRANT SELECT ON snmp.device_metrics TO horus_tenant_reader;
GRANT SELECT ON snmp.interface_metrics TO horus_tenant_reader;
GRANT SELECT ON snmp.device_5m TO horus_tenant_reader;
GRANT SELECT ON snmp.device_1h TO horus_tenant_reader;
GRANT SELECT ON snmp.interface_5m TO horus_tenant_reader;
GRANT SELECT ON snmp.interface_1h TO horus_tenant_reader;
GRANT SELECT ON snmp.device_metrics TO horus_platform_reader;
GRANT SELECT ON snmp.interface_metrics TO horus_platform_reader;
GRANT SELECT ON snmp.device_5m TO horus_platform_reader;
GRANT SELECT ON snmp.device_1h TO horus_platform_reader;
GRANT SELECT ON snmp.interface_5m TO horus_platform_reader;
GRANT SELECT ON snmp.interface_1h TO horus_platform_reader;
GRANT horus_tenant_reader TO horus_analytics_role;
GRANT horus_tenant_reader TO horus_alerts_role;
GRANT horus_platform_reader TO horus_jobs_role;
GRANT ALTER DELETE ON snmp.* TO horus_jobs_role;

CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.device_metrics AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.interface_metrics AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.device_5m AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.device_1h AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.interface_5m AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON snmp.interface_1h AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_platform ON snmp.device_metrics AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON snmp.interface_metrics AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON snmp.device_5m AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON snmp.device_1h AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON snmp.interface_5m AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON snmp.interface_1h AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;

-- +goose Down
DROP ROW POLICY IF EXISTS p_platform ON snmp.interface_1h;
DROP ROW POLICY IF EXISTS p_tenant ON snmp.interface_1h;
DROP ROW POLICY IF EXISTS p_platform ON snmp.interface_5m;
DROP ROW POLICY IF EXISTS p_tenant ON snmp.interface_5m;
DROP ROW POLICY IF EXISTS p_platform ON snmp.device_1h;
DROP ROW POLICY IF EXISTS p_tenant ON snmp.device_1h;
DROP ROW POLICY IF EXISTS p_platform ON snmp.device_5m;
DROP ROW POLICY IF EXISTS p_tenant ON snmp.device_5m;
DROP ROW POLICY IF EXISTS p_platform ON snmp.interface_metrics;
DROP ROW POLICY IF EXISTS p_tenant ON snmp.interface_metrics;
DROP ROW POLICY IF EXISTS p_platform ON snmp.device_metrics;
DROP ROW POLICY IF EXISTS p_tenant ON snmp.device_metrics;
DROP VIEW IF EXISTS snmp.mv_interface_1h;
DROP TABLE IF EXISTS snmp.interface_1h;
DROP VIEW IF EXISTS snmp.mv_interface_5m;
DROP TABLE IF EXISTS snmp.interface_5m;
DROP VIEW IF EXISTS snmp.mv_device_1h;
DROP TABLE IF EXISTS snmp.device_1h;
DROP VIEW IF EXISTS snmp.mv_device_5m;
DROP TABLE IF EXISTS snmp.device_5m;
DROP TABLE IF EXISTS snmp.interface_metrics;
DROP TABLE IF EXISTS snmp.device_metrics;
DROP ROLE IF EXISTS horus_snmp_role;
