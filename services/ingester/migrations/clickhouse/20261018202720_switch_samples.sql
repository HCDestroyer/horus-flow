-- FLOW I6 · Muestras sFlow de switches = fuente SECUNDARIA (docs/traffic-model.md §13). Un switch con sFlow ve
-- otra vez el mismo tráfico que el router principal ya exporta: si sus muestras entraran en flows.flows_raw se
-- contaría dos veces en clientes y nodos y podrían crear clientes. Por eso:
--   * el collector publica sus lotes en horus.telemetry.flows.secondary.<switch_id>, que el durable flows-ingester
--     (filtro horus.telemetry.flows.batch.>) nunca recibe;
--   * el durable flows-switch-samples los escribe AQUÍ, en una tabla aparte que no es origen de ninguna vista
--     materializada (customer_*, site_*, attribution_5m, security…): los agregados existentes no cambian;
--   * sin atribución ni enriquecimiento: solo el top de IP/protocolos/puertos por puerto del switch en su ficha
--     (lo lee el rol snmp con horus_tenant_reader y la política p_tenant).
-- bytes/packets ya multiplicados por la tasa de muestreo. Retención corta (7 días): es diagnóstico, no consumo.
-- Tabla nueva y aditiva (se añade a packages/schemas/datastore/v0/clickhouse-contract-v0.sql).

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS flows.switch_samples
(
    tenant_id UUID, site_id UUID, switch_id UUID, ts DateTime('UTC'),
    input_if_index UInt32, output_if_index UInt32,
    src_ip IPv6, dst_ip IPv6, protocol UInt8, src_port UInt16, dst_port UInt16,
    bytes UInt64, packets UInt64, sampling_rate UInt32, batch_id UUID
)
ENGINE = MergeTree PARTITION BY toYYYYMMDD(ts)
ORDER BY (tenant_id, switch_id, input_if_index, ts)
TTL ts + INTERVAL 7 DAY DELETE
SETTINGS non_replicated_deduplication_window = 10000;

CREATE ROLE IF NOT EXISTS horus_ingester_role;
CREATE ROLE IF NOT EXISTS horus_tenant_reader;
CREATE ROLE IF NOT EXISTS horus_platform_reader;
GRANT INSERT ON flows.switch_samples TO horus_ingester_role;
GRANT SELECT ON flows.switch_samples TO horus_tenant_reader;
GRANT SELECT ON flows.switch_samples TO horus_platform_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.switch_samples AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.switch_samples AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;

-- +goose Down
DROP ROW POLICY IF EXISTS p_platform ON flows.switch_samples;
DROP ROW POLICY IF EXISTS p_tenant ON flows.switch_samples;
DROP TABLE IF EXISTS flows.switch_samples;
