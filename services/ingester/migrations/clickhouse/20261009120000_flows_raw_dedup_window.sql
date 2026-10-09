-- I1-04 · Deduplicación de inserts reentregados en flows.flows_raw (docs/events.md §5.5: el
-- ingester inserta cada lote con insert_deduplication_token = batch_id). En MergeTree no
-- replicado ClickHouse solo deduplica si non_replicated_deduplication_window > 0; sin este ajuste
-- un lote reentregado por JetStream fuera de su ventana de 2 min duplicaría filas (y agregados:
-- un bloque deduplicado tampoco llega a las vistas materializadas). 10 000 bloques cubren de sobra
-- la retención de TLM_FLOWS a la tasa nominal de I1. No cambia columnas ni el contrato C3.

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE flows.flows_raw MODIFY SETTING non_replicated_deduplication_window = 10000;

-- +goose Down
ALTER TABLE flows.flows_raw MODIFY SETTING non_replicated_deduplication_window = 0;
