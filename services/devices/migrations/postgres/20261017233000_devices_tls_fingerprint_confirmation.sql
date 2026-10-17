-- Huella TLS del router confirmada por un administrador (docs/security.md §5.6, docs/vendors/mikrotik.md §11.4):
--   * tls_fingerprint_sha256 sigue siendo la huella FIJADA (la única con la que se leen datos del router).
--   * tls_fingerprint_pending: la huella observada (TOFU en el alta, o distinta de la fijada) a la espera de que un
--     administrador la confirme; nunca se acepta sola. tls_fingerprint_pending_at: cuándo se observó.
--   * tls_fingerprint_confirmed_at / _by: quién y cuándo confirmó la huella fijada (NULL en las fijadas antes de esta
--     migración por la importación de prefijos, que ya era interactiva y auditada).
-- Columnas anulables sin valor por defecto (ALTER instantáneo, sin reescritura); el CHECK de formato se añade
-- NOT VALID y se valida después (sin bloqueo exclusivo durante el recorrido).

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

ALTER TABLE devices.credential
  ADD COLUMN IF NOT EXISTS tls_fingerprint_pending      text,
  ADD COLUMN IF NOT EXISTS tls_fingerprint_pending_at   timestamptz,
  ADD COLUMN IF NOT EXISTS tls_fingerprint_confirmed_at timestamptz,
  ADD COLUMN IF NOT EXISTS tls_fingerprint_confirmed_by text;

ALTER TABLE devices.credential ADD CONSTRAINT ck_credential__tls_pending
  CHECK (tls_fingerprint_pending IS NULL OR tls_fingerprint_pending ~ '^([0-9A-F]{2}:){31}[0-9A-F]{2}$') NOT VALID;
ALTER TABLE devices.credential VALIDATE CONSTRAINT ck_credential__tls_pending;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE devices.credential DROP CONSTRAINT IF EXISTS ck_credential__tls_pending;
ALTER TABLE devices.credential
  DROP COLUMN IF EXISTS tls_fingerprint_confirmed_by,
  DROP COLUMN IF EXISTS tls_fingerprint_confirmed_at,
  DROP COLUMN IF EXISTS tls_fingerprint_pending_at,
  DROP COLUMN IF EXISTS tls_fingerprint_pending;
