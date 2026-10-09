-- I1 · Canal mínimo de alertas (D13/D17/D21, dueño CORE): canales de notificación por ISP (email SMTP de la
-- instalación, Telegram y LibreNMS por API), secretos write-only cifrados con la KEK de alerts (envelope:
-- DEK envuelta), registro de entregas idempotente por (canal, evento origen) y outbox. RLS por tenant.

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
    CREATE ROLE alerts_app NOLOGIN;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
  BEGIN
    CREATE ROLE alerts_platform NOLOGIN BYPASSRLS;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
END $$;
-- +goose StatementEnd
GRANT alerts_app, alerts_platform TO CURRENT_USER;

CREATE TABLE alerts.notification_channel (
  id                    uuid PRIMARY KEY,
  tenant_id             uuid NOT NULL,
  name                  text NOT NULL,
  kind                  text NOT NULL CHECK (kind IN ('email', 'telegram', 'librenms')),
  enabled               boolean NOT NULL DEFAULT true,
  config                jsonb NOT NULL,
  subscription          jsonb NOT NULL,
  include_personal_data boolean NOT NULL DEFAULT false,
  status                text NOT NULL DEFAULT 'unverified' CHECK (status IN ('unverified', 'ok', 'failing', 'disabled')),
  secret_ciphertext     bytea,
  dek_wrapped           bytea,
  kek_id                text,
  last_delivery_at      timestamptz,
  last_error            text,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  version               integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CONSTRAINT ck_channel__secret CHECK ((secret_ciphertext IS NULL) = (dek_wrapped IS NULL))
);
CREATE INDEX ix_channel__tenant ON alerts.notification_channel (tenant_id, created_at, id);

CREATE TABLE alerts.notification_delivery (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL,
  channel_id        uuid NOT NULL,
  channel_kind      text NOT NULL,
  status            text NOT NULL CHECK (status IN ('queued', 'sent', 'failed', 'throttled')),
  event_type        text,
  source_event_type text,
  source_event_id   uuid,
  resource_id       text,
  is_test           boolean NOT NULL DEFAULT false,
  error             text,
  created_at        timestamptz NOT NULL DEFAULT now(),
  sent_at           timestamptz,
  FOREIGN KEY (tenant_id, channel_id) REFERENCES alerts.notification_channel (tenant_id, id) ON DELETE CASCADE
);
-- Idempotencia del consumidor: una entrega por canal y evento de origen (events.md §7).
CREATE UNIQUE INDEX ux_delivery__channel_event ON alerts.notification_delivery (channel_id, source_event_id) WHERE source_event_id IS NOT NULL;
CREATE INDEX ix_delivery__tenant ON alerts.notification_delivery (tenant_id, created_at DESC, id DESC);
CREATE INDEX ix_delivery__throttle ON alerts.notification_delivery (channel_id, event_type, resource_id, created_at DESC);

CREATE TABLE alerts.outbox (
  id uuid PRIMARY KEY, seq bigint GENERATED ALWAYS AS IDENTITY, tenant_id uuid, subject text NOT NULL,
  aggregate_id uuid NOT NULL, headers jsonb NOT NULL DEFAULT '{}', payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL, published_at timestamptz, attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX ix_outbox__pending ON alerts.outbox (seq) WHERE published_at IS NULL;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['alerts.notification_channel', 'alerts.notification_delivery'] LOOP
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY p_tenant ON %s USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA alerts TO alerts_app, alerts_platform;
GRANT SELECT, INSERT, UPDATE, DELETE ON alerts.notification_channel, alerts.notification_delivery, alerts.outbox TO alerts_app, alerts_platform;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA alerts TO alerts_app, alerts_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS alerts.outbox;
DROP TABLE IF EXISTS alerts.notification_delivery;
DROP TABLE IF EXISTS alerts.notification_channel;
REVOKE ALL ON SCHEMA alerts FROM alerts_app, alerts_platform;
