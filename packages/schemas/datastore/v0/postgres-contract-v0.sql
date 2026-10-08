-- =============================================================================================
-- C2 — Modelo multi-tenant v0 en PostgreSQL (DDL DE REFERENCIA, contrato del gate G0).
-- Fuente: docs/database.md §1–§2.3, ADR-0017, ADR-0018, D1, D6, D12, D15.
-- No es una migración: CORE la traduce a migraciones goose de mod:auth y mod:devices (I0-06) con
-- marca de tiempo UTC, lock_timeout y CREATE INDEX CONCURRENTLY donde aplique. Lo que se congela aquí
-- son nombres, tipos, claves, CHECKs, unicidad, RLS fail-closed y FKs compuestas (tenant_id, id).
-- Verificación: `npm run check:db` (aplica este archivo en un PostgreSQL 16 vacío).
-- =============================================================================================
SET client_min_messages = warning;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE SCHEMA auth;
CREATE SCHEMA devices;

-- Función de contexto: NULL si la transacción no fijó horus.tenant_id ⇒ cero filas (fail-closed).
CREATE FUNCTION public.horus_current_tenant() RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT nullif(current_setting('horus.tenant_id', true), '')::uuid $$;

-- ------------------------------------------------------------------ auth (tenants e identidad)
CREATE TABLE auth.tenant (
  id            uuid PRIMARY KEY,
  slug          text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
  name          text NOT NULL,
  status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'offboarding')),
  country       char(2) NOT NULL,
  timezone      text NOT NULL,
  quotas        jsonb NOT NULL DEFAULT '{}',
  settings      jsonb NOT NULL DEFAULT '{}',   -- customer_inactivity_days (30), customer_default_kind, kind_auto_apply_min_confidence (80), retention_overrides
  support_access_policy text NOT NULL DEFAULT 'notify' CHECK (support_access_policy IN ('notify', 'require_approval', 'deny')),
  created_by    uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  deleted_at    timestamptz,
  version       integer NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX ux_tenant__slug ON auth.tenant (slug) WHERE deleted_at IS NULL;

