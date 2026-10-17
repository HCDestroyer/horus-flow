-- Multiempresa de ArgosISP (contrato docs/integrations/argosisp.md 1.2.0 §2.1 y §2.4; solicitud H7): un ISP puede
-- operar varias empresas (NIT) y cada servicio pertenece a una. La identidad externa guarda la empresa del servicio:
-- external_company_id (id en ArgosISP), company_name y company_nit (datos del ISP, no del cliente). NULL = ArgosISP
-- no la informó (respaldo GET /ip/:ip o versión anterior a 1.2.0). Columnas nulas sin DEFAULT: solo cambian los
-- metadatos, sin reescribir la tabla. La RLS de la tabla no cambia.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

ALTER TABLE devices.customer_external_identity
  ADD COLUMN external_company_id bigint,
  ADD COLUMN company_name text CONSTRAINT ck_ext_identity__company_name CHECK (char_length(company_name) <= 120),
  ADD COLUMN company_nit text CONSTRAINT ck_ext_identity__company_nit CHECK (char_length(company_nit) <= 120);

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE devices.customer_external_identity
  DROP COLUMN IF EXISTS company_nit,
  DROP COLUMN IF EXISTS company_name,
  DROP COLUMN IF EXISTS external_company_id;
