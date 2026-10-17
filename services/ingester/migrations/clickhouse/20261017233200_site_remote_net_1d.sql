-- FLOW I3 · Lista local por prefijos (capacidad: reparto local / internacional; docs/capacity-forecast.md §2).
-- flows.site_1d agrega por remote_asn y no guarda la IP remota: un prefijo local del ISP (LAN de un IXP, cachés on-net
-- como GGC/OCA/FNA) no se podía reconocer. flows.site_remote_net_1d guarda el volumen por nodo, día UTC, red remota
-- (IPv4 truncada a /24, IPv6 a /48: la granularidad de los anuncios BGP más específicos que se aceptan en Internet),
-- ASN y sentido. La consulta del reparto (rol snmp) filtra `remote_net` contra los rangos de la lista local del ISP con
-- comparaciones de IPv6 (arrayExists sobre los extremos de cada prefijo): barato y aplicado al consultar, así que
-- cambiar la lista corrige también los días ya guardados. Un prefijo más específico que /24 (o /48) se cuenta por su
-- /24 (o /48) completo (la consola lo avisa).
-- Solo `download` y `upload` (lo que reparte el informe) y sin IP remota nula. Retención: 90 días (el reparto pide como
-- mucho 90). Tabla nueva y aditiva: no cambia columnas, tipos ni ORDER BY de las del contrato C3 (se añade a
-- packages/schemas/datastore/v0/clickhouse-contract-v0.sql). Grants y row policies como attribution_5m; el relleno con
-- lo que hay en flows_raw (≤ 7 días) se hace tras crear la vista, antes de que el rol ingester empiece a consumir.

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS flows.site_remote_net_1d
(
    tenant_id UUID, site_id UUID, bucket Date, remote_net IPv6, remote_asn UInt32,
    direction Enum8('unknown' = 0, 'upload' = 1, 'download' = 2, 'internal' = 3),
    bytes SimpleAggregateFunction(sum, UInt64)
)
ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, bucket, remote_net, remote_asn, direction)
TTL bucket + INTERVAL 90 DAY DELETE
SETTINGS non_replicated_deduplication_window = 10000;

CREATE MATERIALIZED VIEW IF NOT EXISTS flows.mv_site_remote_net_1d TO flows.site_remote_net_1d
DEFINER = horus_mv SQL SECURITY DEFINER AS
SELECT tenant_id, site_id, toDate(ts) AS bucket,
       tupleElement(IPv6CIDRToRange(remote_ip,
         if(remote_ip BETWEEN toIPv6('::ffff:0.0.0.0') AND toIPv6('::ffff:255.255.255.255'), 120, 48)), 1) AS remote_net,
       remote_asn, direction, sum(bytes) AS bytes
FROM flows.flows_raw
WHERE direction IN ('download', 'upload') AND remote_ip != toIPv6('::')
GROUP BY tenant_id, site_id, bucket, remote_net, remote_asn, direction;

INSERT INTO flows.site_remote_net_1d
SELECT tenant_id, site_id, toDate(ts) AS bucket,
       tupleElement(IPv6CIDRToRange(remote_ip,
         if(remote_ip BETWEEN toIPv6('::ffff:0.0.0.0') AND toIPv6('::ffff:255.255.255.255'), 120, 48)), 1) AS remote_net,
       remote_asn, direction, sum(bytes) AS bytes
FROM flows.flows_raw
WHERE direction IN ('download', 'upload') AND remote_ip != toIPv6('::')
GROUP BY tenant_id, site_id, bucket, remote_net, remote_asn, direction;

GRANT SELECT ON flows.site_remote_net_1d TO horus_tenant_reader;
GRANT SELECT ON flows.site_remote_net_1d TO horus_platform_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.site_remote_net_1d AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.site_remote_net_1d AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;

-- +goose Down
DROP ROW POLICY IF EXISTS p_platform ON flows.site_remote_net_1d;
DROP ROW POLICY IF EXISTS p_tenant ON flows.site_remote_net_1d;
DROP VIEW IF EXISTS flows.mv_site_remote_net_1d;
DROP TABLE IF EXISTS flows.site_remote_net_1d;
