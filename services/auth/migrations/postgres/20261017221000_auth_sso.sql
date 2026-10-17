-- Autologin entre sistemas (contrato docs/integrations/argosisp.md §1.5, H11; ADR-0030):
--   * auth.sso_ticket: ticket de un solo uso (60 s) que emite el servidor del sistema integrado con su API token
--     (permiso sso.issue). Solo se guarda su SHA-256; queda ligado al ISP y al conector. `redeemed_at` lo consume
--     (un uso) y `link_consumed_at` consume, también una vez, el link_token de la primera vinculación. Los tickets se
--     purgan al día siguiente (no son auditoría: el canje y la emisión quedan en auth.audit_log).
--   * auth.sso_link: vinculación (conector, usuario externo) → usuario local, creada solo tras iniciar sesión en
--     Horus. Nunca se crea un usuario ni se vincula por coincidir el correo (el correo y el nombre externos son una
--     instantánea informativa). La quitan el propio usuario o un administrador del ISP.
-- RLS fail-closed por tenant; el canje público busca el ticket por hash con auth_platform (aún no conoce el ISP) y
-- "mis vinculaciones" lista por usuario en todos sus ISP con auth_platform. Tablas nuevas y vacías: índices en la
-- misma transacción.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE auth.sso_ticket (
  id               uuid NOT NULL PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES auth.tenant (id) ON DELETE CASCADE,
  system           text NOT NULL DEFAULT 'argosisp' CONSTRAINT ck_sso_ticket__system CHECK (system IN ('argosisp')),
  connector_id     uuid NOT NULL,
  api_token_id     uuid,
  ticket_hash      bytea NOT NULL CONSTRAINT ck_sso_ticket__hash CHECK (length(ticket_hash) = 32),
  external_user_id text NOT NULL CONSTRAINT ck_sso_ticket__ext_user CHECK (char_length(external_user_id) BETWEEN 1 AND 128),
  external_email   text CONSTRAINT ck_sso_ticket__ext_email CHECK (char_length(external_email) <= 254),
  external_name    text CONSTRAINT ck_sso_ticket__ext_name CHECK (char_length(external_name) <= 120),
  target_type      text NOT NULL CONSTRAINT ck_sso_ticket__target_type CHECK (target_type IN ('cliente', 'servicio', 'router', 'hallazgo')),
  target_id        text NOT NULL CONSTRAINT ck_sso_ticket__target_id CHECK (char_length(target_id) BETWEEN 1 AND 128),
  created_at       timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL,
  redeemed_at      timestamptz,
  redeemed_ip      inet,
  link_consumed_at timestamptz,
  CONSTRAINT ck_sso_ticket__expiry CHECK (expires_at > created_at AND expires_at <= created_at + interval '60 seconds')
);
CREATE UNIQUE INDEX ux_sso_ticket__hash ON auth.sso_ticket (ticket_hash);
CREATE INDEX ix_sso_ticket__expires ON auth.sso_ticket (expires_at);

CREATE TABLE auth.sso_link (
  id               uuid NOT NULL PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES auth.tenant (id) ON DELETE CASCADE,
  system           text NOT NULL DEFAULT 'argosisp' CONSTRAINT ck_sso_link__system CHECK (system IN ('argosisp')),
  connector_id     uuid NOT NULL,
  external_user_id text NOT NULL CONSTRAINT ck_sso_link__ext_user CHECK (char_length(external_user_id) BETWEEN 1 AND 128),
  external_email   text CONSTRAINT ck_sso_link__ext_email CHECK (char_length(external_email) <= 254),
  external_name    text CONSTRAINT ck_sso_link__ext_name CHECK (char_length(external_name) <= 120),
  user_id          uuid NOT NULL REFERENCES auth."user" (id) ON DELETE CASCADE,
  created_at       timestamptz NOT NULL DEFAULT now(),
  last_used_at     timestamptz
);
CREATE UNIQUE INDEX ux_sso_link__external ON auth.sso_link (tenant_id, connector_id, external_user_id);
CREATE INDEX ix_sso_link__user ON auth.sso_link (user_id);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['auth.sso_ticket', 'auth.sso_link'] LOOP
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY p_tenant ON %s USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE, DELETE ON auth.sso_ticket, auth.sso_link TO auth_app, auth_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS auth.sso_link;
DROP TABLE IF EXISTS auth.sso_ticket;
