-- I0-06 · Esquema devices (inventario por tenant) traducido del contrato C2
-- (packages/schemas/datastore/v0/postgres-contract-v0.sql): site/nodo, router con un principal por nodo,
-- interface, credential, ip_realm, client_prefix con EXCLUDE sin solapes por realm, customer con clave
-- natural (tenant, realm, address), historial de tipo y outbox. FKs compuestas (tenant_id, id) y RLS
-- fail-closed. Roles del módulo: devices_app (sin BYPASSRLS) y devices_platform (BYPASSRLS).
-- Las tablas son nuevas y vacías: los índices se crean en la misma transacción (sin CONCURRENTLY).

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';
SET LOCAL client_min_messages = warning;

CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.horus_current_tenant() RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT nullif(current_setting('horus.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    CREATE ROLE devices_app NOLOGIN;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
  BEGIN
    CREATE ROLE devices_platform NOLOGIN BYPASSRLS;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
END $$;
-- +goose StatementEnd
GRANT devices_app, devices_platform TO CURRENT_USER;

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
  routeros_version     text CHECK (routeros_version ~ '^7\.[0-9]+(\.[0-9]+)?$'),
  routeros_version_detected text,
  routeros_version_supported boolean,
  tunnel_address       inet,
  mgmt_wireguard_peer_id uuid,
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
CREATE UNIQUE INDEX ux_router__tunnel_address ON devices.router (tunnel_address) WHERE deleted_at IS NULL AND tunnel_address IS NOT NULL;

CREATE TABLE devices.interface (
  id               uuid NOT NULL PRIMARY KEY,
  tenant_id        uuid NOT NULL,
  router_id        uuid NOT NULL,
  if_index         integer,
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

CREATE TABLE devices.credential (
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

CREATE TABLE devices.ip_realm (
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

CREATE TABLE devices.client_prefix (
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
CREATE INDEX ix_client_prefix__site ON devices.client_prefix (tenant_id, site_id) WHERE deleted_at IS NULL;

CREATE TABLE devices.customer (
  id                       uuid NOT NULL PRIMARY KEY,
  tenant_id                uuid NOT NULL,
  realm_id                 uuid NOT NULL,
  address                  inet NOT NULL,
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
  alias                    text,
  alias_source             text CHECK (alias_source IN ('manual', 'routeros_ppp')),
  notes                    text,
  status                   text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
  inactive_reason          text CHECK (inactive_reason IN ('no_traffic', 'prefix_removed')),
  first_seen               timestamptz NOT NULL,
  last_seen                timestamptz NOT NULL,
  reset_at                 timestamptz,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  version                  integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, realm_id, address),
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

CREATE TABLE devices.customer_kind_change (
  id                 uuid NOT NULL PRIMARY KEY,
  tenant_id          uuid NOT NULL,
  customer_id        uuid NOT NULL,
  from_kind          text,
  to_kind            text NOT NULL,
  source             text NOT NULL CHECK (source IN ('default', 'scoring', 'manual', 'reset')),
  reasons            jsonb NOT NULL DEFAULT '[]',
  reason_codes       text[] NOT NULL DEFAULT '{}',
  model_ref          text,
  confidence         smallint,
  actor_id           uuid,
  manual_reason      text,
  causation_event_id uuid,
  changed_at         timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_id) REFERENCES devices.customer (tenant_id, id) ON DELETE CASCADE,
  CONSTRAINT ck_kind_change__manual_reason CHECK (source <> 'manual' OR manual_reason IS NOT NULL)
);
CREATE INDEX ix_kind_change__customer ON devices.customer_kind_change (tenant_id, customer_id, changed_at DESC);

-- Outbox estándar (sin RLS: solo la lee el relay del módulo con devices_platform).
CREATE TABLE devices.outbox (
  id uuid PRIMARY KEY, seq bigint GENERATED ALWAYS AS IDENTITY, tenant_id uuid, subject text NOT NULL,
  aggregate_id uuid NOT NULL, headers jsonb NOT NULL DEFAULT '{}', payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL, published_at timestamptz, attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX ix_outbox__pending ON devices.outbox (seq) WHERE published_at IS NULL;

-- RLS fail-closed (M5).
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'devices.site', 'devices.router', 'devices.interface', 'devices.credential', 'devices.ip_realm',
    'devices.client_prefix', 'devices.customer', 'devices.customer_kind_change'
  ] LOOP
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY p_tenant ON %s USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA devices TO devices_app, devices_platform;
GRANT SELECT, INSERT, UPDATE, DELETE ON
  devices.site, devices.router, devices.interface, devices.credential, devices.ip_realm,
  devices.client_prefix, devices.customer, devices.outbox
  TO devices_app, devices_platform;
-- Historial inmutable: solo INSERT/SELECT.
GRANT SELECT, INSERT ON devices.customer_kind_change TO devices_app, devices_platform;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA devices TO devices_app, devices_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS devices.outbox;
DROP TABLE IF EXISTS devices.customer_kind_change;
DROP TABLE IF EXISTS devices.customer;
DROP TABLE IF EXISTS devices.client_prefix;
DROP TABLE IF EXISTS devices.ip_realm;
DROP TABLE IF EXISTS devices.credential;
DROP TABLE IF EXISTS devices.interface;
DROP TABLE IF EXISTS devices.router;
DROP TABLE IF EXISTS devices.site;
REVOKE ALL ON SCHEMA devices FROM devices_app, devices_platform;
-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    DROP ROLE IF EXISTS devices_app;
  EXCEPTION WHEN dependent_objects_still_exist OR object_in_use THEN NULL;
  END;
  BEGIN
    DROP ROLE IF EXISTS devices_platform;
  EXCEPTION WHEN dependent_objects_still_exist OR object_in_use THEN NULL;
  END;
END $$;
-- +goose StatementEnd
