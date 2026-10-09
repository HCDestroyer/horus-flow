-- I0-06 · Esquema auth (tenants e identidad) traducido del contrato C2
-- (packages/schemas/datastore/v0/postgres-contract-v0.sql). Nombres, tipos, claves, CHECKs, unicidad,
-- RLS fail-closed y FKs compuestas son los del contrato; aquí se añaden los roles de BD del módulo
-- (docs/database.md §1.2: auth_app sin BYPASSRLS, auth_platform con BYPASSRLS) y sus permisos.
-- Las tablas son nuevas y vacías: los índices se crean en la misma transacción (sin CONCURRENTLY).

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';
SET LOCAL client_min_messages = warning;

CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Función de contexto compartida por los módulos: NULL si la transacción no fijó horus.tenant_id ⇒ cero filas.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.horus_current_tenant() RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT nullif(current_setting('horus.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd

-- Roles del módulo (sin LOGIN: el proceso entra con su usuario y hace SET LOCAL ROLE por transacción).
-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    CREATE ROLE auth_app NOLOGIN;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
  BEGIN
    CREATE ROLE auth_platform NOLOGIN BYPASSRLS;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
END $$;
-- +goose StatementEnd
GRANT auth_app, auth_platform TO CURRENT_USER;

CREATE TABLE auth.tenant (
  id            uuid PRIMARY KEY,
  slug          text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
  name          text NOT NULL,
  status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'offboarding')),
  country       char(2) NOT NULL,
  timezone      text NOT NULL,
  quotas        jsonb NOT NULL DEFAULT '{}',
  settings      jsonb NOT NULL DEFAULT '{}',
  support_access_policy text NOT NULL DEFAULT 'notify' CHECK (support_access_policy IN ('notify', 'require_approval', 'deny')),
  created_by    uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  deleted_at    timestamptz,
  version       integer NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX ux_tenant__slug ON auth.tenant (slug) WHERE deleted_at IS NULL;

CREATE TABLE auth."user" (
  id                  uuid PRIMARY KEY,
  email               citext NOT NULL,
  display_name        text NOT NULL,
  password_hash       text,
  status              text NOT NULL DEFAULT 'pending' CHECK (status IN ('active', 'disabled', 'locked', 'pending')),
  is_platform_admin   boolean NOT NULL DEFAULT false,
  mfa_enforced        boolean NOT NULL DEFAULT false,
  must_change_password boolean NOT NULL DEFAULT false,
  failed_logins       integer NOT NULL DEFAULT 0,
  locked_until        timestamptz,
  last_login_at       timestamptz,
  password_changed_at timestamptz,
  locale              text,
  timezone            text,
  default_tenant_id   uuid,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  deleted_at          timestamptz,
  version             integer NOT NULL DEFAULT 1,
  CONSTRAINT ck_user__platform_admin_mfa CHECK (NOT is_platform_admin OR mfa_enforced)
);
CREATE UNIQUE INDEX ux_user__email ON auth."user" (email) WHERE deleted_at IS NULL;

CREATE TABLE auth.platform_role_assignment (
  user_id    uuid NOT NULL REFERENCES auth."user" (id),
  role_key   text NOT NULL CHECK (role_key IN ('platform_admin', 'platform_operator', 'platform_auditor')),
  granted_by uuid,
  granted_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, role_key)
);

CREATE TABLE auth.permission (
  key         text PRIMARY KEY,
  family      text NOT NULL CHECK (family IN ('tenant', 'platform')),
  description text NOT NULL,
  pii         boolean NOT NULL DEFAULT false,
  audited     boolean NOT NULL DEFAULT false,
  reauth      boolean NOT NULL DEFAULT false,
  deprecated_at timestamptz
);

CREATE TABLE auth.role (
  id           uuid PRIMARY KEY,
  tenant_id    uuid REFERENCES auth.tenant (id),
  key          text NOT NULL,
  name         text NOT NULL,
  is_system    boolean NOT NULL DEFAULT false,
  requires_mfa boolean NOT NULL DEFAULT false,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  version      integer NOT NULL DEFAULT 1,
  CONSTRAINT ck_role__system_has_no_tenant CHECK (NOT is_system OR tenant_id IS NULL)
);
CREATE UNIQUE INDEX ux_role__tenant_key ON auth.role (coalesce(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid), key);

