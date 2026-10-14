-- Vinculación de chats de Telegram con el bot compartido de la instalación (HORUS_TELEGRAM_BOT_TOKEN): las
-- actualizaciones de ese bot llegan mezcladas de todos los ISP, así que «Detectar chat» exige un código de un solo
-- uso por tenant. Quien configura el canal envía `/start <código>` al bot (o publica el código en el canal) y solo
-- se devuelven los chats cuyo mensaje contiene ese código. El chat encontrado se guarda en la fila del código
-- (cualquier ISP que lea las actualizaciones las reparte por código, rol de plataforma); caduca a los 15 min.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE alerts.telegram_link_code (
  code          text PRIMARY KEY CHECK (code ~ '^hf[a-z0-9]{10}$'),
  tenant_id     uuid NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  chat_id       text,
  chat_type     text,
  chat_title    text,
  chat_username text,
  matched_at    timestamptz
);
CREATE INDEX ix_telegram_link_code__tenant ON alerts.telegram_link_code (tenant_id, created_at DESC);

ALTER TABLE alerts.telegram_link_code ENABLE ROW LEVEL SECURITY;
ALTER TABLE alerts.telegram_link_code FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON alerts.telegram_link_code USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON alerts.telegram_link_code TO alerts_app, alerts_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS alerts.telegram_link_code;
