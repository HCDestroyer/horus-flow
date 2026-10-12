-- Administración del hub WireGuard desde la consola (PATCH /platform/wireguard/hubs/{id} y rangos de túnel).
--  * mtu y persistent_keepalive_seconds: valores por defecto de los scripts de alta nuevos (los routers ya
--    conectados conservan los suyos hasta un script nuevo).
--  * version: ETag/If-Match del hub (sube con cada edición y con cada rango añadido o quitado).
--  * console_overrides: campos editados desde la consola; el arranque (HORUS_WG_*) ya no los sobrescribe.
--  * Quitar un rango sin direcciones: DELETE en ip_pool y en las asignaciones liberadas fuera de cuarentena.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE wireguard.server
  ADD COLUMN mtu integer NOT NULL DEFAULT 1420,
  ADD COLUMN persistent_keepalive_seconds integer NOT NULL DEFAULT 25,
  ADD COLUMN version integer NOT NULL DEFAULT 1,
  ADD COLUMN console_overrides text[] NOT NULL DEFAULT '{}',
  ADD CONSTRAINT ck_server__mtu CHECK (mtu BETWEEN 1280 AND 1500),
  ADD CONSTRAINT ck_server__keepalive CHECK (persistent_keepalive_seconds BETWEEN 0 AND 120),
  ADD CONSTRAINT ck_server__console_overrides
    CHECK (console_overrides <@ ARRAY['name', 'endpoint', 'mtu', 'persistent_keepalive_seconds']::text[]);

GRANT DELETE ON wireguard.ip_pool, wireguard.ip_allocation TO wireguard_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
REVOKE DELETE ON wireguard.ip_pool, wireguard.ip_allocation FROM wireguard_platform;
ALTER TABLE wireguard.server
  DROP CONSTRAINT IF EXISTS ck_server__console_overrides,
  DROP CONSTRAINT IF EXISTS ck_server__keepalive,
  DROP CONSTRAINT IF EXISTS ck_server__mtu,
  DROP COLUMN IF EXISTS console_overrides,
  DROP COLUMN IF EXISTS version,
  DROP COLUMN IF EXISTS persistent_keepalive_seconds,
  DROP COLUMN IF EXISTS mtu;
