-- I0-13 · Roles por módulo, lectores y row policies por tenant (ADR-0017 §5; docs/database.md §5.1;
-- docs/architecture.md §6.3-6.4). Escritor único por tabla; lectores con política por tenant.
--
-- Roles (sin credenciales: los usuarios con contraseña los crea `chmigrate.Provision` desde los
-- secretos del despliegue y les asigna estos roles por defecto):
--   horus_ingester_role   INSERT en flows.flows_raw (los agregados los escriben las MV como horus_mv).
--   horus_analytics_role  escritor de dim.* + lector por tenant.
--   horus_detection_role  lector por tenant (detection.* llega en I2).
--   horus_alerts_role     lector por tenant.
--   horus_jobs_role       lector de plataforma (multi-tenant, sin política) + flows_coverage + purgas.
--   horus_tenant_reader   SELECT en las tablas publicadas, filtrado por la política p_tenant.
--   horus_platform_reader SELECT sin filtro (política p_platform), solo procesos de plataforma.
--
-- Política por tenant: tenant_id = toUUID(getSetting('SQL_horus_tenant')). El ajuste lo fija el
-- query builder en cada consulta (SETTINGS SQL_horus_tenant = '<uuid>'); si falta, getSetting falla
-- (UNKNOWN_SETTING) y la consulta se rechaza: fail-closed. Las políticas son permisivas y se
-- combinan con OR: ningún usuario debe tener a la vez horus_tenant_reader y horus_platform_reader.

-- +goose NO TRANSACTION
-- +goose Up
CREATE ROLE IF NOT EXISTS horus_tenant_reader;
CREATE ROLE IF NOT EXISTS horus_platform_reader;
CREATE ROLE IF NOT EXISTS horus_ingester_role;
CREATE ROLE IF NOT EXISTS horus_analytics_role;
CREATE ROLE IF NOT EXISTS horus_detection_role;
CREATE ROLE IF NOT EXISTS horus_alerts_role;
CREATE ROLE IF NOT EXISTS horus_jobs_role;

-- Lectores. Grants tabla a tabla (no flows.*): leer una MV leería su destino como su definer.
GRANT SELECT ON flows.flows_raw TO horus_tenant_reader;
GRANT SELECT ON flows.customer_5m TO horus_tenant_reader;
GRANT SELECT ON flows.customer_1h TO horus_tenant_reader;
GRANT SELECT ON flows.customer_1d TO horus_tenant_reader;
GRANT SELECT ON flows.site_5m TO horus_tenant_reader;
GRANT SELECT ON flows.site_1h TO horus_tenant_reader;
GRANT SELECT ON flows.site_1d TO horus_tenant_reader;
GRANT SELECT ON flows.unattributed_1h TO horus_tenant_reader;
GRANT SELECT ON flows.client_security_1m TO horus_tenant_reader;
GRANT SELECT ON flows.client_security_1h TO horus_tenant_reader;
GRANT SELECT ON flows.client_port_1m TO horus_tenant_reader;
GRANT SELECT ON flows.reputation_hit TO horus_tenant_reader;
GRANT SELECT ON flows.flows_coverage TO horus_tenant_reader;
GRANT SELECT ON dim.tenant TO horus_tenant_reader;
GRANT SELECT ON dim.site TO horus_tenant_reader;
GRANT SELECT ON dim.router TO horus_tenant_reader;
GRANT SELECT ON dim.interface TO horus_tenant_reader;
GRANT SELECT ON dim.customer TO horus_tenant_reader;
GRANT SELECT ON dim.tenant_resolver TO horus_tenant_reader;
GRANT SELECT ON dim.service TO horus_tenant_reader;
GRANT SELECT ON dim.category TO horus_tenant_reader;
GRANT SELECT ON dim.organization TO horus_tenant_reader;
GRANT SELECT ON dim.asn TO horus_tenant_reader;
GRANT SELECT ON dim.watch_port TO horus_tenant_reader;
GRANT dictGet ON dim.* TO horus_tenant_reader;
GRANT SELECT ON flows.flows_raw TO horus_platform_reader;
GRANT SELECT ON flows.customer_5m TO horus_platform_reader;
GRANT SELECT ON flows.customer_1h TO horus_platform_reader;
GRANT SELECT ON flows.customer_1d TO horus_platform_reader;
GRANT SELECT ON flows.site_5m TO horus_platform_reader;
GRANT SELECT ON flows.site_1h TO horus_platform_reader;
GRANT SELECT ON flows.site_1d TO horus_platform_reader;
GRANT SELECT ON flows.unattributed_1h TO horus_platform_reader;
GRANT SELECT ON flows.client_security_1m TO horus_platform_reader;
GRANT SELECT ON flows.client_security_1h TO horus_platform_reader;
GRANT SELECT ON flows.client_port_1m TO horus_platform_reader;
GRANT SELECT ON flows.reputation_hit TO horus_platform_reader;
GRANT SELECT ON flows.flows_coverage TO horus_platform_reader;
GRANT SELECT ON dim.tenant TO horus_platform_reader;
GRANT SELECT ON dim.site TO horus_platform_reader;
GRANT SELECT ON dim.router TO horus_platform_reader;
GRANT SELECT ON dim.interface TO horus_platform_reader;
GRANT SELECT ON dim.customer TO horus_platform_reader;
GRANT SELECT ON dim.tenant_resolver TO horus_platform_reader;
GRANT SELECT ON dim.service TO horus_platform_reader;
GRANT SELECT ON dim.category TO horus_platform_reader;
GRANT SELECT ON dim.organization TO horus_platform_reader;
GRANT SELECT ON dim.asn TO horus_platform_reader;
GRANT SELECT ON dim.watch_port TO horus_platform_reader;
GRANT dictGet ON dim.* TO horus_platform_reader;

