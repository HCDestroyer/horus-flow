-- I3 · Reportes programados (rol reporting, FLOW; services/analytics/reporting): definición por ISP y tipo
-- (programación semanal, formatos, destinatarios del aviso) y ejecuciones con sus archivos en el almacén
-- local (reports/<tenant_id>/, ADR-0019). La verdad de la cola de trabajo es report_run (estado queued →
-- running → completed | failed; el worker reclama con FOR UPDATE SKIP LOCKED). Idempotencia: una ejecución
-- programada por (definición, periodo) y una a petición por (tenant, usuario, Idempotency-Key).
-- RLS fail-closed como el resto del esquema; el planificador y el worker usan analytics_platform.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL client_min_messages = warning;

CREATE TABLE analytics.report_definition (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  kind        text NOT NULL CHECK (kind ~ '^[a-z][a-z0-9_]{0,47}$'),
  enabled     boolean NOT NULL DEFAULT true,
  weekday     smallint NOT NULL DEFAULT 1 CHECK (weekday BETWEEN 1 AND 7), -- ISO: 1 = lunes
  hour        smallint NOT NULL DEFAULT 7 CHECK (hour BETWEEN 0 AND 23),
  minute      smallint NOT NULL DEFAULT 0 CHECK (minute BETWEEN 0 AND 59),
  timezone    text NOT NULL DEFAULT 'UTC' CHECK (char_length(timezone) BETWEEN 1 AND 64),
  formats     text[] NOT NULL DEFAULT '{pdf,csv}' CHECK (formats <@ ARRAY['pdf', 'csv'] AND cardinality(formats) >= 1),
  recipients  text[] NOT NULL DEFAULT '{}' CHECK (cardinality(recipients) <= 20),
  -- Copia del directorio de auth (portada del PDF y enlace del aviso cuando auth no está en el proceso).
  tenant_name text,
  tenant_slug text,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  version     integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, kind)
);

CREATE TABLE analytics.report_run (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  definition_id   uuid,
  kind            text NOT NULL,
  status          text NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed')),
  period_from     timestamptz NOT NULL,
  period_to       timestamptz NOT NULL,
  timezone        text NOT NULL,
  formats         text[] NOT NULL CHECK (formats <@ ARRAY['pdf', 'csv'] AND cardinality(formats) >= 1),
  -- [{format, object_key, sha256, size_bytes}]; object_key relativo a HORUS_DATA_DIR.
  files           jsonb NOT NULL DEFAULT '[]',
  error           text,
  created_by      uuid,
  idempotency_key uuid,
  attempts        integer NOT NULL DEFAULT 0,
  claimed_until   timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  started_at      timestamptz,
  completed_at    timestamptz,
  expires_at      timestamptz,
  purged_at       timestamptz,
  UNIQUE (tenant_id, id),
  CONSTRAINT ck_report_run__period CHECK (period_to > period_from),
  FOREIGN KEY (tenant_id, definition_id) REFERENCES analytics.report_definition (tenant_id, id) ON DELETE SET NULL (definition_id)
);
-- Una ejecución programada por definición y periodo (varias réplicas del planificador: ON CONFLICT DO NOTHING).
CREATE UNIQUE INDEX ux_report_run__scheduled ON analytics.report_run (definition_id, period_from) WHERE definition_id IS NOT NULL;
CREATE UNIQUE INDEX ux_report_run__idempotency ON analytics.report_run (tenant_id, created_by, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
CREATE INDEX ix_report_run__tenant ON analytics.report_run (tenant_id, created_at DESC, id DESC);
CREATE INDEX ix_report_run__queue ON analytics.report_run (created_at) WHERE status IN ('queued', 'running');
CREATE INDEX ix_report_run__expiry ON analytics.report_run (expires_at) WHERE purged_at IS NULL AND expires_at IS NOT NULL;

ALTER TABLE analytics.report_definition ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics.report_definition FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON analytics.report_definition USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
ALTER TABLE analytics.report_run ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics.report_run FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON analytics.report_run USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT SELECT, INSERT, UPDATE ON analytics.report_definition, analytics.report_run TO analytics_app, analytics_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS analytics.report_run;
DROP TABLE IF EXISTS analytics.report_definition;
