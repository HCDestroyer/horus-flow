-- Puertos monitoreados de los switches (docs/vendors/mikrotik.md §13, huawei.md §11). El operador elige qué puertos
-- del switch se sondean cada intervalo (máximo 64 por switch: cardinalidad de snmp.interface_*) y cuáles son de
-- subida (entran en el análisis de capacidad y avisan si caen o se vuelven inestables). Sin fila en
-- switch_port_selection rige la elección por defecto (uplinks sugeridos por LLDP + puertos con descripción).
-- switch_port_selection lleva la versión (If-Match) y quién cambió la elección; switch_port, un registro por puerto
-- elegido (clave: nombre IF-MIB, del que deriva interface_id = uuid5(router_id, ifName)). RLS forzada por tenant;
-- el sondeador carga todas las elecciones con el rol de plataforma (multi-tenant declarado).

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL client_min_messages = warning;

CREATE TABLE snmp.switch_port_selection (
  tenant_id  uuid NOT NULL,
  router_id  uuid NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by text,
  version    integer NOT NULL DEFAULT 1,
  PRIMARY KEY (tenant_id, router_id)
);

CREATE TABLE snmp.switch_port (
  tenant_id    uuid NOT NULL,
  router_id    uuid NOT NULL,
  name         text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
  interface_id uuid NOT NULL,
  uplink       boolean NOT NULL DEFAULT false,
  PRIMARY KEY (tenant_id, router_id, name),
  FOREIGN KEY (tenant_id, router_id) REFERENCES snmp.switch_port_selection (tenant_id, router_id) ON DELETE CASCADE
);

ALTER TABLE snmp.switch_port_selection ENABLE ROW LEVEL SECURITY;
ALTER TABLE snmp.switch_port_selection FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON snmp.switch_port_selection USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
ALTER TABLE snmp.switch_port ENABLE ROW LEVEL SECURITY;
ALTER TABLE snmp.switch_port FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON snmp.switch_port USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT SELECT, INSERT, UPDATE, DELETE ON snmp.switch_port_selection, snmp.switch_port TO snmp_app, snmp_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS snmp.switch_port;
DROP TABLE IF EXISTS snmp.switch_port_selection;
