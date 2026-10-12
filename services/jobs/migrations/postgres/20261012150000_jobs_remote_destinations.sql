-- Copia remota de los backups locales (ADR-0019 con la nota de 2026-10-12, storage.md §4, dueño PLAT):
--  * installation: identificador estable de la instalación; nombra la carpeta de las copias en el destino
--    (<ruta>/horus-<id>/backups/...). Viaja con los backups de PostgreSQL: tras una restauración la carpeta
--    remota sigue siendo la misma.
--  * remote_destination: destinos de plataforma (tenant_id nulo; por tenant fuera de v1, Q20). Credenciales
--    SFTP write-only con envelope encryption (KEK de jobs, AAD "remote_destination:<id>"); huella SHA-256 de
--    la clave del servidor confirmada por la persona (TOFU); estado de la última copia. `version` es el ETag:
--    no sube con el estado de las copias, solo con las ediciones.
--  * remote_copy: ledger de lo que está a salvo fuera, por destino y ruta relativa al almacén (tamaño, mtime y
--    SHA-256 del original en claro, nombre remoto cifrado). Base de la copia incremental (solo lo nuevo o
--    cambiado) y de la retención remota (solo se borra lo vencido que ya no existe en local).
-- Sin RLS: son tablas de plataforma sin tenant.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL client_min_messages = warning;

-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    CREATE ROLE jobs_app NOLOGIN;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
  BEGIN
    CREATE ROLE jobs_platform NOLOGIN BYPASSRLS;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
END $$;
-- +goose StatementEnd
GRANT jobs_app, jobs_platform TO CURRENT_USER;

CREATE TABLE jobs.installation (
  singleton  boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  id         uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE jobs.remote_destination (
  id                     uuid PRIMARY KEY,
  tenant_id              uuid CHECK (tenant_id IS NULL),
  kind                   text NOT NULL CHECK (kind IN ('sftp')),
  name                   text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
  host                   text NOT NULL CHECK (length(host) BETWEEN 1 AND 253),
  port                   integer NOT NULL DEFAULT 22 CHECK (port BETWEEN 1 AND 65535),
  username               text NOT NULL CHECK (length(username) BETWEEN 1 AND 128),
  path                   text NOT NULL CHECK (length(path) BETWEEN 1 AND 1024),
  retention_days         integer NOT NULL DEFAULT 30 CHECK (retention_days BETWEEN 1 AND 3650),
  enabled                boolean NOT NULL DEFAULT false,
  host_key_fingerprint   text CHECK (host_key_fingerprint ~ '^SHA256:[A-Za-z0-9+/]{43}$'),
  credential_ciphertext  bytea,
  credential_dek_wrapped bytea,
  credential_kek_id      text,
  credential_kind        text CHECK (credential_kind IN ('password', 'private_key', 'private_key+password')),
  verified_at            timestamptz,
  last_success_at        timestamptz,
  last_attempt_at        timestamptz,
  last_error             text,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  version                integer NOT NULL DEFAULT 1,
  CONSTRAINT ck_destination__credential CHECK (
    (credential_ciphertext IS NULL) = (credential_dek_wrapped IS NULL)
    AND (credential_ciphertext IS NULL) = (credential_kek_id IS NULL)
    AND (credential_ciphertext IS NULL) = (credential_kind IS NULL)),
  -- Habilitado solo con credenciales y huella confirmada (POST .../test).
  CONSTRAINT ck_destination__enabled CHECK (NOT enabled OR (credential_ciphertext IS NOT NULL AND host_key_fingerprint IS NOT NULL))
);
CREATE UNIQUE INDEX ux_destination__name ON jobs.remote_destination (lower(name));

CREATE TABLE jobs.remote_copy (
  destination_id uuid NOT NULL REFERENCES jobs.remote_destination (id) ON DELETE CASCADE,
  rel_path       text NOT NULL,
  size           bigint NOT NULL CHECK (size >= 0),
  mtime          timestamptz NOT NULL,
  sha256         bytea NOT NULL CHECK (length(sha256) = 32),
  remote_name    text NOT NULL,
  uploaded_at    timestamptz NOT NULL,
  PRIMARY KEY (destination_id, rel_path)
);
CREATE INDEX ix_remote_copy__uploaded ON jobs.remote_copy (destination_id, uploaded_at);

GRANT USAGE ON SCHEMA jobs TO jobs_app, jobs_platform;
GRANT SELECT, INSERT, UPDATE, DELETE ON jobs.installation, jobs.remote_destination, jobs.remote_copy TO jobs_app, jobs_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS jobs.remote_copy;
DROP TABLE IF EXISTS jobs.remote_destination;
DROP TABLE IF EXISTS jobs.installation;
REVOKE ALL ON SCHEMA jobs FROM jobs_app, jobs_platform;