CREATE TABLE auth.role_permission (
  role_id        uuid NOT NULL REFERENCES auth.role (id) ON DELETE CASCADE,
  permission_key text NOT NULL REFERENCES auth.permission (key),
  PRIMARY KEY (role_id, permission_key)
);

CREATE TABLE auth.tenant_membership (
  id         uuid PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES auth.tenant (id),
  user_id    uuid NOT NULL REFERENCES auth."user" (id),
  status     text NOT NULL DEFAULT 'invited' CHECK (status IN ('active', 'invited', 'revoked')),
  granted_by uuid,
  granted_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  version    integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, user_id)
);
CREATE INDEX ix_tenant_membership__user ON auth.tenant_membership (user_id);

CREATE TABLE auth.role_assignment (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  membership_id uuid NOT NULL,
  role_id       uuid NOT NULL REFERENCES auth.role (id),
  scope_type    text NOT NULL CHECK (scope_type IN ('tenant', 'site', 'router_group')),
  scope_id      uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, membership_id) REFERENCES auth.tenant_membership (tenant_id, id) ON DELETE CASCADE,
  CONSTRAINT ck_role_assignment__scope CHECK ((scope_type = 'tenant') = (scope_id IS NULL))
);
CREATE UNIQUE INDEX ux_role_assignment ON auth.role_assignment (tenant_id, membership_id, role_id, scope_type, coalesce(scope_id, '00000000-0000-0000-0000-000000000000'::uuid));

CREATE TABLE auth.session (
  id              uuid PRIMARY KEY,
  user_id         uuid NOT NULL REFERENCES auth."user" (id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_seen_at    timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz NOT NULL,
  revoked_at      timestamptz,
  revoked_reason  text,
  ip              inet,
  user_agent      text,
  amr             text[] NOT NULL DEFAULT '{}',
  mfa_verified_at timestamptz
);
CREATE INDEX ix_session__user_active ON auth.session (user_id) WHERE revoked_at IS NULL;

CREATE TABLE auth.refresh_token (
  id          uuid PRIMARY KEY,
  session_id  uuid NOT NULL REFERENCES auth.session (id) ON DELETE CASCADE,
  token_hash  bytea NOT NULL UNIQUE,
  issued_at   timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz,
  replaced_by uuid
);
CREATE INDEX ix_refresh_token__session ON auth.refresh_token (session_id);

CREATE TABLE auth.kiosk (
  id                   uuid PRIMARY KEY,
  tenant_id            uuid NOT NULL REFERENCES auth.tenant (id),
  name                 text NOT NULL,
  status               text NOT NULL DEFAULT 'pending_enrollment' CHECK (status IN ('pending_enrollment', 'active', 'revoked', 'expired')),
  playlist_id          uuid,
  dashboard_ids        uuid[] NOT NULL DEFAULT '{}',
  allowed_cidrs        cidr[] NOT NULL DEFAULT '{}',
  show_personal_data   boolean NOT NULL DEFAULT false,
  show_personal_data_reason text,
  critical_finding_banner boolean NOT NULL DEFAULT false,
  credential_hash      bytea,
  credential_family_id uuid,
  expires_at           timestamptz NOT NULL,
  last_seen_at         timestamptz,
  last_ip              inet,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  version              integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CONSTRAINT ck_kiosk__personal_data_reason CHECK (NOT show_personal_data OR show_personal_data_reason IS NOT NULL)
);

CREATE TABLE auth.kiosk_enrollment_code (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  kiosk_id    uuid NOT NULL,
  code_hash   bytea NOT NULL UNIQUE,
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz,
  failures    integer NOT NULL DEFAULT 0,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, kiosk_id) REFERENCES auth.kiosk (tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE auth.audit_log (
  id           uuid NOT NULL,
  tenant_id    uuid,
  occurred_at  timestamptz NOT NULL,
  actor_type   text NOT NULL,
  actor_id     text NOT NULL,
  via_platform boolean NOT NULL DEFAULT false,
  action       text NOT NULL,
  resource_type text NOT NULL,
  resource_id  text,
  scope        text,
  outcome      text NOT NULL CHECK (outcome IN ('success', 'denied', 'failure')),
  ip           inet,
  user_agent   text,
  changes      jsonb NOT NULL DEFAULT '{}',
  request_id   text,
  trace_id     text,
  prev_hash    bytea,
  hash         bytea NOT NULL,
  PRIMARY KEY (occurred_at, id)
) PARTITION BY RANGE (occurred_at);
CREATE TABLE auth.audit_log_default PARTITION OF auth.audit_log DEFAULT;

CREATE TABLE auth.outbox (
  id uuid PRIMARY KEY, seq bigint GENERATED ALWAYS AS IDENTITY, tenant_id uuid, subject text NOT NULL,
  aggregate_id uuid NOT NULL, headers jsonb NOT NULL DEFAULT '{}', payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL, published_at timestamptz, attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX ix_outbox__pending ON auth.outbox (seq) WHERE published_at IS NULL;

-- RLS fail-closed (M5): ENABLE + FORCE + p_tenant en toda tabla de negocio con tenant_id.
ALTER TABLE auth.tenant_membership ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.tenant_membership FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.tenant_membership USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant());
ALTER TABLE auth.role_assignment ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.role_assignment FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.role_assignment USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant());
ALTER TABLE auth.kiosk ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.kiosk FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.kiosk USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant());
ALTER TABLE auth.kiosk_enrollment_code ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.kiosk_enrollment_code FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.kiosk_enrollment_code USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant());
-- auth.role: plantillas (tenant_id NULL) visibles para todos; propios solo en su tenant.
ALTER TABLE auth.role ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.role FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.role USING (tenant_id IS NULL OR tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
-- auth.audit_log: un usuario de tenant ve solo su tenant (las lecturas de plataforma usan auth_platform).
ALTER TABLE auth.audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.audit_log USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id IS NOT DISTINCT FROM public.horus_current_tenant());
-- Sin RLS (no son de un tenant): auth.tenant, auth."user", auth.platform_role_assignment, auth.permission,
-- auth.role_permission, auth.session, auth.refresh_token, auth.outbox. Su acceso lo restringe el módulo auth.

