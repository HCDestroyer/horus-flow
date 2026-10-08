-- I0-13 · Cobertura (bytes de flujos vs uplink por SNMP; traffic-model.md §4.4.2). Escritor: jobs (I2).

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS flows.flows_coverage
(
    tenant_id UUID, site_id UUID, router_id UUID, bucket DateTime('UTC'),
    flow_bytes UInt64, snmp_bytes Nullable(UInt64), coverage_ratio Nullable(Float32)
)
ENGINE = ReplacingMergeTree PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, site_id, router_id, bucket)
TTL bucket + INTERVAL 13 MONTH DELETE;

-- +goose Down
DROP TABLE IF EXISTS flows.flows_coverage;
