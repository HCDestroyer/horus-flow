-- FLOW · isp10k · Códecs de flows_raw (sin cambiar columnas, tipos, ORDER BY, particiones ni TTL del contrato C3).
-- Medido con 2 M de filas reales del escenario isp10k (tests/load/REPORT.md, «Compresión»): 32,1 B/fila con los
-- códecs de I0-13 (LZ4 por defecto en la mayoría de columnas) y 17,6 B/fila con estos (−45 %):
--   * ts, flow_start y received_at: ZSTD(3) en lugar de DoubleDelta. Las filas van ordenadas por cliente y luego por
--     ts, así que las segundas diferencias saltan en cada cliente: DoubleDelta daba 2,4 B/fila y ZSTD solo 1,2.
--   * IPs, UUID (batch_id, servicio, organización, categoría), puertos, enums y UInt8: ZSTD(3) en lugar de LZ4
--     (remote_ip 2,9 → 1,4 B/fila; batch_id 4,8 → 2,7; service_id/org/categoría ~1,5 → 0,6 cada una).
--   * Contadores y ASN: T64 + ZSTD(3).
-- ZSTD(9) solo bajaría a 16,1 B/fila (−9 %) con ~3× más CPU en los merges: no compensa. TTL RECOMPRESS no se usa en
-- flows_raw porque solo afecta a las columnas sin códec explícito.
-- Solo cambian los metadatos: las partes nuevas y las que se fusionen usan los códecs nuevos (sin mutación).

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE flows.flows_raw
    MODIFY COLUMN ts DateTime64(3, 'UTC') CODEC(ZSTD(3)),
    MODIFY COLUMN flow_start DateTime64(3, 'UTC') CODEC(ZSTD(3)),
    MODIFY COLUMN received_at DateTime64(3, 'UTC') CODEC(ZSTD(3)),
    MODIFY COLUMN client_port UInt16 CODEC(ZSTD(3)),
    MODIFY COLUMN remote_ip IPv6 CODEC(ZSTD(3)),
    MODIFY COLUMN remote_port UInt16 CODEC(ZSTD(3)),
    MODIFY COLUMN protocol UInt8 CODEC(ZSTD(3)),
    MODIFY COLUMN tcp_flags UInt8 CODEC(ZSTD(3)),
    MODIFY COLUMN bytes UInt64 CODEC(T64, ZSTD(3)),
    MODIFY COLUMN packets UInt64 CODEC(T64, ZSTD(3)),
    MODIFY COLUMN duration_ms UInt32 CODEC(T64, ZSTD(3)),
    MODIFY COLUMN remote_asn UInt32 CODEC(T64, ZSTD(3)),
    MODIFY COLUMN remote_prefix IPv6 CODEC(ZSTD(3)),
    MODIFY COLUMN remote_prefix_len UInt8 CODEC(ZSTD(3)),
    MODIFY COLUMN remote_org_id UUID CODEC(ZSTD(3)),
    MODIFY COLUMN remote_country LowCardinality(FixedString(2)) CODEC(ZSTD(3)),
    MODIFY COLUMN service_id UUID CODEC(ZSTD(3)),
    MODIFY COLUMN classification_method Enum8('none' = 0, 'local_override' = 1, 'prefix' = 2, 'asn_port' = 3, 'asn' = 4, 'port' = 5, 'sni' = 6, 'dns' = 7, 'heuristic' = 8) CODEC(ZSTD(3)),
    MODIFY COLUMN classification_confidence UInt8 CODEC(ZSTD(3)),
    MODIFY COLUMN category_id UUID CODEC(ZSTD(3)),
    MODIFY COLUMN reputation_category Enum8('none' = 0, 'botnet_cc' = 1, 'scanner' = 2, 'malware_dist' = 3, 'mining_pool' = 4, 'proxy_vpn' = 5, 'tor_exit' = 6, 'blocklist' = 7) CODEC(ZSTD(3)),
    MODIFY COLUMN batch_id UUID CODEC(ZSTD(3));

-- +goose Down
ALTER TABLE flows.flows_raw
    MODIFY COLUMN ts DateTime64(3, 'UTC') CODEC(DoubleDelta, ZSTD(1)),
    MODIFY COLUMN flow_start DateTime64(3, 'UTC') CODEC(DoubleDelta, ZSTD(1)),
    MODIFY COLUMN received_at DateTime64(3, 'UTC') CODEC(DoubleDelta, ZSTD(1)),
    MODIFY COLUMN client_port REMOVE CODEC,
    MODIFY COLUMN remote_ip REMOVE CODEC,
    MODIFY COLUMN remote_port REMOVE CODEC,
    MODIFY COLUMN protocol REMOVE CODEC,
    MODIFY COLUMN tcp_flags REMOVE CODEC,
    MODIFY COLUMN bytes UInt64 CODEC(T64, ZSTD(1)),
    MODIFY COLUMN packets UInt64 CODEC(T64, ZSTD(1)),
    MODIFY COLUMN duration_ms UInt32 CODEC(T64, ZSTD(1)),
    MODIFY COLUMN remote_asn REMOVE CODEC,
    MODIFY COLUMN remote_prefix REMOVE CODEC,
    MODIFY COLUMN remote_prefix_len REMOVE CODEC,
    MODIFY COLUMN remote_org_id REMOVE CODEC,
    MODIFY COLUMN remote_country REMOVE CODEC,
    MODIFY COLUMN service_id REMOVE CODEC,
    MODIFY COLUMN classification_method REMOVE CODEC,
    MODIFY COLUMN classification_confidence REMOVE CODEC,
    MODIFY COLUMN category_id REMOVE CODEC,
    MODIFY COLUMN reputation_category REMOVE CODEC,
    MODIFY COLUMN batch_id REMOVE CODEC;
