-- D17 · Integración con LibreNMS: Horus aprovisiona LibreNMS (dispositivo «Horus Flow», grupos, plantillas y reglas
-- «Horus · ») y le delega el alertado. Se guarda por canal librenms el estado de la integración y los ids de LibreNMS
-- de lo que Horus creó o adoptó (para detectar la deriva y no tocar nunca objetos ajenos), y por router la IP de
-- gestión alcanzable desde LibreNMS y el modo de monitoreo, sin tocar las tablas de devices. Las credenciales SNMP
-- propias para LibreNMS son write-only y van cifradas con la KEK de alerts (envelope, como las del canal). RLS por tenant.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE alerts.librenms_integration (
  channel_id        uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL,
  horus_hostname    text,
  cpu_threshold     integer NOT NULL DEFAULT 85 CHECK (cpu_threshold BETWEEN 50 AND 99),
  memory_threshold  integer NOT NULL DEFAULT 90 CHECK (memory_threshold BETWEEN 50 AND 99),
  tz_offset_minutes integer CHECK (tz_offset_minutes BETWEEN -900 AND 900),
  remote_version    text,
  status            text NOT NULL DEFAULT 'not_provisioned' CHECK (status IN ('not_provisioned', 'ok', 'partial', 'failed')),
  last_sync_at      timestamptz,
  last_error_code   text,
  last_error        text,
  warnings          jsonb NOT NULL DEFAULT '[]',
  last_sync_steps   jsonb NOT NULL DEFAULT '[]',
  last_test         jsonb,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (tenant_id, channel_id) REFERENCES alerts.notification_channel (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX ix_librenms_integration__tenant ON alerts.librenms_integration (tenant_id);

CREATE TABLE alerts.librenms_object (
  tenant_id        uuid NOT NULL,
  channel_id       uuid NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('device', 'router_device', 'infra_device', 'group', 'template', 'rule')),
  key              text NOT NULL,
  name             text NOT NULL,
  remote_id        bigint NOT NULL,
  hostname         text,
  created_by_horus boolean NOT NULL,
  active           boolean NOT NULL DEFAULT true,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, kind, key),
  FOREIGN KEY (tenant_id, channel_id) REFERENCES alerts.notification_channel (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX ix_librenms_object__tenant ON alerts.librenms_object (tenant_id, channel_id);

CREATE TABLE alerts.librenms_router (
  tenant_id          uuid NOT NULL,
  router_id          uuid NOT NULL,
  mgmt_ip            inet NOT NULL,
  mode               text NOT NULL DEFAULT 'ping' CHECK (mode IN ('ping', 'snmp_v2c', 'snmp_v3', 'horus_snmp_v3')),
  secret_ciphertext  bytea,
  dek_wrapped        bytea,
  kek_id             text,
  reuse_consent_at   timestamptz,
  reuse_consent_by   text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, router_id),
  CONSTRAINT ck_librenms_router__secret CHECK ((secret_ciphertext IS NULL) = (dek_wrapped IS NULL)),
  CONSTRAINT ck_librenms_router__consent CHECK (mode <> 'horus_snmp_v3' OR reuse_consent_at IS NOT NULL)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['alerts.librenms_integration', 'alerts.librenms_object', 'alerts.librenms_router'] LOOP
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY p_tenant ON %s USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE, DELETE ON alerts.librenms_integration, alerts.librenms_object, alerts.librenms_router
  TO alerts_app, alerts_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS alerts.librenms_router;
DROP TABLE IF EXISTS alerts.librenms_object;
DROP TABLE IF EXISTS alerts.librenms_integration;
