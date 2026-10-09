-- I1-14 · Credenciales de kiosco rotadas (detección de reutilización, security.md §5.5): cada canje de la
-- cookie __Secure-hf_kiosk la rota y guarda aquí el SHA-256 de la anterior; si vuelve a presentarse, el
-- kiosco se revoca y se avisa al administrador. Se purga con el kiosco.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE auth.kiosk_credential_rotated (
  credential_hash bytea PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  kiosk_id        uuid NOT NULL,
  family_id       uuid,
  rotated_at      timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (tenant_id, kiosk_id) REFERENCES auth.kiosk (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX ix_kiosk_credential_rotated__kiosk ON auth.kiosk_credential_rotated (tenant_id, kiosk_id);
CREATE UNIQUE INDEX ux_kiosk__credential_hash ON auth.kiosk (credential_hash) WHERE credential_hash IS NOT NULL;

ALTER TABLE auth.kiosk_credential_rotated ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.kiosk_credential_rotated FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.kiosk_credential_rotated USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
GRANT SELECT, INSERT, DELETE ON auth.kiosk_credential_rotated TO auth_app, auth_platform;

-- +goose Down
DROP INDEX IF EXISTS auth.ux_kiosk__credential_hash;
DROP TABLE IF EXISTS auth.kiosk_credential_rotated;
