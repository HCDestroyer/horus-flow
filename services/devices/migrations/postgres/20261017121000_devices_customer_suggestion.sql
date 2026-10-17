-- I2 · Sugerencia del scoring residencial/comercial en la ficha del cliente (docs/api.md §2.9:
-- suggested_kind, suggested_confidence y razones del último scoring; docs/events.md §8.8). Las
-- proyecta el consumidor devices-customer-detection desde horus.detection.customer.kind_suggested,
-- también en clientes con el tipo fijado a mano (kind_locked): el scoring sigue sugiriendo aunque no
-- cambie el tipo. commercial_score ya existía (residencial con indicios comerciales).
-- La tabla puede ser grande: columnas anulables sin valor por defecto (ALTER instantáneo), CHECK
-- añadidos NOT VALID y validados después (sin bloqueo exclusivo durante el recorrido) e índice parcial
-- para la lista de "posibles comerciales" (GET /customers?commercial_candidate=true) creado fuera de
-- transacción con CONCURRENTLY (sin límite de tiempo de sentencia: depende del tamaño).

-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

ALTER TABLE devices.customer
  ADD COLUMN IF NOT EXISTS suggested_kind       text,
  ADD COLUMN IF NOT EXISTS suggested_confidence smallint,
  ADD COLUMN IF NOT EXISTS suggested_reasons    jsonb,
  ADD COLUMN IF NOT EXISTS suggested_model      text,
  ADD COLUMN IF NOT EXISTS suggested_at         timestamptz;

ALTER TABLE devices.customer ADD CONSTRAINT ck_customer__suggested_kind
  CHECK (suggested_kind IN ('residential', 'commercial', 'unknown')) NOT VALID;
ALTER TABLE devices.customer ADD CONSTRAINT ck_customer__suggested_confidence
  CHECK (suggested_confidence BETWEEN 0 AND 100) NOT VALID;

SET statement_timeout = 0;
ALTER TABLE devices.customer VALIDATE CONSTRAINT ck_customer__suggested_kind;
ALTER TABLE devices.customer VALIDATE CONSTRAINT ck_customer__suggested_confidence;

CREATE INDEX CONCURRENTLY IF NOT EXISTS ix_customer__commercial_candidate ON devices.customer (tenant_id, last_seen DESC)
  WHERE NOT kind_locked AND (suggested_kind = 'commercial' OR commercial_use_suspected);

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS devices.ix_customer__commercial_candidate;
ALTER TABLE devices.customer
  DROP CONSTRAINT IF EXISTS ck_customer__suggested_confidence,
  DROP CONSTRAINT IF EXISTS ck_customer__suggested_kind,
  DROP COLUMN IF EXISTS suggested_at,
  DROP COLUMN IF EXISTS suggested_model,
  DROP COLUMN IF EXISTS suggested_reasons,
  DROP COLUMN IF EXISTS suggested_confidence,
  DROP COLUMN IF EXISTS suggested_kind;
RESET lock_timeout;
