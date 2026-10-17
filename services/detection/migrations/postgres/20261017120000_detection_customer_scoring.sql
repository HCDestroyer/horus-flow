-- I2 · Scoring residencial/comercial explicable (docs/database.md "Propuesta del scoring",
-- docs/events.md §8.8). Último scoring por cliente (modelo, puntuación, características con valor y
-- peso, razones y ventana) y estado de la histéresis: nivel publicado (último
-- horus.detection.customer.kind_suggested) y nivel candidato pendiente desde pending_since. La serie
-- diaria vive en ClickHouse (detection.customer_scores_1d). address es dato personal (IP del cliente):
-- solo la API la usa, nunca va en eventos. RLS fail-closed como el resto del esquema. Tabla nueva y
-- vacía: índices en la misma transacción.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE detection.customer_scoring (
  tenant_id        uuid NOT NULL,
  customer_id      uuid NOT NULL,
  realm_id         uuid NOT NULL,
  site_id          uuid NOT NULL,
  address          inet NOT NULL,
  model_version    text NOT NULL,
  computed_at      timestamptz NOT NULL,
  window_from      timestamptz NOT NULL,
  window_to        timestamptz NOT NULL,
  commercial_score smallint NOT NULL CHECK (commercial_score BETWEEN 0 AND 100),
  raw_score        smallint NOT NULL CHECK (raw_score BETWEEN 0 AND 100),
  level            text NOT NULL CHECK (level IN ('residential', 'suspected', 'commercial')),
  confidence       double precision NOT NULL CHECK (confidence BETWEEN 0 AND 1),
  dynamic_pool     boolean NOT NULL DEFAULT false,
  customer_kind    text NOT NULL DEFAULT 'residential',
  kind_locked      boolean NOT NULL DEFAULT false,
  features         jsonb NOT NULL DEFAULT '[]',
  reasons          jsonb NOT NULL DEFAULT '[]',
  published_level  text CHECK (published_level IN ('residential', 'suspected', 'commercial')),
  published_at     timestamptz,
  pending_level    text CHECK (pending_level IN ('residential', 'suspected', 'commercial')),
  pending_since    timestamptz,
  version          integer NOT NULL DEFAULT 1,
  updated_at       timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, customer_id),
  CONSTRAINT ck_customer_scoring__pending CHECK ((pending_level IS NULL) = (pending_since IS NULL))
);
CREATE INDEX ix_customer_scoring__level ON detection.customer_scoring (tenant_id, level, commercial_score DESC);

ALTER TABLE detection.customer_scoring ENABLE ROW LEVEL SECURITY;
ALTER TABLE detection.customer_scoring FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON detection.customer_scoring
  USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON detection.customer_scoring TO detection_app, detection_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS detection.customer_scoring;
