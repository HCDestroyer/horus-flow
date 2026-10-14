-- I2 · Servicios de infraestructura del ISP (IPTV, HTTP, DNS, RADIUS…): inventario por tenant, estado de
-- cada chequeo (histéresis, últimos resultados) y outbox de horus.snmp.service.state_changed. Primer
-- esquema PostgreSQL del rol snmp (el estado de los routers sigue en NATS KV). RLS por tenant,
-- fail-closed. Sin FK a otros esquemas: site_id y probe_via_router_id son UUID sueltos (database.md M2).

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL client_min_messages = warning;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.horus_current_tenant() RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT nullif(current_setting('horus.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    CREATE ROLE snmp_app NOLOGIN;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
  BEGIN
    CREATE ROLE snmp_platform NOLOGIN BYPASSRLS;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
END $$;
-- +goose StatementEnd
GRANT snmp_app, snmp_platform TO CURRENT_USER;

CREATE TABLE snmp.infra_service (
  id                    uuid PRIMARY KEY,
  tenant_id             uuid NOT NULL,
  name                  text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
  site_id               uuid,
  target                inet NOT NULL,
  kind                  text NOT NULL CHECK (kind ~ '^[a-z0-9][a-z0-9_-]{0,31}$'),
  checks                jsonb NOT NULL CHECK (jsonb_typeof(checks) = 'array'),
  interval_seconds      integer NOT NULL DEFAULT 60 CHECK (interval_seconds BETWEEN 30 AND 3600),
  failures_before_down  integer NOT NULL DEFAULT 3 CHECK (failures_before_down BETWEEN 1 AND 20),
  recoveries_before_up  integer NOT NULL DEFAULT 2 CHECK (recoveries_before_up BETWEEN 1 AND 20),
  enabled               boolean NOT NULL DEFAULT true,
  probe_via_router_id   uuid,
  -- Estado del servicio (el peor de sus chequeos conocidos); lo escribe el sondeador, no cambia version.
  state                 text NOT NULL DEFAULT 'unknown' CHECK (state IN ('up', 'down', 'degraded', 'unknown')),
  state_reason          text,
  state_check_type      text,
  state_since           timestamptz,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  version               integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CONSTRAINT ck_infra_service__target_host CHECK (masklen(target) = CASE WHEN family(target) = 4 THEN 32 ELSE 128 END)
);
CREATE UNIQUE INDEX ux_infra_service__tenant_name ON snmp.infra_service (tenant_id, lower(name));
CREATE INDEX ix_infra_service__tenant_site ON snmp.infra_service (tenant_id, site_id);

CREATE TABLE snmp.infra_check_state (
  tenant_id              uuid NOT NULL,
  service_id             uuid NOT NULL,
  check_id               uuid NOT NULL,
  check_type             text NOT NULL CHECK (check_type IN ('icmp', 'tcp', 'http', 'no_traffic', 'ddos_inbound')),
  state                  text NOT NULL DEFAULT 'unknown' CHECK (state IN ('up', 'down', 'degraded', 'unknown')),
  reason                 text,
  detail                 text,
  latency_ms             double precision,
  consecutive_failures   integer NOT NULL DEFAULT 0,
  consecutive_successes  integer NOT NULL DEFAULT 0,
  -- no_traffic: el objetivo tuvo tráfico alguna vez (sin eso no se alerta: hosts ociosos).
  had_traffic            boolean NOT NULL DEFAULT false,
  since                  timestamptz,
  last_checked_at        timestamptz,
  -- Últimos resultados (acotado por la aplicación), el más reciente primero.
  recent                 jsonb NOT NULL DEFAULT '[]',
  PRIMARY KEY (service_id, check_id),
  FOREIGN KEY (tenant_id, service_id) REFERENCES snmp.infra_service (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX ix_infra_check_state__tenant ON snmp.infra_check_state (tenant_id, service_id);

CREATE TABLE snmp.outbox (
  id uuid PRIMARY KEY, seq bigint GENERATED ALWAYS AS IDENTITY, tenant_id uuid, subject text NOT NULL,
  aggregate_id uuid NOT NULL, headers jsonb NOT NULL DEFAULT '{}', payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL, published_at timestamptz, attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX ix_outbox__pending ON snmp.outbox (seq) WHERE published_at IS NULL;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['snmp.infra_service', 'snmp.infra_check_state'] LOOP
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY p_tenant ON %s USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA snmp TO snmp_app, snmp_platform;
GRANT SELECT, INSERT, UPDATE, DELETE ON snmp.infra_service, snmp.infra_check_state, snmp.outbox TO snmp_app, snmp_platform;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA snmp TO snmp_app, snmp_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS snmp.outbox;
DROP TABLE IF EXISTS snmp.infra_check_state;
DROP TABLE IF EXISTS snmp.infra_service;
REVOKE ALL ON SCHEMA snmp FROM snmp_app, snmp_platform;
