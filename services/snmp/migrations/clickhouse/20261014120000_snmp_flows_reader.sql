-- FLOW I2 · Chequeos pasivos de servicios de infraestructura (no_traffic, ddos_inbound): el rol snmp lee
-- flows.flows_raw por tenant. Se le concede horus_tenant_reader (SELECT en flows.* con la política p_tenant,
-- que exige SQL_horus_tenant: fail-closed), igual que analytics y detection. No recibe horus_platform_reader:
-- las dos políticas se combinan con OR. El rol se crea IF NOT EXISTS por si flows aún no se migró.

-- +goose NO TRANSACTION
-- +goose Up
CREATE ROLE IF NOT EXISTS horus_tenant_reader;
CREATE ROLE IF NOT EXISTS horus_snmp_role;
GRANT horus_tenant_reader TO horus_snmp_role;

-- +goose Down
REVOKE horus_tenant_reader FROM horus_snmp_role;
