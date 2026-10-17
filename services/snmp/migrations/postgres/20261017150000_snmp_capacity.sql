-- Capacidad de enlaces de subida (NOC): configuración por enlace (proveedor, tipo, capacidad contratada, salida
-- internacional agregada), ajustes de capacidad por ISP, estado sostenido de las condiciones vigiladas (saturación,
-- pérdida, latencia; deduplica las alertas) y último pronóstico por enlace y por salida internacional. Las series
-- (sondas, picos diarios) viven en ClickHouse (base snmp). RLS forzada por tenant; el monitor y el trabajo diario
-- cargan con el rol de plataforma (multi-tenant declarado).

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL client_min_messages = warning;

CREATE TABLE snmp.uplink_config (
  tenant_id            uuid NOT NULL,
  interface_id         uuid NOT NULL,
  router_id            uuid NOT NULL,
  label                text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 120),
  provider             text CHECK (provider IS NULL OR char_length(provider) BETWEEN 1 AND 120),
  kind                 text NOT NULL CHECK (kind IN ('international_transit', 'local_ix', 'cache', 'other')),
  contracted_bps       bigint CHECK (contracted_bps IS NULL OR contracted_bps BETWEEN 1000000 AND 1600000000000),
  international_egress boolean NOT NULL DEFAULT false,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  updated_by           text,
  version              integer NOT NULL DEFAULT 1,
  PRIMARY KEY (tenant_id, interface_id)
);
CREATE INDEX ix_uplink_config__router ON snmp.uplink_config (tenant_id, router_id);

CREATE TABLE snmp.capacity_settings (
  tenant_id           uuid PRIMARY KEY,
  country             char(2) CHECK (country IS NULL OR country ~ '^[A-Z]{2}$'),
  planning_threshold  double precision NOT NULL CHECK (planning_threshold BETWEEN 0.3 AND 1),
  urgent_threshold    double precision NOT NULL CHECK (urgent_threshold BETWEEN 0.3 AND 1),
  lead_days           integer NOT NULL CHECK (lead_days BETWEEN 7 AND 365),
  saturation_warning  double precision NOT NULL CHECK (saturation_warning BETWEEN 0.3 AND 1),
  saturation_critical double precision NOT NULL CHECK (saturation_critical BETWEEN 0.3 AND 1),
  sustained_minutes   integer NOT NULL CHECK (sustained_minutes BETWEEN 5 AND 240),
  loss_warning_pct    double precision NOT NULL CHECK (loss_warning_pct > 0 AND loss_warning_pct <= 100),
  loss_critical_pct   double precision NOT NULL CHECK (loss_critical_pct > 0 AND loss_critical_pct <= 100),
  latency_warning_ms  double precision NOT NULL CHECK (latency_warning_ms > 0),
  latency_critical_ms double precision NOT NULL CHECK (latency_critical_ms > 0 AND latency_critical_ms <= 10000),
  local_asns          bigint[] NOT NULL DEFAULT '{}' CHECK (cardinality(local_asns) <= 200),
  probe_targets       jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(probe_targets) = 'array'),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  updated_by          text,
  version             integer NOT NULL DEFAULT 1,
  CONSTRAINT ck_capacity_settings__order CHECK (planning_threshold <= urgent_threshold
    AND saturation_warning <= saturation_critical AND loss_warning_pct <= loss_critical_pct
    AND latency_warning_ms <= latency_critical_ms)
);

-- Estado sostenido de cada condición vigilada de un enlace: solo un cambio de nivel emite
-- horus.snmp.uplink.condition_changed (por el outbox, en la misma transacción).
CREATE TABLE snmp.uplink_condition (
  tenant_id     uuid NOT NULL,
  interface_id  uuid NOT NULL,
  condition     text NOT NULL CHECK (condition IN ('saturation', 'loss', 'latency')),
  router_id     uuid NOT NULL,
  level         text NOT NULL CHECK (level IN ('ok', 'warning', 'critical')),
  since         timestamptz NOT NULL,
  value         double precision NOT NULL,
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, interface_id, condition)
);

-- Último pronóstico por sujeto (enlace o salida internacional agregada). El nivel deduplica
-- horus.snmp.capacity.forecast_changed.
CREATE TABLE snmp.capacity_forecast (
  tenant_id    uuid NOT NULL,
  subject_id   uuid NOT NULL,
  subject_kind text NOT NULL CHECK (subject_kind IN ('uplink', 'international_egress')),
  site_id      uuid,
  level        text NOT NULL CHECK (level IN ('ok', 'planning', 'urgent', 'insufficient_data', 'no_capacity')),
  computed_at  timestamptz NOT NULL,
  forecast     jsonb NOT NULL,
  PRIMARY KEY (tenant_id, subject_id)
);

ALTER TABLE snmp.uplink_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE snmp.uplink_config FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON snmp.uplink_config USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
ALTER TABLE snmp.capacity_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE snmp.capacity_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON snmp.capacity_settings USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
ALTER TABLE snmp.uplink_condition ENABLE ROW LEVEL SECURITY;
ALTER TABLE snmp.uplink_condition FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON snmp.uplink_condition USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
ALTER TABLE snmp.capacity_forecast ENABLE ROW LEVEL SECURITY;
ALTER TABLE snmp.capacity_forecast FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON snmp.capacity_forecast USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON snmp.uplink_config, snmp.capacity_settings, snmp.uplink_condition,
  snmp.capacity_forecast TO snmp_app, snmp_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS snmp.capacity_forecast;
DROP TABLE IF EXISTS snmp.uplink_condition;
DROP TABLE IF EXISTS snmp.capacity_settings;
DROP TABLE IF EXISTS snmp.uplink_config;
