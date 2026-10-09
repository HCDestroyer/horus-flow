-- I1-01 · Esquema wireguard (docs/database.md §2.4): hub de plataforma, IPAM de túnel (pools sin solapes,
-- asignaciones /32 únicas en toda la plataforma con cuarentena de 24 h), peers por tenant y tokens de
-- enrolamiento (solo hash). RLS fail-closed en las tablas con tenant_id. Roles: wireguard_app (sin BYPASSRLS)
-- y wireguard_platform (BYPASSRLS: IPAM global, enrolamiento público por hash, reportes del agente).

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';
SET LOCAL client_min_messages = warning;

CREATE EXTENSION IF NOT EXISTS btree_gist;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.horus_current_tenant() RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT nullif(current_setting('horus.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    CREATE ROLE wireguard_app NOLOGIN;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
  BEGIN
    CREATE ROLE wireguard_platform NOLOGIN BYPASSRLS;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
END $$;
-- +goose StatementEnd
GRANT wireguard_app, wireguard_platform TO CURRENT_USER;

-- Hub de plataforma (sin tenant_id: compartido por todos los ISP en el I1). La clave privada vive solo en
-- wg-agent; aquí solo la pública.
CREATE TABLE wireguard.server (
  id              uuid NOT NULL PRIMARY KEY,
  name            text NOT NULL UNIQUE,
  interface_name  text NOT NULL DEFAULT 'wg0',
  endpoint        text NOT NULL,
  listen_port     integer NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
  public_key      text,
  services_cidr   cidr NOT NULL,
  status          text NOT NULL DEFAULT 'down' CHECK (status IN ('up', 'down', 'degraded')),
  desired_version bigint NOT NULL DEFAULT 1,
  applied_version bigint NOT NULL DEFAULT 0,
  last_report_at  timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE wireguard.ip_pool (
  id         uuid NOT NULL PRIMARY KEY,
  server_id  uuid NOT NULL REFERENCES wireguard.server (id),
  cidr       cidr NOT NULL,
  family     smallint NOT NULL DEFAULT 4 CHECK (family IN (4, 6)),
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT ck_ip_pool__not_cgnat CHECK (NOT (cidr && '100.64.0.0/10'::cidr)),
  CONSTRAINT ex_ip_pool__no_overlap EXCLUDE USING gist ((cidr::inet) inet_ops WITH &&)
);

CREATE TABLE wireguard.ip_allocation (
  id           uuid NOT NULL PRIMARY KEY,
  pool_id      uuid NOT NULL REFERENCES wireguard.ip_pool (id),
  tenant_id    uuid NOT NULL,
  address      inet NOT NULL CHECK (masklen(address) IN (32, 128)),
  peer_id      uuid NOT NULL,
  allocated_at timestamptz NOT NULL DEFAULT now(),
  released_at  timestamptz,
  UNIQUE (tenant_id, id)
);
-- Única en toda la plataforma (identidad del exportador).
CREATE UNIQUE INDEX ux_ip_allocation__address ON wireguard.ip_allocation (address) WHERE released_at IS NULL;
CREATE INDEX ix_ip_allocation__pool ON wireguard.ip_allocation (pool_id, address);

CREATE TABLE wireguard.peer (
  id                 uuid NOT NULL PRIMARY KEY,
  tenant_id          uuid NOT NULL,
  server_id          uuid NOT NULL REFERENCES wireguard.server (id),
  kind               text NOT NULL DEFAULT 'router' CHECK (kind IN ('router', 'operator', 'service')),
  router_id          uuid NOT NULL,
  address            inet NOT NULL,
  public_key         text CHECK (public_key ~ '^[A-Za-z0-9+/]{42}[AEIMQUYcgkosw480]=$'),
  status             text NOT NULL DEFAULT 'awaiting_enrollment'
                      CHECK (status IN ('awaiting_enrollment', 'pending_handshake', 'active', 'revoked')),
  handshake_state    text NOT NULL DEFAULT 'never' CHECK (handshake_state IN ('never', 'ok', 'stale')),
  persistent_keepalive_seconds integer NOT NULL DEFAULT 25,
  enrolled_at        timestamptz,
  enrolled_from_ip   inet,
  activated_at       timestamptz,
  revoked_at         timestamptz,
  revoked_reason     text,
  last_handshake_at  timestamptz,
  endpoint           text,
  rx_bytes           numeric(20, 0) NOT NULL DEFAULT 0,
  tx_bytes           numeric(20, 0) NOT NULL DEFAULT 0,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  version            integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CONSTRAINT ck_peer__revoked CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);
-- Una clave pública no puede estar en dos peers vivos (de ningún ISP).
CREATE UNIQUE INDEX ux_peer__public_key ON wireguard.peer (public_key) WHERE revoked_at IS NULL AND public_key IS NOT NULL;
CREATE UNIQUE INDEX ux_peer__router ON wireguard.peer (tenant_id, router_id) WHERE revoked_at IS NULL;
CREATE INDEX ix_peer__handshake ON wireguard.peer (tenant_id, last_handshake_at);

CREATE TABLE wireguard.enrollment_token (
  id              uuid NOT NULL PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  router_id       uuid NOT NULL,
  peer_id         uuid NOT NULL,
  token_hash      bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  expires_at      timestamptz NOT NULL,
  used_at         timestamptz,
  revoked_at      timestamptz,
  failed_attempts integer NOT NULL DEFAULT 0,
  created_by      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, peer_id) REFERENCES wireguard.peer (tenant_id, id)
);
CREATE INDEX ix_enrollment_token__router ON wireguard.enrollment_token (tenant_id, router_id) WHERE used_at IS NULL AND revoked_at IS NULL;

-- Outbox estándar (sin RLS: solo la lee el relay del módulo con wireguard_platform).
CREATE TABLE wireguard.outbox (
  id uuid PRIMARY KEY, seq bigint GENERATED ALWAYS AS IDENTITY, tenant_id uuid, subject text NOT NULL,
  aggregate_id uuid NOT NULL, headers jsonb NOT NULL DEFAULT '{}', payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL, published_at timestamptz, attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX ix_outbox__pending ON wireguard.outbox (seq) WHERE published_at IS NULL;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['wireguard.ip_allocation', 'wireguard.peer', 'wireguard.enrollment_token'] LOOP
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY p_tenant ON %s USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA wireguard TO wireguard_app, wireguard_platform;
GRANT SELECT ON wireguard.server, wireguard.ip_pool TO wireguard_app;
GRANT SELECT, INSERT, UPDATE ON wireguard.server, wireguard.ip_pool TO wireguard_platform;
GRANT SELECT, INSERT, UPDATE ON wireguard.ip_allocation, wireguard.peer, wireguard.enrollment_token TO wireguard_app, wireguard_platform;
GRANT SELECT, INSERT, UPDATE, DELETE ON wireguard.outbox TO wireguard_app, wireguard_platform;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA wireguard TO wireguard_app, wireguard_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS wireguard.outbox;
DROP TABLE IF EXISTS wireguard.enrollment_token;
DROP TABLE IF EXISTS wireguard.peer;
DROP TABLE IF EXISTS wireguard.ip_allocation;
DROP TABLE IF EXISTS wireguard.ip_pool;
DROP TABLE IF EXISTS wireguard.server;
REVOKE ALL ON SCHEMA wireguard FROM wireguard_app, wireguard_platform;
-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    DROP ROLE IF EXISTS wireguard_app;
  EXCEPTION WHEN dependent_objects_still_exist OR object_in_use THEN NULL;
  END;
  BEGIN
    DROP ROLE IF EXISTS wireguard_platform;
  EXCEPTION WHEN dependent_objects_still_exist OR object_in_use THEN NULL;
  END;
END $$;
-- +goose StatementEnd