-- Permisos: DML para los roles de la aplicación; auditoría solo INSERT/SELECT (append-only).
GRANT USAGE ON SCHEMA auth TO auth_app, auth_platform;
GRANT SELECT, INSERT, UPDATE, DELETE ON
  auth.tenant, auth."user", auth.platform_role_assignment, auth.permission, auth.role, auth.role_permission,
  auth.tenant_membership, auth.role_assignment, auth.session, auth.refresh_token, auth.kiosk,
  auth.kiosk_enrollment_code, auth.outbox
  TO auth_app, auth_platform;
GRANT SELECT, INSERT ON auth.audit_log TO auth_app, auth_platform;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA auth TO auth_app, auth_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS auth.outbox;
DROP TABLE IF EXISTS auth.audit_log;
DROP TABLE IF EXISTS auth.kiosk_enrollment_code;
DROP TABLE IF EXISTS auth.kiosk;
DROP TABLE IF EXISTS auth.refresh_token;
DROP TABLE IF EXISTS auth.session;
DROP TABLE IF EXISTS auth.role_assignment;
DROP TABLE IF EXISTS auth.tenant_membership;
DROP TABLE IF EXISTS auth.role_permission;
DROP TABLE IF EXISTS auth.role;
DROP TABLE IF EXISTS auth.permission;
DROP TABLE IF EXISTS auth.platform_role_assignment;
DROP TABLE IF EXISTS auth."user";
DROP TABLE IF EXISTS auth.tenant;
REVOKE ALL ON SCHEMA auth FROM auth_app, auth_platform;
-- Los roles son del clúster: se borran solo si ninguna otra base los usa. La extensión y
-- public.horus_current_tenant() son compartidas con otros módulos y no se borran.
-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    DROP ROLE IF EXISTS auth_app;
  EXCEPTION WHEN dependent_objects_still_exist OR object_in_use THEN NULL;
  END;
  BEGIN
    DROP ROLE IF EXISTS auth_platform;
  EXCEPTION WHEN dependent_objects_still_exist OR object_in_use THEN NULL;
  END;
END $$;
-- +goose StatementEnd
