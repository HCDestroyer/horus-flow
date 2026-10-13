-- Esquema assist (asistente de IA, docs/assist.md): configuración del proveedor por ISP, auditoría de
-- cada consulta a la IA y tokens MCP. RLS fail-closed en todo lo que es de un tenant.
--  * settings: una fila por ISP (si no hay fila rige el valor por defecto de plataforma del entorno). La
--    clave de API es write-only: se guarda con envelope encryption (KEK de assist, AAD
--    "assist_api_key:<tenant>") y nunca vuelve en la API.
--  * audit: quién, feature, proveedor, modelo, tokens, herramientas usadas (nombres y conteos), duración y
--    resultado. Sin datos de clientes: ni la respuesta, ni la evidencia, ni IPs.
--  * mcp_token: tokens de acceso del servidor MCP (solo el hash SHA-256; el valor se muestra una vez). La
--    búsqueda por hash (antes de conocer el tenant) usa el rol de plataforma.
-- Roles: assist_app (sin BYPASSRLS) y assist_platform (BYPASSRLS: solo la búsqueda del token MCP por hash).

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';
SET LOCAL client_min_messages = warning;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.horus_current_tenant() RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT nullif(current_setting('horus.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    CREATE ROLE assist_app NOLOGIN;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
  BEGIN
    CREATE ROLE assist_platform NOLOGIN BYPASSRLS;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
END $$;
-- +goose StatementEnd
GRANT assist_app, assist_platform TO CURRENT_USER;

CREATE TABLE assist.settings (
  tenant_id           uuid NOT NULL PRIMARY KEY,
  enabled             boolean NOT NULL DEFAULT false,
  provider            text NOT NULL CHECK (provider IN ('anthropic', 'openai_compatible')),
  model               text NOT NULL CHECK (length(model) BETWEEN 1 AND 100),
  base_url            text CHECK (base_url IS NULL OR length(base_url) BETWEEN 8 AND 300),
  api_key_ciphertext  bytea,
  api_key_dek_wrapped bytea,
  api_key_kek_id      text,
  effort              text NOT NULL DEFAULT 'medium' CHECK (effort IN ('low', 'medium', 'high')),
  daily_request_limit integer NOT NULL DEFAULT 100 CHECK (daily_request_limit BETWEEN 1 AND 10000),
  version             integer NOT NULL DEFAULT 1,
  updated_at          timestamptz NOT NULL DEFAULT now(),
  updated_by          uuid,
  CONSTRAINT ck_settings__api_key CHECK (
    (api_key_ciphertext IS NULL) = (api_key_dek_wrapped IS NULL)
    AND (api_key_ciphertext IS NULL) = (api_key_kek_id IS NULL)),
  CONSTRAINT ck_settings__base_url CHECK (provider <> 'openai_compatible' OR base_url IS NOT NULL)
);

CREATE TABLE assist.audit (
  id            uuid NOT NULL PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  user_id       uuid,
  feature       text NOT NULL CHECK (feature IN ('finding', 'router', 'mcp', 'test')),
  context_id    text NOT NULL DEFAULT '' CHECK (length(context_id) <= 100),
  provider      text NOT NULL DEFAULT '' CHECK (length(provider) <= 40),
  model         text NOT NULL DEFAULT '' CHECK (length(model) <= 100),
  input_tokens  bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
  tool_calls    jsonb NOT NULL DEFAULT '[]',
  duration_ms   integer NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
  outcome       text NOT NULL CHECK (outcome IN ('success', 'refused', 'error', 'limited', 'denied')),
  error_code    text NOT NULL DEFAULT '' CHECK (length(error_code) <= 60),
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_audit__list ON assist.audit (tenant_id, created_at DESC, id DESC);

CREATE TABLE assist.mcp_token (
  id           uuid NOT NULL PRIMARY KEY,
  tenant_id    uuid NOT NULL,
  user_id      uuid NOT NULL,
  name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  token_hash   bytea NOT NULL CHECK (length(token_hash) = 32),
  prefix       text NOT NULL CHECK (length(prefix) BETWEEN 4 AND 20),
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  last_used_at timestamptz,
  revoked_at   timestamptz,
  UNIQUE (token_hash)
);
CREATE INDEX ix_mcp_token__list ON assist.mcp_token (tenant_id, created_at DESC);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['assist.settings', 'assist.audit', 'assist.mcp_token'] LOOP
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY p_tenant ON %s USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA assist TO assist_app, assist_platform;
GRANT SELECT, INSERT, UPDATE ON assist.settings, assist.mcp_token TO assist_app;
GRANT SELECT, INSERT ON assist.audit TO assist_app;
GRANT SELECT, UPDATE ON assist.mcp_token TO assist_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS assist.mcp_token;
DROP TABLE IF EXISTS assist.audit;
DROP TABLE IF EXISTS assist.settings;
REVOKE ALL ON SCHEMA assist FROM assist_app, assist_platform;
-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    DROP ROLE IF EXISTS assist_app;
  EXCEPTION WHEN dependent_objects_still_exist OR object_in_use THEN NULL;
  END;
  BEGIN
    DROP ROLE IF EXISTS assist_platform;
  EXCEPTION WHEN dependent_objects_still_exist OR object_in_use THEN NULL;
  END;
END $$;
-- +goose StatementEnd
