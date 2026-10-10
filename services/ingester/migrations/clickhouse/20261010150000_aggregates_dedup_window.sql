-- FLOW · D23 · Deduplicación de los agregados ante un INSERT interrumpido.
-- La matriz de reinicio (make chaos-restart-flows) encontró agregados por cliente por encima de
-- flows_raw tras un kill -9 de horus-app: si el ingester muere a mitad de un INSERT, ClickHouse
-- puede haber escrito ya los bloques de las vistas materializadas sin confirmar el de flows_raw; el
-- reintento (mismo insert_deduplication_token, ledger de grupos) inserta flows_raw y vuelve a
-- empujar las vistas: los agregados cuentan dos veces ese grupo. El ingester inserta ahora con
-- deduplicate_blocks_in_dependent_materialized_views = 1, que deduplica también los bloques de las
-- vistas por el token; en MergeTree no replicado eso exige una ventana de deduplicación en cada
-- tabla destino (la misma que flows_raw, 20261009120000). No cambia columnas ni el contrato C3.

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE flows.customer_5m MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE flows.customer_1h MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE flows.customer_1d MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE flows.site_5m MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE flows.site_1h MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE flows.site_1d MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE flows.unattributed_1h MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE flows.client_security_1m MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE flows.client_security_1h MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE flows.client_port_1m MODIFY SETTING non_replicated_deduplication_window = 10000;
ALTER TABLE flows.reputation_hit MODIFY SETTING non_replicated_deduplication_window = 10000;

-- +goose Down
ALTER TABLE flows.customer_5m MODIFY SETTING non_replicated_deduplication_window = 0;
ALTER TABLE flows.customer_1h MODIFY SETTING non_replicated_deduplication_window = 0;
ALTER TABLE flows.customer_1d MODIFY SETTING non_replicated_deduplication_window = 0;
ALTER TABLE flows.site_5m MODIFY SETTING non_replicated_deduplication_window = 0;
ALTER TABLE flows.site_1h MODIFY SETTING non_replicated_deduplication_window = 0;
ALTER TABLE flows.site_1d MODIFY SETTING non_replicated_deduplication_window = 0;
ALTER TABLE flows.unattributed_1h MODIFY SETTING non_replicated_deduplication_window = 0;
ALTER TABLE flows.client_security_1m MODIFY SETTING non_replicated_deduplication_window = 0;
ALTER TABLE flows.client_security_1h MODIFY SETTING non_replicated_deduplication_window = 0;
ALTER TABLE flows.client_port_1m MODIFY SETTING non_replicated_deduplication_window = 0;
ALTER TABLE flows.reputation_hit MODIFY SETTING non_replicated_deduplication_window = 0;
