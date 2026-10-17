-- API tokens de integración `hf_pat_…` (docs/security.md §5.4, ADR-0030): ligados a un ISP y a quien los
-- crea, con un subconjunto de sus permisos (claves; los alcances salen de sus asignaciones en cada canje),
-- caducidad obligatoria (máx. 1 año), último uso y revocación. Solo se guarda el SHA-256 del token. El
-- gateway lo canjea por un access token de 2 min (búsqueda por hash con auth_platform: aún no conoce el ISP).
-- RLS fail-closed como el resto de tablas de tenant. Tabla nueva y vacía: índices en la misma transacción.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE auth.api_token (
  id           uuid NOT NULL PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES auth.tenant (id) ON DELETE CASCADE,
  user_id      uuid NOT NULL REFERENCES auth."user" (id),
  name         text NOT NULL CONSTRAINT ck_api_token__name CHECK (char_length(name) BETWEEN 1 AND 80),
  prefix       text NOT NULL,
  token_hash   bytea NOT NULL CONSTRAINT ck_api_token__hash CHECK (length(token_hash) = 32),
  permissions  text[] NOT NULL CONSTRAINT ck_api_token__permissions CHECK (cardinality(permissions) BETWEEN 1 AND 64),
  template     text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  last_used_at timestamptz,
  last_used_ip inet,
  revoked_at   timestamptz,
  revoked_by   uuid,
  CONSTRAINT ck_api_token__expiry CHECK (expires_at > created_at AND expires_at <= created_at + interval '366 days')
);
CREATE UNIQUE INDEX ux_api_token__hash ON auth.api_token (token_hash);
CREATE INDEX ix_api_token__tenant ON auth.api_token (tenant_id, created_at DESC);
CREATE INDEX ix_api_token__user ON auth.api_token (user_id) WHERE revoked_at IS NULL;

ALTER TABLE auth.api_token ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.api_token FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.api_token USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
-- Sin DELETE: un token revocado se conserva (auditoría); se borra con el ISP.
GRANT SELECT, INSERT, UPDATE ON auth.api_token TO auth_app, auth_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS auth.api_token;
