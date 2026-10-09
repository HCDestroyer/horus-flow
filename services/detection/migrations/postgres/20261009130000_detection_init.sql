-- I1-10 / I1-12 · Esquema detection (docs/database.md §2.7, ADR-0024): hallazgos (C8) con
-- ciclo de vida, retroalimentación humana (verdict_feedback), allowlist del ISP, parámetros de
-- detectores por ISP, estado de seguridad por cliente (D5/D18) y outbox. RLS fail-closed en todo
-- lo que es de un tenant. Roles: detection_app (sin BYPASSRLS) y detection_platform (BYPASSRLS:
-- relay del outbox y planificador del motor, que solo enumera tenants).

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
    CREATE ROLE detection_app NOLOGIN;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
  BEGIN
    CREATE ROLE detection_platform NOLOGIN BYPASSRLS;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
END $$;
-- +goose StatementEnd
GRANT detection_app, detection_platform TO CURRENT_USER;

-- Hallazgo (C8). address es dato personal (IP del cliente): solo la API la devuelve, con
-- customers.read; nunca va en eventos. Clave de deduplicación: (tenant, cliente, kind, objetivo
-- principal) con un solo hallazgo activo (open | acknowledged).
CREATE TABLE detection.finding (
  id                          uuid NOT NULL PRIMARY KEY,
  tenant_id                   uuid NOT NULL,
  version                     integer NOT NULL DEFAULT 1,
  state                       text NOT NULL CHECK (state IN ('open', 'acknowledged', 'resolved', 'false_positive')),
  kind                        text NOT NULL CHECK (kind ~ '^[a-z][a-z0-9_]*$'),
  category                    text NOT NULL DEFAULT 'security',
  severity                    text NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
  confidence                  double precision NOT NULL CHECK (confidence BETWEEN 0 AND 1),
  customer_id                 uuid NOT NULL,
  realm_id                    uuid NOT NULL,
  site_id                     uuid NOT NULL,
  router_id                   uuid NOT NULL,
  address                     inet NOT NULL,
  customer_kind               text NOT NULL DEFAULT 'residential',
  target_type                 text NOT NULL CHECK (target_type IN ('remote_ip', 'remote_port', 'remote_asn', 'remote_prefix', 'none')),
  target_value                text NOT NULL DEFAULT '',
  signals                     text[] NOT NULL DEFAULT '{}',
  summary                     jsonb NOT NULL,
  reasons                     jsonb NOT NULL,
  evidence                    jsonb NOT NULL DEFAULT '{}',
  window_from                 timestamptz NOT NULL,
  window_to                   timestamptz NOT NULL,
  first_seen_at               timestamptz NOT NULL,
  last_seen_at                timestamptz NOT NULL,
  opened_at                   timestamptz NOT NULL,
  updated_at                  timestamptz NOT NULL,
  occurrences                 integer NOT NULL DEFAULT 1 CHECK (occurrences >= 1),
  rule_version                text NOT NULL,
  reputation_snapshot_version integer,
  min_sampling_rate           integer,
  sampling_reduced_confidence boolean NOT NULL DEFAULT false,
  previous_finding_id         uuid,
  acknowledged_by             uuid,
  acknowledged_at             timestamptz,
  resolution                  jsonb,
  resolved_at                 timestamptz,
  silence_until               timestamptz,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, previous_finding_id) REFERENCES detection.finding (tenant_id, id)
);
CREATE UNIQUE INDEX ux_finding__active ON detection.finding (tenant_id, customer_id, kind, target_type, target_value)
  WHERE state IN ('open', 'acknowledged');
CREATE INDEX ix_finding__pattern ON detection.finding (tenant_id, customer_id, kind, target_type, target_value, updated_at DESC);
CREATE INDEX ix_finding__list ON detection.finding (tenant_id, state, opened_at DESC);
CREATE INDEX ix_finding__customer ON detection.finding (tenant_id, customer_id, opened_at DESC);
CREATE INDEX ix_finding__site ON detection.finding (tenant_id, site_id) WHERE state IN ('open', 'acknowledged');

-- Retroalimentación humana (inmutable): base para recalibrar umbrales (traffic-model.md §8).
CREATE TABLE detection.verdict_feedback (
  id            uuid NOT NULL PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  finding_id    uuid NOT NULL,
  customer_id   uuid NOT NULL,
  kind          text NOT NULL,
  rule_version  text NOT NULL,
  verdict       text NOT NULL CHECK (verdict IN ('resolved', 'false_positive')),
  comment       text,
  silence_until timestamptz,
  actions_taken text[] NOT NULL DEFAULT '{}',
  actor_id      uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, finding_id) REFERENCES detection.finding (tenant_id, id)
);
CREATE INDEX ix_verdict__finding ON detection.verdict_feedback (tenant_id, finding_id);

