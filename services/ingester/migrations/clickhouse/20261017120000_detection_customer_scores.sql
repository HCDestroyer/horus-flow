-- I2 · Serie diaria del scoring residencial/comercial (docs/database.md §6.4: detection.customer_scores_1d;
-- fuera del contrato C3 v0, que la delega en I2). Una fila por cliente, día y versión de modelo:
-- ReplacingMergeTree(computed_at), repetir el día sustituye la fila. Escritor único: el rol detection
-- (usuario horus_detection, rol horus_detection_role). Lectores: los de tenant con la política p_tenant
-- (SQL_horus_tenant, fail-closed) y los procesos de plataforma con p_platform; jobs purga por tenant.
-- security_score y anomaly_score quedan reservados (NULL) hasta que exista su modelo.
-- La crea el rol ingester, dueño del esquema ClickHouse de flows (roles y usuarios en 20261008120500).

-- +goose NO TRANSACTION
-- +goose Up
CREATE DATABASE IF NOT EXISTS detection;

CREATE ROLE IF NOT EXISTS horus_tenant_reader;
CREATE ROLE IF NOT EXISTS horus_platform_reader;
CREATE ROLE IF NOT EXISTS horus_detection_role;
CREATE ROLE IF NOT EXISTS horus_jobs_role;

CREATE TABLE IF NOT EXISTS detection.customer_scores_1d
(
    tenant_id UUID, realm_id UUID, client_ip IPv6, day Date, model_version LowCardinality(String),
    customer_id UUID, site_id UUID,
    commercial_score UInt8, raw_score UInt8, confidence Float32,
    security_score Nullable(UInt8), anomaly_score Nullable(UInt8),
    kind_on_day LowCardinality(String), suggested_kind LowCardinality(String), level LowCardinality(String),
    dynamic_pool UInt8, reasons Array(LowCardinality(String)),
    computed_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(computed_at) PARTITION BY toYYYYMM(day)
ORDER BY (tenant_id, realm_id, client_ip, day, model_version)
TTL day + INTERVAL 25 MONTH DELETE;

GRANT INSERT ON detection.customer_scores_1d TO horus_detection_role;
GRANT SELECT ON detection.customer_scores_1d TO horus_tenant_reader;
GRANT SELECT ON detection.customer_scores_1d TO horus_platform_reader;
GRANT ALTER DELETE ON detection.* TO horus_jobs_role;

CREATE ROW POLICY IF NOT EXISTS p_tenant ON detection.customer_scores_1d AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_platform ON detection.customer_scores_1d AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader;

-- +goose Down
DROP ROW POLICY IF EXISTS p_platform ON detection.customer_scores_1d;
DROP ROW POLICY IF EXISTS p_tenant ON detection.customer_scores_1d;
DROP TABLE IF EXISTS detection.customer_scores_1d;
