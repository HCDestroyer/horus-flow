-- Switches monitoreados (MikroTik RouterOS/SwOS y Huawei serie S; docs/vendors/mikrotik.md §13, huawei.md §11).
-- Decisión de modelo: un switch es una FILA de devices.router con device_type = 'switch' (no una entidad nueva):
-- reutiliza el estado de alta, la credencial SNMP cifrada (devices.credential), el modo directo, la dirección de
-- gestión única en la plataforma y la prueba de control (snmp_verified), el sondeo del rol snmp y los eventos
-- horus.devices.router.* (con device_type en el payload). Reglas que lo hacen solo de monitoreo:
--   * device_type: 'router' (por defecto: todas las filas existentes) | 'switch'.
--   * switch_platform: 'routeros' (CRS con RouterOS 7, SNMPv3) | 'swos' (SwOS, solo SNMP v1/v2c) | 'vrp' (Huawei serie
--     S); obligatoria en los switches, NULL en los routers y coherente con vendor.
--   * Un switch NUNCA es router principal (is_primary = false), siempre va en modo directo por la red de gestión
--     (connection_mode = 'direct', protection_level = 'unencrypted_private') y no tiene túnel.
--   * sflow_enabled: el switch exporta sFlow (solo vrp). Sus muestras son una fuente SECUNDARIA que nunca llega a
--     flows_raw ni a la atribución (docs/traffic-model.md §13).
--   * devices.credential admite 'snmp_v2c' (comunidad generada por Horus para SwOS, cifrada como las demás).
-- Patrón sin bloqueos: columnas con DEFAULT constante, CHECK NOT VALID validados después e índice CONCURRENTLY.

-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

ALTER TABLE devices.router
  ADD COLUMN IF NOT EXISTS device_type     text NOT NULL DEFAULT 'router',
  ADD COLUMN IF NOT EXISTS switch_platform text,
  ADD COLUMN IF NOT EXISTS sflow_enabled   boolean NOT NULL DEFAULT false;

ALTER TABLE devices.router DROP CONSTRAINT IF EXISTS ck_router__vendor_connection_mode;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__vendor_connection_mode
  CHECK ((device_type = 'switch' AND connection_mode = 'direct')
      OR (device_type = 'router' AND vendor = 'mikrotik' AND connection_mode IN ('wireguard', 'ipsec', 'gre'))
      OR (device_type = 'router' AND vendor = 'huawei' AND connection_mode IN ('ipsec', 'gre', 'direct'))) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__device_type
  CHECK (device_type IN ('router', 'switch')) NOT VALID;
ALTER TABLE devices.router ADD CONSTRAINT ck_router__switch
  CHECK ((device_type = 'router' AND switch_platform IS NULL AND NOT sflow_enabled)
      OR (device_type = 'switch' AND switch_platform IS NOT NULL AND NOT is_primary AND protection_level = 'unencrypted_private'
          AND tunnel_address IS NULL AND routeros_version IS NULL
          AND ((switch_platform IN ('routeros', 'swos') AND vendor = 'mikrotik' AND NOT sflow_enabled)
            OR (switch_platform = 'vrp' AND vendor = 'huawei')))) NOT VALID;

ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__vendor_connection_mode;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__device_type;
ALTER TABLE devices.router VALIDATE CONSTRAINT ck_router__switch;

ALTER TABLE devices.credential DROP CONSTRAINT IF EXISTS ck_credential__kind;
ALTER TABLE devices.credential ADD CONSTRAINT ck_credential__kind
  CHECK (kind IN ('snmp_v3', 'snmp_v2c', 'routeros_api', 'ipsec_psk')) NOT VALID;
ALTER TABLE devices.credential VALIDATE CONSTRAINT ck_credential__kind;

CREATE INDEX CONCURRENTLY IF NOT EXISTS ix_router__site_device_type ON devices.router (tenant_id, site_id, device_type)
  WHERE deleted_at IS NULL;

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS devices.ix_router__site_device_type;
ALTER TABLE devices.credential DROP CONSTRAINT IF EXISTS ck_credential__kind;
-- NOT VALID: las comunidades v2c que existan no se borran al deshacer (quedan fuera del CHECK hasta limpiarlas a mano).
ALTER TABLE devices.credential ADD CONSTRAINT ck_credential__kind CHECK (kind IN ('snmp_v3', 'routeros_api', 'ipsec_psk')) NOT VALID;
ALTER TABLE devices.router
  DROP CONSTRAINT IF EXISTS ck_router__switch,
  DROP CONSTRAINT IF EXISTS ck_router__device_type,
  DROP CONSTRAINT IF EXISTS ck_router__vendor_connection_mode,
  DROP COLUMN IF EXISTS sflow_enabled,
  DROP COLUMN IF EXISTS switch_platform,
  DROP COLUMN IF EXISTS device_type;
-- NOT VALID: los switches que existan (ahora sin device_type) quedan fuera del CHECK hasta darlos de baja a mano.
ALTER TABLE devices.router ADD CONSTRAINT ck_router__vendor_connection_mode
  CHECK ((vendor = 'mikrotik' AND connection_mode IN ('wireguard', 'ipsec', 'gre'))
      OR (vendor = 'huawei' AND connection_mode IN ('ipsec', 'gre', 'direct'))) NOT VALID;
RESET lock_timeout;
