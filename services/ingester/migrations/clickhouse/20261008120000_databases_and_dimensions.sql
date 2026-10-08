-- I0-13 · Bases flows/dim, usuarios internos y dimensiones (contrato C3:
-- packages/schemas/datastore/v0/clickhouse-contract-v0.sql; docs/database.md §4 y §5).
--
-- ClickHouse no tiene DDL transaccional: cada sentencia es idempotente (IF NOT EXISTS) para que
-- una migración interrumpida se pueda reintentar sin intervención.

-- +goose NO TRANSACTION
-- +goose Up
CREATE DATABASE IF NOT EXISTS flows;
CREATE DATABASE IF NOT EXISTS dim;

-- Definer de las vistas materializadas (SQL SECURITY DEFINER): las MV no dependen del usuario que
-- migra ni del que inserta; el ingester solo necesita INSERT en flows.flows_raw. HOST NONE: nadie
-- puede iniciar sesión con él.
CREATE USER IF NOT EXISTS horus_mv IDENTIFIED WITH no_password HOST NONE;
GRANT SELECT, INSERT ON flows.* TO horus_mv;
GRANT SELECT ON dim.* TO horus_mv;

-- Origen de los diccionarios dim.*_dict: el servidor se consulta a sí mismo. HOST LOCAL y solo
-- SELECT sobre dim.*: sin secreto que repartir y sin acceso desde fuera del host de ClickHouse.
CREATE USER IF NOT EXISTS horus_dict IDENTIFIED WITH no_password HOST LOCAL;
GRANT SELECT ON dim.* TO horus_dict;

