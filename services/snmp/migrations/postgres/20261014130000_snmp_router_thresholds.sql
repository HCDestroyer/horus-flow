-- Umbrales de los routers por ISP (CPU, memoria y temperatura, aviso y crítico) editables desde la consola. Sin fila
-- el ISP usa los de la instalación (HORUS_SNMP_*_WARNING/_CRITICAL). El sondeador los carga con el rol de
-- plataforma (todas las tenants) y la API los lee y escribe con RLS por tenant.

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

CREATE TABLE snmp.router_thresholds (
  tenant_id            uuid PRIMARY KEY,
  cpu_warning          double precision NOT NULL CHECK (cpu_warning BETWEEN 1 AND 100),
  cpu_critical         double precision NOT NULL CHECK (cpu_critical BETWEEN 1 AND 100),
  memory_warning       double precision NOT NULL CHECK (memory_warning BETWEEN 1 AND 100),
  memory_critical      double precision NOT NULL CHECK (memory_critical BETWEEN 1 AND 100),
  temperature_warning  double precision NOT NULL CHECK (temperature_warning BETWEEN 20 AND 150),
  temperature_critical double precision NOT NULL CHECK (temperature_critical BETWEEN 20 AND 150),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  updated_by           text,
  version              integer NOT NULL DEFAULT 1,
  CONSTRAINT ck_router_thresholds__order CHECK (cpu_warning <= cpu_critical AND memory_warning <= memory_critical
    AND temperature_warning <= temperature_critical)
);

ALTER TABLE snmp.router_thresholds ENABLE ROW LEVEL SECURITY;
ALTER TABLE snmp.router_thresholds FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON snmp.router_thresholds USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT USAGE ON SCHEMA snmp TO snmp_app, snmp_platform;
GRANT SELECT, INSERT, UPDATE, DELETE ON snmp.router_thresholds TO snmp_app, snmp_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS snmp.router_thresholds;
REVOKE ALL ON SCHEMA snmp FROM snmp_app, snmp_platform;
