-- FLOW · isp10k · Recompresión por antigüedad de los agregados (TTL RECOMPRESS), sin cambiar la retención del
-- contrato C3: cada tabla conserva su TTL DELETE y añade un RECOMPRESS CODEC(ZSTD(6)) cuando el bucket ya está cerrado
-- (1–3 días). Las columnas de los agregados no tienen códec explícito (LZ4 por defecto), así que la recompresión les
-- afecta a todas. Medido con datos isp10k (tests/load/REPORT.md, «Compresión»): customer_* 19,6–20,2 → 11,7–11,9 B/fila,
-- client_port_1m 18,5 → 11,2, client_security_1m 105 → 79, client_security_1h 241 → 210, site_* −70 %. ZSTD(9) solo
-- gana un 2 % más con 2–3× más CPU. En flows_raw no se usa: sus columnas llevan códec explícito (20261010130000).
-- materialize_ttl_after_modify = 0: no se reescriben las partes existentes al migrar (se recomprimen al fusionarse).

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE flows.customer_5m MODIFY TTL bucket + INTERVAL 2 DAY RECOMPRESS CODEC(ZSTD(6)), bucket + INTERVAL 90 DAY DELETE
SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.customer_1h MODIFY TTL bucket + INTERVAL 2 DAY RECOMPRESS CODEC(ZSTD(6)), bucket + INTERVAL 13 MONTH DELETE
SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.customer_1d MODIFY TTL bucket + INTERVAL 3 DAY RECOMPRESS CODEC(ZSTD(6)), bucket + INTERVAL 25 MONTH DELETE
SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.site_5m MODIFY TTL bucket + INTERVAL 2 DAY RECOMPRESS CODEC(ZSTD(6)), bucket + INTERVAL 90 DAY DELETE
SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.site_1h MODIFY TTL bucket + INTERVAL 2 DAY RECOMPRESS CODEC(ZSTD(6)), bucket + INTERVAL 13 MONTH DELETE
SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.site_1d MODIFY TTL bucket + INTERVAL 3 DAY RECOMPRESS CODEC(ZSTD(6)), bucket + INTERVAL 5 YEAR DELETE
SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.unattributed_1h MODIFY TTL bucket + INTERVAL 2 DAY RECOMPRESS CODEC(ZSTD(6)), bucket + INTERVAL 30 DAY DELETE
SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.client_security_1m MODIFY TTL bucket + INTERVAL 1 DAY RECOMPRESS CODEC(ZSTD(6)), bucket + INTERVAL 7 DAY DELETE
SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.client_security_1h MODIFY TTL bucket + INTERVAL 2 DAY RECOMPRESS CODEC(ZSTD(6)), bucket + INTERVAL 13 MONTH DELETE
SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.client_port_1m MODIFY TTL bucket + INTERVAL 1 DAY RECOMPRESS CODEC(ZSTD(6)), bucket + INTERVAL 7 DAY DELETE
SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.reputation_hit MODIFY TTL bucket_1h + INTERVAL 2 DAY RECOMPRESS CODEC(ZSTD(6)), bucket_1h + INTERVAL 13 MONTH DELETE
SETTINGS materialize_ttl_after_modify = 0;

-- +goose Down
ALTER TABLE flows.customer_5m MODIFY TTL bucket + INTERVAL 90 DAY DELETE SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.customer_1h MODIFY TTL bucket + INTERVAL 13 MONTH DELETE SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.customer_1d MODIFY TTL bucket + INTERVAL 25 MONTH DELETE SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.site_5m MODIFY TTL bucket + INTERVAL 90 DAY DELETE SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.site_1h MODIFY TTL bucket + INTERVAL 13 MONTH DELETE SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.site_1d MODIFY TTL bucket + INTERVAL 5 YEAR DELETE SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.unattributed_1h MODIFY TTL bucket + INTERVAL 30 DAY DELETE SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.client_security_1m MODIFY TTL bucket + INTERVAL 7 DAY DELETE SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.client_security_1h MODIFY TTL bucket + INTERVAL 13 MONTH DELETE SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.client_port_1m MODIFY TTL bucket + INTERVAL 7 DAY DELETE SETTINGS materialize_ttl_after_modify = 0;
ALTER TABLE flows.reputation_hit MODIFY TTL bucket_1h + INTERVAL 13 MONTH DELETE SETTINGS materialize_ttl_after_modify = 0;