-- Allowlist del ISP (D20: listas propias del ISP que NO deben generar hallazgos): prefijo o ASN,
-- limitada opcionalmente a unos kind. Se aplica al cliente y al destino principal.
CREATE TABLE detection.reputation_allowlist (
  id         uuid NOT NULL PRIMARY KEY,
  tenant_id  uuid NOT NULL,
  prefix     cidr,
  asn        bigint CHECK (asn BETWEEN 1 AND 4294967295),
  kinds      text[] NOT NULL DEFAULT '{}',
  reason     text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 300),
  created_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid NOT NULL,
  deleted_at timestamptz,
  UNIQUE (tenant_id, id),
  CONSTRAINT ck_allowlist__target CHECK ((prefix IS NULL) <> (asn IS NULL))
);
CREATE INDEX ix_allowlist__tenant ON detection.reputation_allowlist (tenant_id, created_at) WHERE deleted_at IS NULL;

-- Parámetros por ISP de cada detector (JSON que se fusiona sobre los valores por defecto).
CREATE TABLE detection.detector_config (
  tenant_id  uuid NOT NULL,
  detector   text NOT NULL CHECK (detector ~ '^[a-z][a-z0-9_]*$'),
  enabled    boolean NOT NULL DEFAULT true,
  params     jsonb NOT NULL DEFAULT '{}',
  version    integer NOT NULL DEFAULT 1,
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid,
  PRIMARY KEY (tenant_id, detector)
);

-- Estado de seguridad por cliente (D5/D18), proyectado a devices por el evento
-- horus.detection.customer.security_state_changed. Guarda razones y confianza del estado.
CREATE TABLE detection.customer_security (
  tenant_id     uuid NOT NULL,
  customer_id   uuid NOT NULL,
  site_id       uuid NOT NULL,
  state         text NOT NULL CHECK (state IN ('clean', 'suspected', 'infected', 'mitigated')),
  version       integer NOT NULL DEFAULT 1,
  open_findings integer NOT NULL DEFAULT 0,
  top_kind      text,
  max_severity  text,
  confidence    double precision,
  reasons       jsonb NOT NULL DEFAULT '[]',
  since         timestamptz NOT NULL,
  updated_at    timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, customer_id)
);
CREATE INDEX ix_customer_security__state ON detection.customer_security (tenant_id, state);

-- Estado del motor por tenant (último barrido retroactivo por versión de snapshot…).
CREATE TABLE detection.engine_state (
  tenant_id  uuid NOT NULL,
  key        text NOT NULL,
  value      jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, key)
);

-- Tenants conocidos por el motor (planificador): se registran al primer acceso de la API o por
-- configuración; no contiene datos del tenant (sin RLS, solo lo lee detection_platform).
CREATE TABLE detection.tenant_registry (
  tenant_id     uuid PRIMARY KEY,
  registered_at timestamptz NOT NULL DEFAULT now()
);

-- Outbox estándar (sin RLS: solo la lee el relay del módulo con detection_platform).
CREATE TABLE detection.outbox (
  id uuid PRIMARY KEY, seq bigint GENERATED ALWAYS AS IDENTITY, tenant_id uuid, subject text NOT NULL,
  aggregate_id uuid NOT NULL, headers jsonb NOT NULL DEFAULT '{}', payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL, published_at timestamptz, attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX ix_outbox__pending ON detection.outbox (seq) WHERE published_at IS NULL;

-- RLS fail-closed (M5).
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'detection.finding', 'detection.verdict_feedback', 'detection.reputation_allowlist',
    'detection.detector_config', 'detection.customer_security', 'detection.engine_state'
  ] LOOP
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY p_tenant ON %s USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant())', t);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA detection TO detection_app, detection_platform;
GRANT SELECT, INSERT, UPDATE ON detection.finding, detection.customer_security, detection.engine_state,
  detection.detector_config, detection.reputation_allowlist TO detection_app, detection_platform;
GRANT DELETE ON detection.detector_config, detection.engine_state TO detection_app, detection_platform;
-- Retroalimentación inmutable: solo INSERT/SELECT.
GRANT SELECT, INSERT ON detection.verdict_feedback TO detection_app, detection_platform;
GRANT SELECT, INSERT ON detection.outbox TO detection_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON detection.outbox TO detection_platform;
GRANT SELECT, INSERT ON detection.tenant_registry TO detection_app, detection_platform;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA detection TO detection_app, detection_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS detection.outbox;
DROP TABLE IF EXISTS detection.tenant_registry;
DROP TABLE IF EXISTS detection.engine_state;
DROP TABLE IF EXISTS detection.customer_security;
DROP TABLE IF EXISTS detection.detector_config;
DROP TABLE IF EXISTS detection.reputation_allowlist;
DROP TABLE IF EXISTS detection.verdict_feedback;
DROP TABLE IF EXISTS detection.finding;
REVOKE ALL ON SCHEMA detection FROM detection_app, detection_platform;
-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    DROP ROLE IF EXISTS detection_app;
  EXCEPTION WHEN dependent_objects_still_exist OR object_in_use THEN NULL;
  END;
  BEGIN
    DROP ROLE IF EXISTS detection_platform;
  EXCEPTION WHEN dependent_objects_still_exist OR object_in_use THEN NULL;
  END;
END $$;
-- +goose StatementEnd
