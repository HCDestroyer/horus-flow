-- Switches vía router del sitio (ADR-0032, docs/tunnels.md §10): un equipo de monitoreo con IP de gestión PRIVADA
-- se alcanza a través del túnel de un router del sitio sin meter nunca esa IP privada en el hub. Cada equipo recibe
-- una DIRECCIÓN HORUS de la misma IPAM que las IP de túnel (única en la plataforma, cuarentena de 24 h) y el router
-- hace NAT 1:1 (netmap) entre esa dirección y la IP privada. El hub enruta la dirección Horus por el túnel del router
-- (allowed-ips del peer WireGuard, ruta + ipset en hxfrm<n>/hgre<n>) y la usa como identidad del equipo (SNMP y
-- sFlow). Las IP privadas repetidas entre ISP o entre routers de un ISP nunca chocan: solo existen en la regla NAT
-- del router de cada sitio.
--   * via_mapping: kind 'device' (un switch: dirección Horus /32) o 'subnet' (red de gestión declarada en el router:
--     bloque Horus del mismo tamaño, alineado). Un switch cuya IP cae en una red declarada usa la dirección del bloque
--     (parent_id = la fila 'subnet', sin asignación propia).
--   * target: IP privada (/32) o red de gestión; mapped: dirección o bloque Horus (mismo tamaño).
--   * Una IP privada no se repite por router dentro de un ISP (el NAT del router sería ambiguo); las redes declaradas
--     de un router no se solapan; las direcciones Horus de primer nivel no se solapan en toda la plataforma.
--   * ip_allocation: una dirección es de un peer (IP de túnel) o de un via_mapping (dirección Horus), nunca de los dos.
-- RLS fail-closed por tenant; la IPAM y el estado deseado del hub los lee wireguard_platform (BYPASSRLS).

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE wireguard.via_mapping (
  id          uuid NOT NULL PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  server_id   uuid NOT NULL REFERENCES wireguard.server (id),
  router_id   uuid NOT NULL,
  kind        text NOT NULL,
  device_id   uuid,
  subnet_id   uuid,
  parent_id   uuid,
  target      cidr NOT NULL,
  mapped      cidr NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  released_at timestamptz,
  version     integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CONSTRAINT ck_via_mapping__kind CHECK (
    (kind = 'device' AND device_id IS NOT NULL AND (parent_id IS NULL) = (subnet_id IS NULL)
       AND masklen(target) = 32 AND masklen(mapped) = 32)
    OR (kind = 'subnet' AND subnet_id IS NOT NULL AND device_id IS NULL AND parent_id IS NULL
       AND masklen(target) BETWEEN 22 AND 30)),
  CONSTRAINT ck_via_mapping__family CHECK (family(target) = 4 AND family(mapped) = 4 AND masklen(target) = masklen(mapped)),
  CONSTRAINT ck_via_mapping__not_cgnat CHECK (NOT (mapped && '100.64.0.0/10'::cidr))
);
-- Un switch tiene una sola dirección Horus viva; una red declarada, un solo bloque.
CREATE UNIQUE INDEX ux_via_mapping__device ON wireguard.via_mapping (tenant_id, device_id) WHERE released_at IS NULL AND kind = 'device';
CREATE UNIQUE INDEX ux_via_mapping__subnet ON wireguard.via_mapping (tenant_id, subnet_id) WHERE released_at IS NULL AND kind = 'subnet';
-- La misma IP privada no puede ser de dos switches a través del mismo router (el netmap sería ambiguo).
CREATE UNIQUE INDEX ux_via_mapping__router_target ON wireguard.via_mapping (tenant_id, router_id, target)
  WHERE released_at IS NULL AND kind = 'device';
-- Las redes declaradas de un router no se solapan.
ALTER TABLE wireguard.via_mapping ADD CONSTRAINT ex_via_mapping__router_subnet
  EXCLUDE USING gist (tenant_id WITH =, router_id WITH =, (target::inet) inet_ops WITH &&)
  WHERE (released_at IS NULL AND kind = 'subnet');
-- Las direcciones y bloques Horus de primer nivel no se solapan en toda la plataforma (cryptokey routing).
ALTER TABLE wireguard.via_mapping ADD CONSTRAINT ex_via_mapping__mapped
  EXCLUDE USING gist ((mapped::inet) inet_ops WITH &&) WHERE (released_at IS NULL AND parent_id IS NULL);
CREATE INDEX ix_via_mapping__router ON wireguard.via_mapping (server_id, tenant_id, router_id) WHERE released_at IS NULL;

ALTER TABLE wireguard.ip_allocation
  ALTER COLUMN peer_id DROP NOT NULL,
  ADD COLUMN via_mapping_id uuid,
  ADD CONSTRAINT ck_ip_allocation__owner CHECK ((peer_id IS NULL) <> (via_mapping_id IS NULL));
CREATE INDEX ix_ip_allocation__via_mapping ON wireguard.ip_allocation (tenant_id, via_mapping_id) WHERE via_mapping_id IS NOT NULL;

ALTER TABLE wireguard.via_mapping ENABLE ROW LEVEL SECURITY;
ALTER TABLE wireguard.via_mapping FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON wireguard.via_mapping USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT SELECT, INSERT, UPDATE ON wireguard.via_mapping TO wireguard_app, wireguard_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- Las direcciones Horus se liberan y sus asignaciones se borran (no hay peer al que volver a atarlas).
DELETE FROM wireguard.ip_allocation WHERE via_mapping_id IS NOT NULL;
ALTER TABLE wireguard.ip_allocation
  DROP CONSTRAINT IF EXISTS ck_ip_allocation__owner,
  DROP COLUMN IF EXISTS via_mapping_id;
ALTER TABLE wireguard.ip_allocation ALTER COLUMN peer_id SET NOT NULL;
DROP TABLE IF EXISTS wireguard.via_mapping;
