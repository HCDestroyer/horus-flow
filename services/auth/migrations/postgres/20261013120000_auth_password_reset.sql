-- Restablecimiento de contraseña (docs/security.md §4.5; POST /auth/password/forgot, /members/{user_id}/password-reset,
-- /platform/users/{user_id}/password-reset → POST /auth/password/reset): token de un uso, 30 min, del que solo se
-- guarda el SHA-256. La cuenta es global (sin tenant): la tabla no lleva RLS; issued_in_tenant solo dice desde qué ISP se
-- emitió (auditoría) y created_by quién (NULL = la propia persona, por "olvidé mi contraseña").

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE auth.password_reset (
  id          uuid PRIMARY KEY,
  user_id     uuid NOT NULL REFERENCES auth."user" (id),
  token_hash  bytea NOT NULL UNIQUE,
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz,
  created_by  uuid,
  issued_in_tenant uuid,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_password_reset__user_open ON auth.password_reset (user_id) WHERE used_at IS NULL;
GRANT SELECT, INSERT, UPDATE, DELETE ON auth.password_reset TO auth_app, auth_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS auth.password_reset;
