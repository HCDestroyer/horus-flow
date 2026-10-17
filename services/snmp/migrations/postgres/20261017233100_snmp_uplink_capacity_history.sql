-- Capacidad contratada de un enlace con vigencia (docs/capacity-forecast.md §3): cada cambio de contracted_bps cierra la
-- fila vigente (valid_to) y abre otra desde valid_from (por defecto el momento del cambio; un administrador puede
-- fecharlo en el pasado, nunca antes del cambio anterior). La utilización, el p95 y las horas sobre umbral de cada día se
-- calculan con la capacidad vigente en ese momento (un upgrade no reescribe la historia); el pronóstico usa la vigente
-- hoy para los umbrales futuros. contracted_bps NULL = sin capacidad contratada (se usa la velocidad del puerto).
-- Las filas se escriben en la misma transacción que snmp.uplink_config; borrar la configuración cierra la vigente y
-- conserva la historia. RLS forzada por tenant; el trabajo diario la carga con el rol de plataforma.
-- Tabla nueva: los índices en la misma transacción; el relleno es una fila por enlace ya configurado.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL client_min_messages = warning;

CREATE TABLE snmp.uplink_capacity_history (
  tenant_id      uuid NOT NULL,
  interface_id   uuid NOT NULL,
  valid_from     timestamptz NOT NULL,
  valid_to       timestamptz,
  contracted_bps bigint CHECK (contracted_bps IS NULL OR contracted_bps BETWEEN 1000000 AND 1600000000000),
  changed_by     text,
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, interface_id, valid_from),
  CONSTRAINT ck_uplink_capacity_history__range CHECK (valid_to IS NULL OR valid_to > valid_from)
);
-- Una sola fila vigente por enlace.
CREATE UNIQUE INDEX ux_uplink_capacity_history__open ON snmp.uplink_capacity_history (tenant_id, interface_id)
  WHERE valid_to IS NULL;

INSERT INTO snmp.uplink_capacity_history (tenant_id, interface_id, valid_from, contracted_bps, changed_by)
SELECT tenant_id, interface_id, created_at, contracted_bps, updated_by FROM snmp.uplink_config;

ALTER TABLE snmp.uplink_capacity_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE snmp.uplink_capacity_history FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON snmp.uplink_capacity_history USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT SELECT, INSERT, UPDATE ON snmp.uplink_capacity_history TO snmp_app, snmp_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS snmp.uplink_capacity_history;
