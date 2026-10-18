-- Switches con IP de gestión privada: unicidad por ISP y modo «vía router del sitio» (ADR-0032, docs/tunnels.md §10,
-- decisión del PO del 2026-10-18).
--   * management_address deja de ser única en TODA la plataforma: dos ISP (o dos sitios) reutilizan la misma IP
--     privada (192.168.88.2). Pasa a ser única por ISP y por camino:
--       - direct: (tenant_id, management_address) entre equipos verificados (como antes, pero por ISP). Dos ISP con la
--         misma dirección directa verificada no se confunden en el colector: el inventario de flujos deja fuera una
--         dirección directa repetida (flowinv) y la prueba de control exige la credencial SNMPv3 del propio equipo.
--       - via_router: (tenant_id, via_router_id, management_address) entre equipos vivos (el NAT del router sería
--         ambiguo con dos switches en la misma IP).
--     Sigue sin poder ser una IP de Horus (túneles, instalación): lo comprueba la API.
--   * connection_mode admite 'via_router' (solo switches): el switch conserva su IP privada, no tiene túnel y Horus lo
--     alcanza por el túnel de un router del sitio del MISMO ISP (via_router_id). via_address es su dirección Horus
--     (IPAM de túneles de wireguard, única en la plataforma): destino del sondeo SNMP e identidad del exportador sFlow.
--     El router hace NAT 1:1 entre via_address y management_address. protection_level del switch sigue siendo el de su
--     último tramo (red de gestión privada); la consola muestra además el del túnel del router.
--   * management_subnet: redes de gestión declaradas en un router del sitio (una regla NAT por red en lugar de una por
--     switch). No se solapan dentro del mismo router de un ISP (EXCLUDE); RLS por tenant.
-- Patrón sin bloqueos: columnas sin DEFAULT, CHECK/FK NOT VALID validados después e índices CONCURRENTLY.

-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

ALTER TABLE devices.router
  ADD COLUMN IF NOT EXISTS via_router_id uuid,
  ADD COLUMN IF NOT EXISTS via_address   inet;

ALTER TABLE devices.router DROP CONSTRAINT IF EXISTS ck_router__connection_mode;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__connection_mode
  CHECK (connection_mode IN ('wireguard', 'ipsec', 'gre', 'direct', 'via_router')) NOT VALID;
ALTER TABLE devices.router DROP CONSTRAINT IF EXISTS ck_router__vendor_connection_mode;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__vendor_connection_mode
  CHECK ((device_type = 'switch' AND connection_mode IN ('direct', 'via_router'))
      OR (device_type = 'router' AND vendor = 'mikrotik' AND connection_mode IN ('wireguard', 'ipsec', 'gre'))
      OR (device_type = 'router' AND vendor = 'huawei' AND connection_mode IN ('ipsec', 'gre', 'direct'))) NOT VALID;
ALTER TABLE devices.router DROP CONSTRAINT IF EXISTS ck_router__management_address;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__management_address
  CHECK (management_address IS NULL OR (connection_mode IN ('direct', 'via_router')
     AND masklen(management_address) = CASE WHEN family(management_address) = 4 THEN 32 ELSE 128 END)) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__via
  CHECK ((connection_mode = 'via_router') = (via_router_id IS NOT NULL)
     AND (via_address IS NULL OR (connection_mode = 'via_router' AND family(via_address) = 4 AND masklen(via_address) = 32))
     AND (via_router_id IS NULL OR via_router_id <> id)
     AND (connection_mode <> 'via_router' OR management_address IS NULL OR family(management_address) = 4)) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT fk_router__via_router
  FOREIGN KEY (tenant_id, via_router_id) REFERENCES devices.router (tenant_id, id) NOT VALID;

ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__connection_mode;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__vendor_connection_mode;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__management_address;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__via;
ALTER TABLE devices.router VALIDATE CONSTRAINT fk_router__via_router;

