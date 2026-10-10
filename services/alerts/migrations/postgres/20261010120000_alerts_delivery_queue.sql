-- D23 · Cola persistente de entregas: el consumidor alerts-notify solo encola (con el mensaje ya renderizado,
-- sin datos personales) y un despachador la vacía con reintentos y backoff. Una entrega en curso se reclama con
-- un plazo (claimed_until): si el proceso muere a mitad, otra pasada (o réplica) la retoma al vencer el plazo.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE alerts.notification_delivery
  ADD COLUMN attempts        integer NOT NULL DEFAULT 0,
  ADD COLUMN next_attempt_at timestamptz,
  ADD COLUMN claimed_until   timestamptz,
  ADD COLUMN message         jsonb,
  ADD COLUMN severity        text,
  ADD COLUMN trace_parent    text;

-- Pendientes por vencimiento (el despachador ordena por created_at dentro de las vencidas).
CREATE INDEX ix_delivery__due ON alerts.notification_delivery (coalesce(next_attempt_at, created_at)) WHERE status = 'queued';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP INDEX IF EXISTS alerts.ix_delivery__due;
ALTER TABLE alerts.notification_delivery
  DROP COLUMN IF EXISTS trace_parent,
  DROP COLUMN IF EXISTS severity,
  DROP COLUMN IF EXISTS message,
  DROP COLUMN IF EXISTS claimed_until,
  DROP COLUMN IF EXISTS next_attempt_at,
  DROP COLUMN IF EXISTS attempts;