CREATE TABLE auth."user" (                 -- global: sin tenant_id (usuario de plataforma)
  id                  uuid PRIMARY KEY,
  email               citext NOT NULL,
  display_name        text NOT NULL,
  password_hash       text,                -- Argon2id PHC
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

CREATE TABLE auth.permission (             -- catálogo global (packages/schemas/permissions/v0/permissions.yaml)
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
  tenant_id    uuid REFERENCES auth.tenant (id),   -- NULL = plantilla de sistema
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

-- Asignaciones (rol, alcance) de una membresía; alcance siempre dentro del mismo tenant.
-- (database.md §2.1 tiene role_id en la membresía + acl_entry; api.md/security.md usan varias asignaciones
--  con alcance: el contrato adopta la forma de security.md §6.3 — ver G0, decisión C2-1.)
CREATE TABLE auth.role_assignment (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  membership_id uuid NOT NULL,
  role_id       uuid NOT NULL REFERENCES auth.role (id),
  scope_type    text NOT NULL CHECK (scope_type IN ('tenant', 'site', 'router_group')),
  scope_id      uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (tenant_id, membership_id) REFERENCES auth.tenant_membership (tenant_id, id) ON DELETE CASCADE,
  CONSTRAINT ck_role_assignment__scope CHECK ((scope_type = 'tenant') = (scope_id IS NULL))
);
CREATE UNIQUE INDEX ux_role_assignment ON auth.role_assignment (tenant_id, membership_id, role_id, scope_type, coalesce(scope_id, '00000000-0000-0000-0000-000000000000'::uuid));

CREATE TABLE auth.session (                -- de la persona, no del tenant
  id              uuid PRIMARY KEY,         -- = sid
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
  token_hash  bytea NOT NULL UNIQUE,        -- SHA-256
  issued_at   timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz,
  replaced_by uuid
);

CREATE TABLE auth.kiosk (                  -- dispositivo registrado (security.md §5.5)
  id                   uuid PRIMARY KEY,
  tenant_id            uuid NOT NULL REFERENCES auth.tenant (id),
  name                 text NOT NULL,
  status               text NOT NULL DEFAULT 'pending_enrollment' CHECK (status IN ('pending_enrollment', 'active', 'revoked', 'expired')),
  playlist_id          uuid,                -- sin FK: analytics
  dashboard_ids        uuid[] NOT NULL DEFAULT '{}',
  allowed_cidrs        cidr[] NOT NULL DEFAULT '{}',
  show_personal_data   boolean NOT NULL DEFAULT false,
  show_personal_data_reason text,
  critical_finding_banner boolean NOT NULL DEFAULT false,
  credential_hash      bytea,               -- SHA-256 de la credencial vigente (rotativa)
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
  expires_at  timestamptz NOT NULL,         -- 10 min
  used_at     timestamptz,
  failures    integer NOT NULL DEFAULT 0,   -- 10 ⇒ invalidado
  FOREIGN KEY (tenant_id, kiosk_id) REFERENCES auth.kiosk (tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE auth.audit_log (              -- append-only, cadena de hashes; solo INSERT
  id           uuid NOT NULL,
  tenant_id    uuid,                         -- NULL = acción de plataforma
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

-- ------------------------------------------------------------------ devices (inventario por tenant)
CREATE TABLE devices.site (
  id         uuid NOT NULL,
  tenant_id  uuid NOT NULL,
  parent_id  uuid,
  code       text,
  name       text NOT NULL,
  kind       text NOT NULL DEFAULT 'node' CHECK (kind IN ('node', 'region', 'datacenter', 'other')),
  address    text,
  latitude   double precision CHECK (latitude BETWEEN -90 AND 90),
  longitude  double precision CHECK (longitude BETWEEN -180 AND 180),
  timezone   text,
  tags       text[] NOT NULL DEFAULT '{}',
  metadata   jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  version    integer NOT NULL DEFAULT 1,
  PRIMARY KEY (id),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, parent_id) REFERENCES devices.site (tenant_id, id)
);
CREATE UNIQUE INDEX ux_site__tenant_code ON devices.site (tenant_id, code) WHERE deleted_at IS NULL AND code IS NOT NULL;
CREATE INDEX ix_site__name_trgm ON devices.site USING gin (name gin_trgm_ops);

CREATE TABLE devices.router (
  id                   uuid NOT NULL PRIMARY KEY,
  tenant_id            uuid NOT NULL,
  site_id              uuid NOT NULL,
  hostname             text NOT NULL,
  display_name         text,
  vendor               text NOT NULL DEFAULT 'mikrotik' CHECK (vendor IN ('mikrotik')),
  model                text,
  serial_number        text,
  is_primary           boolean NOT NULL DEFAULT true,
  admin_state          text NOT NULL DEFAULT 'active' CHECK (admin_state IN ('active', 'maintenance', 'decommissioned')),
  onboarding_state     text NOT NULL DEFAULT 'pending_configuration'
                        CHECK (onboarding_state IN ('pending_configuration', 'key_received', 'tunnel_up', 'exporting')),
  routeros_version     text CHECK (routeros_version ~ '^7\.[0-9]+(\.[0-9]+)?$'),   -- D15: solo RouterOS 7.x
  routeros_version_detected text,
  routeros_version_supported boolean,          -- false si < 7.12 (aviso, no bloqueo del registro)
  tunnel_address       inet,                   -- /32 de túnel = identidad del exportador (la asigna wireguard)
  mgmt_wireguard_peer_id uuid,                 -- sin FK: wireguard
  flow_export_enabled  boolean NOT NULL DEFAULT true,
  tags                 text[] NOT NULL DEFAULT '{}',
  metadata             jsonb NOT NULL DEFAULT '{}',
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  deleted_at           timestamptz,
  version              integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES devices.site (tenant_id, id)
);
CREATE UNIQUE INDEX ux_router__primary_per_site ON devices.router (tenant_id, site_id) WHERE is_primary AND deleted_at IS NULL;
CREATE UNIQUE INDEX ux_router__tenant_hostname ON devices.router (tenant_id, hostname) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX ux_router__tunnel_address ON devices.router (tunnel_address) WHERE deleted_at IS NULL AND tunnel_address IS NOT NULL; -- única en la plataforma

CREATE TABLE devices.interface (
  id               uuid NOT NULL PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  router_id        uuid NOT NULL,
  if_index         integer,                    -- NULL en el grupo dinámico PPPoE/L2TP
  is_dynamic_group boolean NOT NULL DEFAULT false,
  name             text NOT NULL,
  alias            text,
  speed_bps        bigint,
  flow_role        text NOT NULL DEFAULT 'none' CHECK (flow_role IN ('customer_edge', 'upstream', 'peering', 'core', 'management', 'none')),
  monitored        boolean NOT NULL DEFAULT false,
  last_seen_at     timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  deleted_at       timestamptz,
  version          integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, router_id) REFERENCES devices.router (tenant_id, id)
);
CREATE UNIQUE INDEX ux_interface__ifindex ON devices.interface (tenant_id, router_id, if_index) WHERE deleted_at IS NULL AND if_index IS NOT NULL;

CREATE TABLE devices.credential (          -- envelope encryption; ninguna API devuelve el secreto
  id                uuid NOT NULL PRIMARY KEY,
  tenant_id         uuid NOT NULL,
  router_id         uuid NOT NULL,
  kind              text NOT NULL CHECK (kind IN ('snmp_v3', 'routeros_api')),
  username          text NOT NULL,
  secret_ciphertext bytea,
  dek_wrapped       bytea,
  kek_id            text,
  secret_ref        text,
  tls_fingerprint_sha256 text,
  last_used_at      timestamptz,
  last_result       text CHECK (last_result IN ('ok', 'auth_failed', 'unreachable', 'tls_fingerprint_changed')),
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  deleted_at        timestamptz,
  version           integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, router_id) REFERENCES devices.router (tenant_id, id),
  CONSTRAINT ck_credential__secret CHECK (secret_ciphertext IS NOT NULL OR secret_ref IS NOT NULL)
);
CREATE UNIQUE INDEX ux_credential__router_kind ON devices.credential (tenant_id, router_id, kind) WHERE deleted_at IS NULL;

CREATE TABLE devices.ip_realm (            -- espacio donde una IP es única
  id         uuid NOT NULL PRIMARY KEY,
  tenant_id  uuid NOT NULL,
  kind       text NOT NULL CHECK (kind IN ('public', 'node_private')),
  site_id    uuid,
  name       text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  version    integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES devices.site (tenant_id, id),
  CONSTRAINT ck_ip_realm__site CHECK ((kind = 'node_private') = (site_id IS NOT NULL))
);
CREATE UNIQUE INDEX ux_ip_realm__public ON devices.ip_realm (tenant_id) WHERE kind = 'public' AND deleted_at IS NULL;
CREATE UNIQUE INDEX ux_ip_realm__node ON devices.ip_realm (tenant_id, site_id) WHERE kind = 'node_private' AND deleted_at IS NULL;

CREATE TABLE devices.client_prefix (       -- qué IPs son de clientes en cada nodo (D1, D12)
  id              uuid NOT NULL PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  site_id         uuid NOT NULL,
  realm_id        uuid NOT NULL,
  prefix          cidr NOT NULL,
  role            text NOT NULL CHECK (role IN ('customers', 'infrastructure', 'excluded')),
  default_kind    text NOT NULL DEFAULT 'residential' CHECK (default_kind IN ('residential', 'commercial', 'unknown')),
  assignment_mode text NOT NULL DEFAULT 'unknown' CHECK (assignment_mode IN ('static', 'dynamic', 'unknown')),
  ipv6_client_len smallint CHECK (ipv6_client_len IN (48, 56, 60, 64)),
  source          text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'routeros_api', 'discovery_confirmed')),
  confirmed       boolean NOT NULL DEFAULT true,
  note            text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  deleted_at      timestamptz,
  version         integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES devices.site (tenant_id, id),
  FOREIGN KEY (tenant_id, realm_id) REFERENCES devices.ip_realm (tenant_id, id),
  CONSTRAINT ck_client_prefix__v6len CHECK ((family(prefix) = 6) = (ipv6_client_len IS NOT NULL)),
  CONSTRAINT ex_client_prefix__no_overlap EXCLUDE USING gist (realm_id WITH =, (prefix::inet) inet_ops WITH &&) WHERE (deleted_at IS NULL)
);
CREATE INDEX ix_client_prefix__prefix ON devices.client_prefix USING gist ((prefix::inet) inet_ops);