-- Unicidad por ISP y camino (primero los índices nuevos, después se quita el de plataforma).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux_router__management_address_direct
  ON devices.router (tenant_id, management_address)
  WHERE deleted_at IS NULL AND management_address IS NOT NULL AND connection_mode = 'direct'
    AND onboarding_state IN ('snmp_verified', 'exporting');
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux_router__management_address_via
  ON devices.router (tenant_id, via_router_id, management_address)
  WHERE deleted_at IS NULL AND management_address IS NOT NULL AND connection_mode = 'via_router';
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux_router__via_address ON devices.router (via_address)
  WHERE deleted_at IS NULL AND via_address IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS ix_router__via_router ON devices.router (tenant_id, via_router_id)
  WHERE deleted_at IS NULL AND via_router_id IS NOT NULL;
DROP INDEX CONCURRENTLY IF EXISTS devices.ux_router__management_address;

CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE IF NOT EXISTS devices.management_subnet (
  id         uuid NOT NULL PRIMARY KEY,
  tenant_id  uuid NOT NULL,
  router_id  uuid NOT NULL,
  prefix     cidr NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  created_by text,
  UNIQUE (tenant_id, id),
  CONSTRAINT fk_management_subnet__router FOREIGN KEY (tenant_id, router_id) REFERENCES devices.router (tenant_id, id),
  CONSTRAINT ck_management_subnet__prefix CHECK (family(prefix) = 4 AND masklen(prefix) BETWEEN 22 AND 30),
  CONSTRAINT ex_management_subnet__overlap EXCLUDE USING gist (tenant_id WITH =, router_id WITH =, (prefix::inet) inet_ops WITH &&)
);
CREATE INDEX IF NOT EXISTS ix_management_subnet__router ON devices.management_subnet (tenant_id, router_id);

ALTER TABLE devices.management_subnet ENABLE ROW LEVEL SECURITY;
ALTER TABLE devices.management_subnet FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS p_tenant ON devices.management_subnet;
CREATE POLICY p_tenant ON devices.management_subnet USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
GRANT SELECT, INSERT, DELETE ON devices.management_subnet TO devices_app, devices_platform;

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP TABLE IF EXISTS devices.management_subnet;
-- Los switches vía router vuelven a modo directo (sin camino: hay que pegar su alta de nuevo).
UPDATE devices.router SET connection_mode = 'direct', via_router_id = NULL, via_address = NULL,
  onboarding_state = 'pending_configuration', version = version + 1
  WHERE connection_mode = 'via_router';
-- NOT VALID: si dos ISP comparten una dirección directa verificada, la unicidad de plataforma no se puede recrear
-- sin limpiar a mano; el índice se crea solo si no hay duplicados.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux_router__management_address ON devices.router (management_address)
  WHERE deleted_at IS NULL AND management_address IS NOT NULL AND onboarding_state IN ('snmp_verified', 'exporting');
DROP INDEX CONCURRENTLY IF EXISTS devices.ix_router__via_router;
DROP INDEX CONCURRENTLY IF EXISTS devices.ux_router__via_address;
DROP INDEX CONCURRENTLY IF EXISTS devices.ux_router__management_address_via;
DROP INDEX CONCURRENTLY IF EXISTS devices.ux_router__management_address_direct;
ALTER TABLE devices.router
  DROP CONSTRAINT IF EXISTS fk_router__via_router,
  DROP CONSTRAINT IF EXISTS ck_router__via,
  DROP CONSTRAINT IF EXISTS ck_router__management_address,
  DROP CONSTRAINT IF EXISTS ck_router__vendor_connection_mode,
  DROP CONSTRAINT IF EXISTS ck_router__connection_mode,
  DROP COLUMN IF EXISTS via_address,
  DROP COLUMN IF EXISTS via_router_id;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__connection_mode
  CHECK (connection_mode IN ('wireguard', 'ipsec', 'gre', 'direct')) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__vendor_connection_mode
  CHECK ((device_type = 'switch' AND connection_mode = 'direct')
      OR (device_type = 'router' AND vendor = 'mikrotik' AND connection_mode IN ('wireguard', 'ipsec', 'gre'))
      OR (device_type = 'router' AND vendor = 'huawei' AND connection_mode IN ('ipsec', 'gre', 'direct'))) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__management_address
  CHECK (management_address IS NULL OR (connection_mode = 'direct' AND masklen(management_address) = CASE WHEN family(management_address) = 4 THEN 32 ELSE 128 END)) NOT VALID;
RESET lock_timeout;
