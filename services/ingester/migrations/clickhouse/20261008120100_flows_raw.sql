-- I0-13 · Flujos crudos enriquecidos en ingesta (contrato C3, docs/database.md §6.1,
-- docs/traffic-model.md §3). Escritor único: rol ingester. Pre-NAT (D12): client_ip es la IP
-- privada del cliente en subida y bajada cuando el NAT está en el router principal.
-- pii: client_ip y remote_ip son datos personales; retención 7 d (storage.md §5).

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS flows.flows_raw
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

-- +goose Down
DROP TABLE IF EXISTS flows.flows_raw;
