-- Enlace de conexión con ArgosISP (contrato docs/integrations/argosisp.md §1.4, H10; ADR-0030):
--   * devices.argosisp_connection_link: código de un solo uso (32 B aleatorios; solo su SHA-256) que genera un
--     administrador, con caducidad de 15 min. El canje público lo marca usado (CAS) y guarda el hash de la petición
--     y la respuesta CIFRADA con la KEK de devices (sobre), solo para devolver la misma respuesta si ArgosISP repite
--     exactamente la misma petición antes de que caduque (idempotencia); cualquier otra petición → 410. Se purga a
--     los 7 días (la auditoría queda en auth.audit_log).
--   * devices.integration_argosisp: cómo se preparó el conector (enlace generado o canjeado), el API token hf_pat_
--     emitido para Argos (para revocarlo si se vuelve a conectar), el canal webhook hacia Argos y el id opaco del
--     conector en Argos.
-- RLS fail-closed como el resto del esquema; el canje público busca por hash con devices_platform. Tabla nueva y
-- vacía: índices en la misma transacción; las columnas nuevas admiten NULL (sin reescritura).

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE devices.argosisp_connection_link (
  id                  uuid NOT NULL PRIMARY KEY,
  tenant_id           uuid NOT NULL,
  code_hash           bytea NOT NULL CONSTRAINT ck_connection_link__hash CHECK (length(code_hash) = 32),
  created_by          uuid NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now(),
  expires_at          timestamptz NOT NULL,
  used_at             timestamptz,
  used_ip             inet,
  request_hash        bytea CONSTRAINT ck_connection_link__request CHECK (length(request_hash) = 32),
  response_ciphertext bytea,
  response_dek        bytea,
  kek_id              text,
  revoked_at          timestamptz,
  UNIQUE (tenant_id, id),
  CONSTRAINT ck_connection_link__expiry CHECK (expires_at > created_at AND expires_at <= created_at + interval '15 minutes'),
  CONSTRAINT ck_connection_link__response CHECK ((response_ciphertext IS NULL) = (response_dek IS NULL))
);
CREATE UNIQUE INDEX ux_connection_link__hash ON devices.argosisp_connection_link (code_hash);
CREATE INDEX ix_connection_link__tenant ON devices.argosisp_connection_link (tenant_id, created_at DESC);

ALTER TABLE devices.integration_argosisp
  ADD COLUMN linked_at          timestamptz,
  ADD COLUMN link_method        text,
  ADD COLUMN peer_connector_id  text,
  ADD COLUMN peer_api_token_id  uuid,
  ADD COLUMN webhook_channel_id uuid;
ALTER TABLE devices.integration_argosisp ADD CONSTRAINT ck_integration_argosisp__link_method
  CHECK (link_method IN ('link_generated', 'link_redeemed')) NOT VALID;
ALTER TABLE devices.integration_argosisp VALIDATE CONSTRAINT ck_integration_argosisp__link_method;
ALTER TABLE devices.integration_argosisp ADD CONSTRAINT ck_integration_argosisp__peer_connector
  CHECK (char_length(peer_connector_id) <= 128) NOT VALID;
ALTER TABLE devices.integration_argosisp VALIDATE CONSTRAINT ck_integration_argosisp__peer_connector;

-- +goose StatementBegin
DO $$
BEGIN
  EXECUTE 'ALTER TABLE devices.argosisp_connection_link ENABLE ROW LEVEL SECURITY';
  EXECUTE 'ALTER TABLE devices.argosisp_connection_link FORCE ROW LEVEL SECURITY';
  EXECUTE 'CREATE POLICY p_tenant ON devices.argosisp_connection_link USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())';
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE, DELETE ON devices.argosisp_connection_link TO devices_app, devices_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE devices.integration_argosisp
  DROP CONSTRAINT IF EXISTS ck_integration_argosisp__peer_connector,
  DROP CONSTRAINT IF EXISTS ck_integration_argosisp__link_method,
  DROP COLUMN IF EXISTS webhook_channel_id,
  DROP COLUMN IF EXISTS peer_api_token_id,
  DROP COLUMN IF EXISTS peer_connector_id,
  DROP COLUMN IF EXISTS link_method,
  DROP COLUMN IF EXISTS linked_at;
DROP TABLE IF EXISTS devices.argosisp_connection_link;