-- Escritores (un escritor por tabla, architecture.md §6.3).
GRANT INSERT ON flows.flows_raw TO horus_ingester_role;
GRANT INSERT, ALTER DELETE ON dim.* TO horus_analytics_role;
GRANT horus_tenant_reader TO horus_analytics_role;
GRANT horus_tenant_reader TO horus_detection_role;
GRANT horus_tenant_reader TO horus_alerts_role;
GRANT horus_platform_reader TO horus_jobs_role;
GRANT INSERT ON flows.flows_coverage TO horus_jobs_role;
-- Offboarding y retención por tenant (database.md §5.1, §10): mutaciones/lightweight deletes.
GRANT ALTER DELETE ON flows.* TO horus_jobs_role;

-- Row policies: p_tenant filtra a los lectores de tenant; p_platform deja ver todo a los procesos de
-- plataforma y a los usuarios internos (definer de las MV, origen de los diccionarios).
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.flows_raw AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.customer_5m AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.customer_1h AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.customer_1d AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.site_5m AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.site_1h AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.site_1d AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.unattributed_1h AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.client_security_1m AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.client_security_1h AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.client_port_1m AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.reputation_hit AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON flows.flows_coverage AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON dim.tenant AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON dim.site AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON dim.router AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON dim.interface AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON dim.customer AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_tenant ON dim.tenant_resolver AS PERMISSIVE FOR SELECT USING tenant_id = toUUID(getSetting('SQL_horus_tenant')) TO horus_tenant_reader;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.flows_raw AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.customer_5m AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.customer_1h AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.customer_1d AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.site_5m AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.site_1h AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.site_1d AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.unattributed_1h AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.client_security_1m AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.client_security_1h AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.client_port_1m AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.reputation_hit AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON flows.flows_coverage AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON dim.tenant AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON dim.site AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON dim.router AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON dim.interface AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON dim.customer AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;
CREATE ROW POLICY IF NOT EXISTS p_platform ON dim.tenant_resolver AS PERMISSIVE FOR SELECT USING 1 TO horus_platform_reader, horus_mv, horus_dict;

-- +goose Down
DROP ROW POLICY IF EXISTS p_platform ON dim.tenant_resolver;
DROP ROW POLICY IF EXISTS p_tenant ON dim.tenant_resolver;
DROP ROW POLICY IF EXISTS p_platform ON dim.customer;
DROP ROW POLICY IF EXISTS p_tenant ON dim.customer;
DROP ROW POLICY IF EXISTS p_platform ON dim.interface;
DROP ROW POLICY IF EXISTS p_tenant ON dim.interface;
DROP ROW POLICY IF EXISTS p_platform ON dim.router;
DROP ROW POLICY IF EXISTS p_tenant ON dim.router;
DROP ROW POLICY IF EXISTS p_platform ON dim.site;
DROP ROW POLICY IF EXISTS p_tenant ON dim.site;
DROP ROW POLICY IF EXISTS p_platform ON dim.tenant;
DROP ROW POLICY IF EXISTS p_tenant ON dim.tenant;
DROP ROW POLICY IF EXISTS p_platform ON flows.flows_coverage;
DROP ROW POLICY IF EXISTS p_tenant ON flows.flows_coverage;
DROP ROW POLICY IF EXISTS p_platform ON flows.reputation_hit;
DROP ROW POLICY IF EXISTS p_tenant ON flows.reputation_hit;
DROP ROW POLICY IF EXISTS p_platform ON flows.client_port_1m;
DROP ROW POLICY IF EXISTS p_tenant ON flows.client_port_1m;
DROP ROW POLICY IF EXISTS p_platform ON flows.client_security_1h;
DROP ROW POLICY IF EXISTS p_tenant ON flows.client_security_1h;
DROP ROW POLICY IF EXISTS p_platform ON flows.client_security_1m;
DROP ROW POLICY IF EXISTS p_tenant ON flows.client_security_1m;
DROP ROW POLICY IF EXISTS p_platform ON flows.unattributed_1h;
DROP ROW POLICY IF EXISTS p_tenant ON flows.unattributed_1h;
DROP ROW POLICY IF EXISTS p_platform ON flows.site_1d;
DROP ROW POLICY IF EXISTS p_tenant ON flows.site_1d;
DROP ROW POLICY IF EXISTS p_platform ON flows.site_1h;
DROP ROW POLICY IF EXISTS p_tenant ON flows.site_1h;
DROP ROW POLICY IF EXISTS p_platform ON flows.site_5m;
DROP ROW POLICY IF EXISTS p_tenant ON flows.site_5m;
DROP ROW POLICY IF EXISTS p_platform ON flows.customer_1d;
DROP ROW POLICY IF EXISTS p_tenant ON flows.customer_1d;
DROP ROW POLICY IF EXISTS p_platform ON flows.customer_1h;
DROP ROW POLICY IF EXISTS p_tenant ON flows.customer_1h;
DROP ROW POLICY IF EXISTS p_platform ON flows.customer_5m;
DROP ROW POLICY IF EXISTS p_tenant ON flows.customer_5m;
DROP ROW POLICY IF EXISTS p_platform ON flows.flows_raw;
DROP ROW POLICY IF EXISTS p_tenant ON flows.flows_raw;
DROP ROLE IF EXISTS horus_jobs_role;
DROP ROLE IF EXISTS horus_alerts_role;
DROP ROLE IF EXISTS horus_detection_role;
DROP ROLE IF EXISTS horus_analytics_role;
DROP ROLE IF EXISTS horus_ingester_role;
DROP ROLE IF EXISTS horus_platform_reader;
DROP ROLE IF EXISTS horus_tenant_reader;
