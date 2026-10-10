-- D23 · Registro de eventos de plataforma: arranques/paradas por rol con versión, caídas detectadas, migraciones,
-- degradaciones, fallos de envío de alertas y cambios de configuración. Datos de plataforma (sin datos de clientes):
-- lo escribe cada proceso `horus` con el usuario de la aplicación y lo lee el superadministrador por
-- GET /api/v1/platform/events. Retención por borrado periódico (HORUS_PLATFORM_EVENTS_RETENTION, 90 días).

-- +goose Up
SET LOCAL lock_timeout = '5s';

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

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS platform_events.event;