CREATE TABLE devices.customer (            -- cliente = IP observada (ADR-0018)
  id                       uuid NOT NULL PRIMARY KEY,
  tenant_id                uuid NOT NULL,
  realm_id                 uuid NOT NULL,
  address                  inet NOT NULL,     -- canónica: /32 o IPv6 truncada a ipv6_client_len
  site_id                  uuid NOT NULL,
  client_prefix_id         uuid,
  kind                     text NOT NULL DEFAULT 'residential' CHECK (kind IN ('residential', 'commercial', 'unknown')),
  kind_source              text NOT NULL DEFAULT 'default' CHECK (kind_source IN ('default', 'scoring', 'manual')),
  kind_locked              boolean NOT NULL DEFAULT false,
  kind_confidence          smallint CHECK (kind_confidence BETWEEN 0 AND 100),
  kind_changed_at          timestamptz,
  commercial_use_suspected boolean NOT NULL DEFAULT false,
  commercial_score         smallint CHECK (commercial_score BETWEEN 0 AND 100),
  security_state           text NOT NULL DEFAULT 'clean' CHECK (security_state IN ('clean', 'suspected', 'infected', 'mitigated')),
  open_findings            integer NOT NULL DEFAULT 0,
  alias                    text,              -- dato personal
  alias_source             text CHECK (alias_source IN ('manual', 'routeros_ppp')),
  notes                    text,              -- dato personal
  status                   text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
  inactive_reason          text CHECK (inactive_reason IN ('no_traffic', 'prefix_removed')),
  first_seen               timestamptz NOT NULL,
  last_seen                timestamptz NOT NULL,   -- resolución ≤ 1 h
  reset_at                 timestamptz,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  version                  integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, realm_id, address),        -- clave natural (la de ClickHouse)
  FOREIGN KEY (tenant_id, realm_id) REFERENCES devices.ip_realm (tenant_id, id),
  FOREIGN KEY (tenant_id, site_id) REFERENCES devices.site (tenant_id, id),
  CONSTRAINT ck_customer__manual_locked CHECK (kind_source <> 'manual' OR kind_locked),
  CONSTRAINT ck_customer__inactive_reason CHECK ((status = 'inactive') = (inactive_reason IS NOT NULL))
);
CREATE INDEX ix_customer__site_status ON devices.customer (tenant_id, site_id, status);
CREATE INDEX ix_customer__kind ON devices.customer (tenant_id, kind);
CREATE INDEX ix_customer__last_seen ON devices.customer (tenant_id, last_seen);
CREATE INDEX ix_customer__address ON devices.customer USING gist (address inet_ops);
CREATE INDEX ix_customer__alias_trgm ON devices.customer USING gin (alias gin_trgm_ops);

