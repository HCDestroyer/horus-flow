-- D23 · Registro de eventos de plataforma: arranques/paradas por rol con versión, caídas detectadas, migraciones,
-- degradaciones, fallos de envío de alertas y cambios de configuración. Datos de plataforma (sin datos de clientes):
-- lo escribe cada proceso `horus` con el usuario de la aplicación y lo lee el superadministrador por
-- GET /api/v1/platform/events. Retención por borrado periódico (HORUS_PLATFORM_EVENTS_RETENTION, 90 días).

-- Aunque es de plataforma, la tabla lleva tenant_id (eventos de un ISP, p. ej. alert_delivery_failed): RLS
-- forzada como todas las tablas con tenant_id (security.md §3.9); el proceso escribe y el superadministrador lee
-- con el rol platform_events_platform (BYPASSRLS), igual que los métodos de plataforma de cada módulo.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL client_min_messages = warning;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.horus_current_tenant() RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT nullif(current_setting('horus.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    CREATE ROLE platform_events_app NOLOGIN;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
  BEGIN
    CREATE ROLE platform_events_platform NOLOGIN BYPASSRLS;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
END $$;
-- +goose StatementEnd
GRANT platform_events_app, platform_events_platform TO CURRENT_USER;

CREATE TABLE platform_events.event (
  id          uuid PRIMARY KEY,
  occurred_at timestamptz NOT NULL,
  recorded_at timestamptz NOT NULL DEFAULT now(),
  kind        text NOT NULL CHECK (kind ~ '^[a-z_]{3,40}$'),
  severity    text NOT NULL CHECK (severity IN ('info', 'warn', 'error')),
  process     text NOT NULL,
  instance    text NOT NULL DEFAULT '',
  role        text NOT NULL DEFAULT '',
  version     text NOT NULL DEFAULT '',
  tenant_id   uuid,
  message     text NOT NULL,
  details     jsonb NOT NULL DEFAULT '{}',
  trace_id    text NOT NULL DEFAULT ''
);
CREATE INDEX ix_event__time ON platform_events.event (occurred_at DESC, id DESC);
CREATE INDEX ix_event__kind ON platform_events.event (kind, occurred_at DESC);
-- Detección de paradas no limpias: último process_started/process_stopped de cada proceso e instancia.
CREATE INDEX ix_event__lifecycle ON platform_events.event (process, instance, occurred_at DESC)
  WHERE kind IN ('process_started', 'process_stopped');

ALTER TABLE platform_events.event ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_events.event FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON platform_events.event USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
GRANT USAGE ON SCHEMA platform_events TO platform_events_app, platform_events_platform;
GRANT SELECT, INSERT, DELETE ON platform_events.event TO platform_events_app, platform_events_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS platform_events.event;
REVOKE ALL ON SCHEMA platform_events FROM platform_events_app, platform_events_platform;
