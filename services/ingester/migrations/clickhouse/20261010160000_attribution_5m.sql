-- INT · nocturno load-i1 · Atribución por nodo en un agregado de 5 min (flows.attribution_5m).
-- GET /analytics/traffic/attribution (24 h por defecto) sumaba flows_raw por attribution_status
-- filtrando solo por ts, que es la ÚLTIMA columna del ORDER BY (tenant_id, site_id, realm_id,
-- client_ip, ts): leía todas las filas del ISP de las particiones diarias tocadas. A 5 000 flujos/s
-- son 18 M de filas por hora y su p95 crecía con el día (452 ms a los 20 min en 4 vCPU; el p95 de la
-- API llegó a 747 ms en la hora del nocturno, run 38042743182). Las demás consultas de tráfico leen
-- agregados (≈ 30 ms). Tabla nueva y aditiva: no cambia columnas, tipos ni ORDER BY de las del
-- contrato C3 (se añade a packages/schemas/datastore/v0/clickhouse-contract-v0.sql).
-- Retención como site_5m (90 días) y la ventana de deduplicación de los demás agregados
-- (20261010150000). Grants y row policies como site_5m (20261008120500): lectores de tenant con
-- p_tenant, plataforma y usuarios internos con p_platform; horus_mv ya tiene INSERT en flows.*.
-- El relleno inicial con lo que ya hay en flows_raw (≤ 7 días) se hace tras crear la vista: la
-- migración corre al arrancar el rol ingester, antes de consumir, así que no hay inserciones entre
-- medias que se cuenten dos veces.

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS flows.attribution_5m
(
    tenant_id UUID, site_id UUID, bucket DateTime('UTC'),
    attribution_status Enum8('unknown' = 0, 'attributed' = 1, 'infrastructure' = 2, 'transit' = 3, 'internal' = 4),
    bytes SimpleAggregateFunction(sum, UInt64), flows SimpleAggregateFunction(sum, UInt64)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, bucket, attribution_status)
TTL bucket + INTERVAL 90 DAY DELETE
SETTINGS non_replicated_deduplication_window = 10000;

CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_attribution_5m TO flows.attribution_5m
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, toStartOfFiveMinutes(ts) AS bucket, attribution_status,
       sum(bytes) AS bytes, sum(toUInt64(merged_flows)) AS flows
FROM flows.flows_raw
GROUP BY tenant_id, site_id, bucket, attribution_status;

INSERT INTO flows.attribution_5m
SELECT tenant_id, site_id, toStartOfFiveMinutes(ts) AS bucket, attribution_status,
       sum(bytes) AS bytes, sum(toUInt64(merged_flows)) AS flows
FROM flows.flows_raw
GROUP BY tenant_id, site_id, bucket, attribution_status;

GRANT SELECT ON flows.attribution_5m TO horus_tenant_reader;
GRANT SELECT ON flows.attribution_5m TO horus_platform_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.attribution_5m AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.attribution_5m AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;

-- +goose Down
DROP ROW POLICY IF EXISTS p_platform ON flows.attribution_5m;
DROP ROW POLICY IF EXISTS p_tenant ON flows.attribution_5m;
DROP VIEW IF EXISTS flows.mv_attribution_5m;
DROP TABLE IF EXISTS flows.attribution_5m;