-- ---------------------------------------------------------------- dimensiones (escritor único: analytics)
CREATE TABLE IF NOT EXISTS dim.tenant (tenant_id UUID, name String, timezone String, version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY tenant_id;

CREATE TABLE IF NOT EXISTS dim.site (tenant_id UUID, site_id UUID, name String, parent_id UUID, timezone String, version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY (tenant_id, site_id);

CREATE TABLE IF NOT EXISTS dim.router (tenant_id UUID, router_id UUID, site_id UUID, name String, is_primary UInt8, version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY (tenant_id, router_id);

CREATE TABLE IF NOT EXISTS dim.interface (tenant_id UUID, interface_id UUID, router_id UUID, name String, speed_bps UInt64,
    flow_role LowCardinality(String), version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY (tenant_id, interface_id);

-- pii: address y alias identifican a un cliente (retención: storage.md §5, mientras existan datos del cliente).
CREATE TABLE IF NOT EXISTS dim.customer (tenant_id UUID, realm_id UUID, address IPv6, customer_id UUID, site_id UUID,
    kind LowCardinality(String), kind_source LowCardinality(String), alias Nullable(String), status LowCardinality(String),
    reset_at Nullable(DateTime('UTC')), version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY (tenant_id, realm_id, address);

CREATE TABLE IF NOT EXISTS dim.service (service_id UUID, name String, category_id UUID, catalog_version UInt32)
ENGINE = ReplacingMergeTree(catalog_version) ORDER BY service_id;

CREATE TABLE IF NOT EXISTS dim.category (category_id UUID, name String, catalog_version UInt32)
ENGINE = ReplacingMergeTree(catalog_version) ORDER BY category_id;

CREATE TABLE IF NOT EXISTS dim.organization (org_id UUID, name String, country FixedString(2), catalog_version UInt32)
ENGINE = ReplacingMergeTree(catalog_version) ORDER BY org_id;

CREATE TABLE IF NOT EXISTS dim.asn (asn UInt32, org_id UUID, name String, catalog_version UInt32)
ENGINE = ReplacingMergeTree(catalog_version) ORDER BY asn;

CREATE TABLE IF NOT EXISTS dim.watch_port (protocol UInt8, port UInt16, label String, version UInt32)
ENGINE = ReplacingMergeTree(version) ORDER BY (protocol, port);

-- Resolvers DNS propios de cada tenant (docs/database.md §4.1, §6.2.1: dns_flows_isp vs dns_flows_other).
-- No está en el DDL de C3 (que delega en I0-13 "dim.tenant_resolver como diccionario"): v0 lo modela
-- como IPs exactas; si hiciera falta por prefijo, se añade una tabla nueva con IP_TRIE.
CREATE TABLE IF NOT EXISTS dim.tenant_resolver (tenant_id UUID, resolver_ip IPv6, label String, version UInt32, deleted UInt8 DEFAULT 0)
ENGINE = ReplacingMergeTree(version) ORDER BY (tenant_id, resolver_ip);

-- Puertos vigilados por defecto (traffic-model.md §8). detection los edita con versiones mayores.
INSERT INTO dim.watch_port (protocol, port, label, version)
SELECT protocol, port, label, 0
FROM values('protocol UInt8, port UInt16, label String',
    (6, 23, 'telnet'), (6, 2323, 'telnet-alt'), (6, 37215, 'huawei-hg532'), (6, 52869, 'realtek-upnp'),
    (6, 7547, 'tr-069'), (6, 5555, 'adb'), (6, 445, 'smb'), (6, 139, 'netbios'), (6, 6667, 'irc'),
    (6, 6697, 'irc-tls'), (6, 3389, 'rdp'), (6, 1433, 'mssql'), (6, 8291, 'winbox'), (6, 25, 'smtp'))
WHERE (protocol, port) NOT IN (SELECT protocol, port FROM dim.watch_port);

-- ---------------------------------------------------------------- diccionarios (resolución en consulta, §4.1)
-- Clave compuesta con tenant_id en las dimensiones de tenant: dictGet exige conocer el tenant.
CREATE DICTIONARY IF NOT EXISTS dim.tenant_dict (tenant_id UUID, name String, timezone String)
PRIMARY KEY tenant_id
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT tenant_id, name, timezone FROM dim.tenant FINAL WHERE deleted = 0'))
LIFETIME(MIN 60 MAX 300) LAYOUT(COMPLEX_KEY_HASHED());

CREATE DICTIONARY IF NOT EXISTS dim.site_dict (tenant_id UUID, site_id UUID, name String, parent_id UUID, timezone String)
PRIMARY KEY tenant_id, site_id
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT tenant_id, site_id, name, parent_id, timezone FROM dim.site FINAL WHERE deleted = 0'))
LIFETIME(MIN 60 MAX 300) LAYOUT(COMPLEX_KEY_HASHED());

CREATE DICTIONARY IF NOT EXISTS dim.router_dict (tenant_id UUID, router_id UUID, site_id UUID, name String, is_primary UInt8)
PRIMARY KEY tenant_id, router_id
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT tenant_id, router_id, site_id, name, is_primary FROM dim.router FINAL WHERE deleted = 0'))
LIFETIME(MIN 60 MAX 300) LAYOUT(COMPLEX_KEY_HASHED());

CREATE DICTIONARY IF NOT EXISTS dim.interface_dict (tenant_id UUID, interface_id UUID, router_id UUID, name String, speed_bps UInt64, flow_role String)
PRIMARY KEY tenant_id, interface_id
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT tenant_id, interface_id, router_id, name, speed_bps, flow_role FROM dim.interface FINAL WHERE deleted = 0'))
LIFETIME(MIN 60 MAX 300) LAYOUT(COMPLEX_KEY_HASHED());

CREATE DICTIONARY IF NOT EXISTS dim.customer_dict (tenant_id UUID, realm_id UUID, address IPv6, customer_id UUID, site_id UUID,
    kind String, kind_source String, alias Nullable(String), status String)
PRIMARY KEY tenant_id, realm_id, address
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT tenant_id, realm_id, address, customer_id, site_id, kind, kind_source, alias, status FROM dim.customer FINAL WHERE deleted = 0'))
LIFETIME(MIN 60 MAX 300) LAYOUT(COMPLEX_KEY_HASHED());

CREATE DICTIONARY IF NOT EXISTS dim.service_dict (service_id UUID, name String, category_id UUID)
PRIMARY KEY service_id
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT service_id, name, category_id FROM dim.service FINAL'))
LIFETIME(MIN 60 MAX 300) LAYOUT(COMPLEX_KEY_HASHED());

CREATE DICTIONARY IF NOT EXISTS dim.category_dict (category_id UUID, name String)
PRIMARY KEY category_id
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT category_id, name FROM dim.category FINAL'))
LIFETIME(MIN 60 MAX 300) LAYOUT(COMPLEX_KEY_HASHED());

CREATE DICTIONARY IF NOT EXISTS dim.organization_dict (org_id UUID, name String, country String)
PRIMARY KEY org_id
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT org_id, name, toString(country) AS country FROM dim.organization FINAL'))
LIFETIME(MIN 60 MAX 300) LAYOUT(COMPLEX_KEY_HASHED());

CREATE DICTIONARY IF NOT EXISTS dim.asn_dict (asn UInt64, org_id UUID, name String)
PRIMARY KEY asn
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT toUInt64(asn) AS asn, org_id, name FROM dim.asn FINAL'))
LIFETIME(MIN 60 MAX 300) LAYOUT(HASHED());

CREATE DICTIONARY IF NOT EXISTS dim.watch_port_dict (protocol UInt8, port UInt16, label String)
PRIMARY KEY protocol, port
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT protocol, port, label FROM dim.watch_port FINAL'))
LIFETIME(MIN 60 MAX 300) LAYOUT(COMPLEX_KEY_HASHED());

CREATE DICTIONARY IF NOT EXISTS dim.tenant_resolver_dict (tenant_id UUID, resolver_ip IPv6, label String)
PRIMARY KEY tenant_id, resolver_ip
SOURCE(CLICKHOUSE(USER 'horus_dict' QUERY 'SELECT tenant_id, resolver_ip, label FROM dim.tenant_resolver FINAL WHERE deleted = 0'))
LIFETIME(MIN 60 MAX 300) LAYOUT(COMPLEX_KEY_HASHED());

-- +goose Down
-- Solo desarrollo (database.md §3: forward-only en producción). Las bases no se borran: flows
-- guarda la tabla de control de goose.
DROP DICTIONARY IF EXISTS dim.tenant_resolver_dict;
DROP DICTIONARY IF EXISTS dim.watch_port_dict;
DROP DICTIONARY IF EXISTS dim.asn_dict;
DROP DICTIONARY IF EXISTS dim.organization_dict;
DROP DICTIONARY IF EXISTS dim.category_dict;
DROP DICTIONARY IF EXISTS dim.service_dict;
DROP DICTIONARY IF EXISTS dim.customer_dict;
DROP DICTIONARY IF EXISTS dim.interface_dict;
DROP DICTIONARY IF EXISTS dim.router_dict;
DROP DICTIONARY IF EXISTS dim.site_dict;
DROP DICTIONARY IF EXISTS dim.tenant_dict;
DROP TABLE IF EXISTS dim.tenant_resolver;
DROP TABLE IF EXISTS dim.watch_port;
DROP TABLE IF EXISTS dim.asn;
DROP TABLE IF EXISTS dim.organization;
DROP TABLE IF EXISTS dim.category;
DROP TABLE IF EXISTS dim.service;
DROP TABLE IF EXISTS dim.customer;
DROP TABLE IF EXISTS dim.interface;
DROP TABLE IF EXISTS dim.router;
DROP TABLE IF EXISTS dim.site;
DROP TABLE IF EXISTS dim.tenant;
DROP USER IF EXISTS horus_dict;
DROP USER IF EXISTS horus_mv;
