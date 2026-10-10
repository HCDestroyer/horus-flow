-- FLOW · isp10k · Vistas de 1 h y 1 d en cascada (sin cambiar tablas ni columnas del contrato C3).
-- Antes, 10 de las 11 vistas materializadas leían cada bloque insertado en flows_raw. site_1h y
-- site_1d tienen la misma clave que site_5m con un bucket más grueso, y customer_1d es customer_1h
-- sin site_id y con bucket diario: se calculan a partir del bloque ya agregado de site_5m y de
-- customer_1h (decenas de veces más pequeño), combinando estados (uniqMergeState) como ya hace
-- client_security_1h. El resultado es el mismo: sumas de sumas, y uniq de la unión de estados;
-- remote_asns de customer_1d sale de la columna remote_asn, que es clave de customer_1h.
-- Quedan 7 vistas sobre flows_raw (customer_5m, customer_1h, site_5m, unattributed_1h,
-- client_security_1m, client_port_1m, reputation_hit) y 4 en cascada.
-- Se aplica al arrancar el rol, antes de consumir: entre el DROP y el CREATE no hay inserciones.

-- +goose NO TRANSACTION
-- +goose Up
DROP VIEW IF EXISTS flows.mv_site_1h;
CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_site_1h TO flows.site_1h
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, toStartOfHour(s.bucket) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(flows) AS flows, uniqMergeState(clients) AS clients
FROM flows.site_5m AS s
GROUP BY tenant_id, site_id, bucket, service_id, remote_asn, direction;

DROP VIEW IF EXISTS flows.mv_site_1d;
CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_site_1d TO flows.site_1d
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, toStartOfDay(s.bucket) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(flows) AS flows, uniqMergeState(clients) AS clients
FROM flows.site_5m AS s
GROUP BY tenant_id, site_id, bucket, service_id, remote_asn, direction;

DROP VIEW IF EXISTS flows.mv_customer_1d;
CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_customer_1d TO flows.customer_1d
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, realm_id, client_ip, toDate(c.bucket) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(flows) AS flows,
       uniqMergeState(remote_ips) AS remote_ips, uniqState(remote_asn) AS remote_asns
FROM flows.customer_1h AS c
GROUP BY tenant_id, realm_id, client_ip, bucket, service_id, remote_asn, direction;

-- +goose Down
DROP VIEW IF EXISTS flows.mv_customer_1d;
CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_customer_1d TO flows.customer_1d
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, realm_id, client_ip, toDate(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows,
       uniqState(remote_ip) AS remote_ips, uniqState(remote_asn) AS remote_asns
FROM flows.flows_raw WHERE attribution_status IN ('attributed', 'internal')
GROUP BY tenant_id, realm_id, client_ip, bucket, service_id, remote_asn, direction;

DROP VIEW IF EXISTS flows.mv_site_1d;
CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_site_1d TO flows.site_1d
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, toStartOfDay(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(realm_id, client_ip) AS clients
FROM flows.flows_raw
GROUP BY tenant_id, site_id, bucket, service_id, remote_asn, direction;

DROP VIEW IF EXISTS flows.mv_site_1h;
CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_site_1h TO flows.site_1h
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, toStartOfHour(ts) AS bucket, service_id, remote_asn, direction,
       sum(bytes) AS bytes, sum(packets) AS packets, sum(toUInt64(merged_flows)) AS flows, uniqState(realm_id, client_ip) AS clients
FROM flows.flows_raw
GROUP BY tenant_id, site_id, bucket, service_id, remote_asn, direction;