CREATE TABLE devices.customer_kind_change (  -- historial inmutable (solo INSERT)
  id                 uuid NOT NULL PRIMARY KEY,
  tenant_id          uuid NOT NULL,
  customer_id        uuid NOT NULL,
  from_kind          text,
  to_kind            text NOT NULL,
  source             text NOT NULL CHECK (source IN ('default', 'scoring', 'manual', 'reset')),
  reasons            jsonb NOT NULL DEFAULT '[]',   -- [{code, detail, weight}] copiadas en el momento del cambio
  reason_codes       text[] NOT NULL DEFAULT '{}',
  model_ref          text,
  confidence         smallint,
  actor_id           uuid,
  manual_reason      text,
  causation_event_id uuid,
  changed_at         timestamptz NOT NULL,
  FOREIGN KEY (tenant_id, customer_id) REFERENCES devices.customer (tenant_id, id) ON DELETE CASCADE,
  CONSTRAINT ck_kind_change__manual_reason CHECK (source <> 'manual' OR manual_reason IS NOT NULL)
);
CREATE INDEX ix_kind_change__customer ON devices.customer_kind_change (tenant_id, customer_id, changed_at DESC);

-- Outbox estándar (una por esquema; sin RLS: solo la lee el relay del módulo).
CREATE TABLE auth.outbox (
  id uuid PRIMARY KEY, seq bigint GENERATED ALWAYS AS IDENTITY, tenant_id uuid, subject text NOT NULL,
  aggregate_id uuid NOT NULL, headers jsonb NOT NULL DEFAULT '{}', payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL, published_at timestamptz, attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX ix_outbox__pending ON auth.outbox (seq) WHERE published_at IS NULL;
CREATE TABLE devices.outbox (LIKE auth.outbox INCLUDING ALL);

-- ------------------------------------------------------------------ RLS fail-closed (M5)
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'auth.tenant_membership', 'auth.role_assignment', 'auth.kiosk', 'auth.kiosk_enrollment_code',
    'devices.site', 'devices.router', 'devices.interface', 'devices.credential', 'devices.ip_realm',
    'devices.client_prefix', 'devices.customer', 'devices.customer_kind_change'
  ] LOOP
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY p_tenant ON %s USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())', t);
  END LOOP;
END $$;
-- auth.role: plantillas (tenant_id NULL) visibles para todos; propios solo en su tenant.
ALTER TABLE auth.role ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.role FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.role USING (tenant_id IS NULL OR tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
-- auth.audit_log: un usuario de tenant ve solo su tenant (las lecturas de plataforma usan el rol <svc>_platform).
ALTER TABLE auth.audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.audit_log USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id IS NOT DISTINCT FROM public.horus_current_tenant());
-- Sin RLS (no son de un tenant): auth.tenant, auth."user", auth.platform_role_assignment, auth.permission,
-- auth.role_permission, auth.session, auth.refresh_token, *.outbox. Su acceso lo restringe el módulo auth.
