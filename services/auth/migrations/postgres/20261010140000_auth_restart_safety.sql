-- D23 · Seguridad de auth ante reinicios.
--  * Kiosco: cada rotación guarda su sucesora y el proceso que la hizo (rotated_by = arranque del proceso). Si
--    el proceso muere tras confirmar la rotación y antes de que la cookie nueva llegue al kiosco, el kiosco
--    vuelve con la anterior: si la rotó OTRO arranque, la sucesora nunca se usó y no ha pasado el plazo de
--    gracia, se reanuda la rotación en vez de revocar el kiosco por "reutilización". Dentro del mismo arranque
--    la reutilización sigue revocando (security.md §5.5).
--  * Sesión: los fallos de 2FA de un login pendiente se cuentan en la fila (antes en memoria: un reinicio
--    regalaba intentos).

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE auth.kiosk_credential_rotated
  ADD COLUMN successor_hash bytea,
  ADD COLUMN rotated_by     uuid;

ALTER TABLE auth.session ADD COLUMN mfa_failures integer NOT NULL DEFAULT 0;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE auth.session DROP COLUMN IF EXISTS mfa_failures;
ALTER TABLE auth.kiosk_credential_rotated
  DROP COLUMN IF EXISTS rotated_by,
  DROP COLUMN IF EXISTS successor_hash;
