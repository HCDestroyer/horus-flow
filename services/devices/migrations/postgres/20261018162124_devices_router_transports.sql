-- Hub de túneles multitransporte (docs/tunnels.md, ADR-0031; decisión del PO del 2026-10-17): cada router usa el
-- mejor transporte estándar que admite y su nivel de protección queda a la vista.
--   * connection_mode admite 'ipsec' (IKEv2 route-based, RFC 7296) y 'gre' (RFC 2784, sin cifrado) además de
--     'wireguard' y 'direct'. MikroTik: wireguard | ipsec | gre. Huawei VRP: ipsec | gre | direct.
--   * protection_level: 'encrypted' (wireguard, ipsec) | 'unencrypted_private' (gre/direct por un camino que el
--     administrador declara privado: MPLS L3VPN, VRF de gestión, fibra propia) | 'unencrypted_internet' (gre/direct
--     por Internet, con confirmación explícita, re-autenticación y auditoría). Los routers directos existentes
--     pasan a 'unencrypted_private': el alta directa exigía un camino privado (docs/vendors/huawei.md §5.2).
--   * tunnel_remote_address: IPv4 pública del router (origen IKE/GRE; obligatoria en gre: el hub solo acepta GRE de
--     esa IP). tunnel_remote_id: identidad IKE del router (ipsec). Únicas en toda la plataforma entre routers vivos:
--     el kernel distingue los túneles GRE por IP remota y charon elige la conexión por identidad.
--   * devices.credential admite 'ipsec_psk' (PSK IKEv2 por router, cifrada con la KEK de devices).
-- Patrón sin bloqueos: columnas con DEFAULT constante, CHECK NOT VALID validados después e índices CONCURRENTLY.

-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

ALTER TABLE devices.router
  ADD COLUMN IF NOT EXISTS protection_level      text NOT NULL DEFAULT 'encrypted',
  ADD COLUMN IF NOT EXISTS tunnel_remote_address inet,
  ADD COLUMN IF NOT EXISTS tunnel_remote_id      text;

UPDATE devices.router SET protection_level = 'unencrypted_private' WHERE connection_mode = 'direct' AND protection_level = 'encrypted';

ALTER TABLE devices.router
  DROP CONSTRAINT IF EXISTS ck_router__huawei_direct,
  DROP CONSTRAINT IF EXISTS ck_router__connection_mode;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__connection_mode
  CHECK (connection_mode IN ('wireguard', 'ipsec', 'gre', 'direct')) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__vendor_connection_mode
  CHECK ((vendor = 'mikrotik' AND connection_mode IN ('wireguard', 'ipsec', 'gre'))
      OR (vendor = 'huawei' AND connection_mode IN ('ipsec', 'gre', 'direct'))) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__protection_level
  CHECK (protection_level IN ('encrypted', 'unencrypted_private', 'unencrypted_internet')
     AND ((connection_mode IN ('wireguard', 'ipsec')) = (protection_level = 'encrypted'))) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__tunnel_remote
  CHECK ((connection_mode IN ('ipsec', 'gre') OR (tunnel_remote_address IS NULL AND tunnel_remote_id IS NULL))
     AND (connection_mode <> 'gre' OR (tunnel_remote_address IS NOT NULL AND tunnel_remote_id IS NULL))
     AND (connection_mode <> 'ipsec' OR tunnel_remote_id IS NOT NULL)
     AND (tunnel_remote_address IS NULL OR (family(tunnel_remote_address) = 4 AND masklen(tunnel_remote_address) = 32))
     AND (tunnel_remote_id IS NULL OR length(tunnel_remote_id) BETWEEN 1 AND 253)) NOT VALID;

ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__connection_mode;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__vendor_connection_mode;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__protection_level;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__tunnel_remote;

ALTER TABLE devices.credential DROP CONSTRAINT IF EXISTS credential_kind_check;
ALTER TABLE devices.credential ADD CONSTRAINT ck_credential__kind CHECK (kind IN ('snmp_v3', 'routeros_api', 'ipsec_psk')) NOT VALID;
ALTER TABLE devices.credential VALIDATE CONSTRAINT ck_credential__kind;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux_router__gre_remote_address ON devices.router (tunnel_remote_address)
  WHERE deleted_at IS NULL AND connection_mode = 'gre';
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ux_router__ipsec_remote_id ON devices.router (tunnel_remote_id)
  WHERE deleted_at IS NULL AND connection_mode = 'ipsec';

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS devices.ux_router__ipsec_remote_id;
DROP INDEX CONCURRENTLY IF EXISTS devices.ux_router__gre_remote_address;
ALTER TABLE devices.credential DROP CONSTRAINT IF EXISTS ck_credential__kind;
-- NOT VALID: las PSK que existan no se borran al deshacer (quedan fuera del CHECK hasta limpiarlas a mano).
ALTER TABLE devices.credential ADD CONSTRAINT credential_kind_check CHECK (kind IN ('snmp_v3', 'routeros_api')) NOT VALID;
ALTER TABLE devices.router
  DROP CONSTRAINT IF EXISTS ck_router__tunnel_remote,
  DROP CONSTRAINT IF EXISTS ck_router__protection_level,
  DROP CONSTRAINT IF EXISTS ck_router__vendor_connection_mode,
  DROP CONSTRAINT IF EXISTS ck_router__connection_mode,
  DROP COLUMN IF EXISTS tunnel_remote_id,
  DROP COLUMN IF EXISTS tunnel_remote_address,
  DROP COLUMN IF EXISTS protection_level;
-- NOT VALID: los routers ipsec/gre que existan quedan fuera del CHECK hasta pasarlos a wireguard/direct a mano.
ALTER TABLE devices.router ADD CONSTRAINT ck_router__connection_mode CHECK (connection_mode IN ('wireguard', 'direct')) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__huawei_direct CHECK (vendor <> 'huawei' OR connection_mode = 'direct') NOT VALID;
RESET lock_timeout;
