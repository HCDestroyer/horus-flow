-- I0-07 · Segundo factor TOTP y códigos de recuperación (docs/security.md §4.3; D14: 2FA obligatorio
-- para administradores desde I1). docs/database.md §2.1 lista totp_secret/recovery_code ("igual que en
-- Sprint 0") pero el contrato C2 no los congela: se añaden aquí sin tenant_id (son de la persona).
-- El secreto va cifrado con envelope encryption (AES-256-GCM, DEK por secreto envuelta con la KEK de auth).

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE auth.totp_credential (
  user_id           uuid PRIMARY KEY REFERENCES auth."user" (id) ON DELETE CASCADE,
  secret_ciphertext bytea NOT NULL,
  dek_wrapped       bytea NOT NULL,
  kek_id            text NOT NULL,
  confirmed_at      timestamptz,
  last_used_step    bigint,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE auth.recovery_code (
  id         uuid PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES auth."user" (id) ON DELETE CASCADE,
  code_hash  bytea NOT NULL,
  used_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_recovery_code__user ON auth.recovery_code (user_id) WHERE used_at IS NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON auth.totp_credential, auth.recovery_code TO auth_app, auth_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS auth.recovery_code;
DROP TABLE IF EXISTS auth.totp_credential;
