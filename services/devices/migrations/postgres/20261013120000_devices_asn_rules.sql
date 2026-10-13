-- ASN propios del ISP: regla ASN → nombre, categoría y servicio que flows aplica al clasificar el
-- tráfico nuevo (classification_method = local_override, docs/traffic-model.md §6). Una regla viva
-- por (tenant, asn); baja lógica. Los slugs de categoría y servicio son del catálogo de plataforma
-- (los valida la aplicación: el catálogo no vive en PostgreSQL). RLS fail-closed como el resto del
-- esquema. Tabla nueva y vacía: índices en la misma transacción.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE devices.asn_rule (
  id            uuid NOT NULL PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  asn           bigint NOT NULL CONSTRAINT ck_asn_rule__asn CHECK (asn BETWEEN 1 AND 4294967295),
  name          text NOT NULL CONSTRAINT ck_asn_rule__name CHECK (char_length(name) BETWEEN 1 AND 80),
  category_slug text NOT NULL CONSTRAINT ck_asn_rule__category CHECK (char_length(category_slug) BETWEEN 1 AND 64),
  service_slug  text CONSTRAINT ck_asn_rule__service CHECK (char_length(service_slug) BETWEEN 1 AND 64),
  note          text CONSTRAINT ck_asn_rule__note CHECK (char_length(note) <= 300),
  created_by    uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  deleted_at    timestamptz,
  version       integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX ux_asn_rule__tenant_asn ON devices.asn_rule (tenant_id, asn) WHERE deleted_at IS NULL;

ALTER TABLE devices.asn_rule ENABLE ROW LEVEL SECURITY;
ALTER TABLE devices.asn_rule FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON devices.asn_rule
  USING (tenant_id = public.horus_current_tenant()) WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON devices.asn_rule TO devices_app, devices_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS devices.asn_rule;
