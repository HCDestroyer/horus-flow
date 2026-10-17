-- Segundo fabricante: Huawei VRP (docs/vendors/huawei.md, roadmap I6). VRP no tiene WireGuard, así que el
-- router se conecta en modo "direct": sin túnel con el hub de Horus, por un camino que asegura el operador
-- (VRF de gestión, VPN de sitio existente o relé MikroTik/Linux en el sitio; la tecnología de túnel para
-- Huawei es una decisión pendiente del PO, docs/vendors/huawei.md §5).
--   * vendor admite 'huawei'.
--   * connection_mode: 'wireguard' (MikroTik, el comportamiento de siempre) | 'direct'. Huawei siempre direct.
--   * management_address: en modo directo, la IP de origen del router tal y como la ve Horus. Es el destino del
--     sondeo SNMP y la identidad del exportador de flujos (lista de permitidos por IP de origen del colector).
--   * onboarding_state 'snmp_verified' (modo directo): el sondeador respondió con la credencial SNMPv3 emitida para
--     ESE router en esa dirección (prueba de control). Solo entonces el colector acepta sus flujos y la dirección
--     pasa a ser única en toda la plataforma (índice parcial): un ISP no puede apropiarse de la IP de otro.
-- La tabla es pequeña, pero se sigue el patrón sin bloqueos: columna con DEFAULT constante (instantánea),
-- CHECK NOT VALID validados después e índice CONCURRENTLY fuera de transacción.

-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

ALTER TABLE devices.router
  ADD COLUMN IF NOT EXISTS connection_mode    text NOT NULL DEFAULT 'wireguard',
  ADD COLUMN IF NOT EXISTS management_address inet;

ALTER TABLE devices.router DROP CONSTRAINT IF EXISTS router_vendor_check;
ALTER TABLE devices.router DROP CONSTRAINT IF EXISTS router_onboarding_state_check;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__onboarding_state
  CHECK (onboarding_state IN ('pending_configuration', 'key_received', 'tunnel_up', 'snmp_verified', 'exporting')) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__vendor
  CHECK (vendor IN ('mikrotik', 'huawei')) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__connection_mode
  CHECK (connection_mode IN ('wireguard', 'direct')) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__huawei_direct
  CHECK (vendor <> 'huawei' OR connection_mode = 'direct') NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__management_address
  CHECK (management_address IS NULL OR (connection_mode = 'direct' AND masklen(management_address) = CASE WHEN family(management_address) = 4 THEN 32 ELSE 128 END)) NOT VALID;

ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__onboarding_state;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__vendor;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__connection_mode;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__huawei_direct;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__management_address;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux_router__management_address ON devices.router (management_address)
  WHERE deleted_at IS NULL AND management_address IS NOT NULL AND onboarding_state IN ('snmp_verified', 'exporting');

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS devices.ux_router__management_address;
ALTER TABLE devices.router
  DROP CONSTRAINT IF EXISTS ck_router__management_address,
  DROP CONSTRAINT IF EXISTS ck_router__huawei_direct,
  DROP CONSTRAINT IF EXISTS ck_router__connection_mode,
  DROP CONSTRAINT IF EXISTS ck_router__vendor,
  DROP CONSTRAINT IF EXISTS ck_router__onboarding_state,
  DROP COLUMN IF EXISTS management_address,
  DROP COLUMN IF EXISTS connection_mode;
-- NOT VALID: los routers Huawei que existan no se borran al deshacer (quedan fuera del CHECK hasta limpiarlos a mano).
ALTER TABLE devices.router ADD CONSTRAINT router_vendor_check CHECK (vendor IN ('mikrotik')) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT router_onboarding_state_check
  CHECK (onboarding_state IN ('pending_configuration', 'key_received', 'tunnel_up', 'exporting')) NOT VALID;
RESET lock_timeout;
