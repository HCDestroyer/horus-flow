-- Integración de ecosistema con ArgosISP (ADR-0030; contrato docs/integrations/argosisp.md §2.2–§2.4):
--   * devices.integration_argosisp: conector por ISP (URL base, token atk_ y secretos del webhook cifrados con
--     la KEK de devices, estado de la última sincronización y espera exponencial ante ArgosISP caído);
--   * devices.customer_external_identity: identidad del cliente en ArgosISP CON VIGENCIA (valid_from/valid_to;
--     NULL = vigente), porque en PPPoE la misma IP cambia de dueño; una sola vigente por cliente y sistema;
--   * devices.customer_external_sync: última resolución por cliente (barrido de identidades de más de 6 h);
--   * devices.argosisp_webhook_inbox: deduplicación de webhooks por X-Webhook-Id (se purga a los 7 días).
-- alias_source admite 'argosisp' (un alias 'manual' nunca lo pisa ArgosISP; lo decide la aplicación). Todo se
-- purga con el cliente (ON DELETE CASCADE). RLS fail-closed como el resto del esquema. Tablas nuevas y vacías:
-- índices en la misma transacción; el CHECK de customer se añade NOT VALID y se valida después.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

ALTER TABLE devices.customer DROP CONSTRAINT IF EXISTS customer_alias_source_check;
ALTER TABLE devices.customer ADD CONSTRAINT ck_customer__alias_source
  CHECK (alias_source IN ('manual', 'routeros_ppp', 'argosisp')) NOT VALID;
ALTER TABLE devices.customer VALIDATE CONSTRAINT ck_customer__alias_source;

CREATE TABLE devices.integration_argosisp (
  id                     uuid NOT NULL PRIMARY KEY,
  tenant_id              uuid NOT NULL UNIQUE,
  base_url               text NOT NULL CONSTRAINT ck_integration_argosisp__url CHECK (char_length(base_url) BETWEEN 8 AND 2048),
  enabled                boolean NOT NULL DEFAULT false,
  token_ciphertext       bytea,
  token_dek              bytea,
  secret_ciphertext      bytea,
  secret_dek             bytea,
  prev_secret_ciphertext bytea,
  prev_secret_dek        bytea,
  prev_secret_until      timestamptz,
  kek_id                 text,
  resolver               text CONSTRAINT ck_integration_argosisp__resolver CHECK (resolver IN ('batch', 'fallback')),
  last_sync_at           timestamptz,
  last_sync_status       text CONSTRAINT ck_integration_argosisp__status CHECK (last_sync_status IN ('ok', 'degraded', 'failing')),
  last_error             text CONSTRAINT ck_integration_argosisp__error CHECK (char_length(last_error) <= 300),
  failures               integer NOT NULL DEFAULT 0,
  backoff_until          timestamptz,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  version                integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CONSTRAINT ck_integration_argosisp__token CHECK ((token_ciphertext IS NULL) = (token_dek IS NULL))
);

CREATE TABLE devices.customer_external_identity (
  id                   uuid NOT NULL PRIMARY KEY,
  tenant_id            uuid NOT NULL,
  customer_id          uuid NOT NULL,
  system               text NOT NULL DEFAULT 'argosisp' CONSTRAINT ck_ext_identity__system CHECK (system IN ('argosisp')),
  external_customer_id bigint,
  external_service_id  bigint,
  display_name         text CONSTRAINT ck_ext_identity__name CHECK (char_length(display_name) <= 120),
  code                 text CONSTRAINT ck_ext_identity__code CHECK (char_length(code) <= 120),
  service_state        text CONSTRAINT ck_ext_identity__state CHECK (char_length(service_state) <= 120),
  plan                 text CONSTRAINT ck_ext_identity__plan CHECK (char_length(plan) <= 120),
  valid_from           timestamptz NOT NULL,
  valid_to             timestamptz,
  synced_at            timestamptz NOT NULL,
  stale                boolean NOT NULL DEFAULT false,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES devices.customer (tenant_id, id) ON DELETE CASCADE,
  CONSTRAINT ck_ext_identity__validity CHECK (valid_to IS NULL OR valid_to >= valid_from)
);
CREATE UNIQUE INDEX ux_ext_identity__current ON devices.customer_external_identity (tenant_id, customer_id, system) WHERE valid_to IS NULL;
CREATE INDEX ix_ext_identity__customer ON devices.customer_external_identity (tenant_id, customer_id, valid_from DESC);
CREATE INDEX ix_ext_identity__service ON devices.customer_external_identity (tenant_id, system, external_service_id, valid_from);
CREATE INDEX ix_ext_identity__ext_customer ON devices.customer_external_identity (tenant_id, system, external_customer_id)
  WHERE valid_to IS NULL;

CREATE TABLE devices.customer_external_sync (
  tenant_id   uuid NOT NULL,
  customer_id uuid NOT NULL,
  system      text NOT NULL DEFAULT 'argosisp' CONSTRAINT ck_ext_sync__system CHECK (system IN ('argosisp')),
  checked_at  timestamptz NOT NULL,
  status      text NOT NULL CONSTRAINT ck_ext_sync__status CHECK (status IN ('found', 'not_found', 'error')),
  PRIMARY KEY (tenant_id, customer_id, system),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES devices.customer (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX ix_ext_sync__checked ON devices.customer_external_sync (tenant_id, system, checked_at);

CREATE TABLE devices.argosisp_webhook_inbox (
  tenant_id    uuid NOT NULL,
  webhook_id   text NOT NULL CONSTRAINT ck_webhook_inbox__id CHECK (char_length(webhook_id) BETWEEN 1 AND 128),
  event        text,
  payload      jsonb NOT NULL,
  received_at  timestamptz NOT NULL DEFAULT now(),
  processed_at timestamptz,
  attempts     integer NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, webhook_id)
);
CREATE INDEX ix_webhook_inbox__pending ON devices.argosisp_webhook_inbox (received_at) WHERE processed_at IS NULL;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'devices.integration_argosisp', 'devices.customer_external_identity', 'devices.customer_external_sync',
    'devices.argosisp_webhook_inbox'
  ] LOOP
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY p_tenant ON %s USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE, DELETE ON devices.integration_argosisp, devices.customer_external_identity,
  devices.customer_external_sync, devices.argosisp_webhook_inbox TO devices_app, devices_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS devices.argosisp_webhook_inbox;
DROP TABLE IF EXISTS devices.customer_external_sync;
DROP TABLE IF EXISTS devices.customer_external_identity;
DROP TABLE IF EXISTS devices.integration_argosisp;
UPDATE devices.customer SET alias = NULL, alias_source = NULL WHERE alias_source = 'argosisp';
ALTER TABLE devices.customer DROP CONSTRAINT IF EXISTS ck_customer__alias_source;
ALTER TABLE devices.customer ADD CONSTRAINT customer_alias_source_check CHECK (alias_source IN ('manual', 'routeros_ppp'));
