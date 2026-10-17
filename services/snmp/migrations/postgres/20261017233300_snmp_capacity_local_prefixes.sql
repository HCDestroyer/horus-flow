-- Lista local por prefijos (docs/capacity-forecast.md §2): además de los ASN, prefijos CIDR IPv4/IPv6 por ISP (LAN de
-- un IXP, cachés on-net) que el reparto local / internacional cuenta como locales (flows.site_remote_net_1d). Hasta 200
-- por ISP; la aplicación los normaliza y deduplica. Columna con valor por defecto constante: ALTER sin reescritura.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE snmp.capacity_settings
  ADD COLUMN IF NOT EXISTS local_prefixes cidr[] NOT NULL DEFAULT '{}';
ALTER TABLE snmp.capacity_settings ADD CONSTRAINT ck_capacity_settings__local_prefixes
  CHECK (cardinality(local_prefixes) <= 200) NOT VALID;
ALTER TABLE snmp.capacity_settings VALIDATE CONSTRAINT ck_capacity_settings__local_prefixes;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE snmp.capacity_settings DROP CONSTRAINT IF EXISTS ck_capacity_settings__local_prefixes;
ALTER TABLE snmp.capacity_settings DROP COLUMN IF EXISTS local_prefixes;
