-- Rutas del almacén que replica cada destino remoto (ADR-0019 §2 "qué rutas replicar", docs/api.md §destinos
-- remotos): `backups` (backups/postgres y backups/clickhouse) y `reports` (reports/<tenant_id>/ del rol
-- reporting). Por defecto las dos, también en los destinos existentes: todo viaja cifrado con age igual que los
-- backups. Quitar una ruta no borra nada del destino (la retención remota solo retira lo que ya no existe en local).

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE jobs.remote_destination
  ADD COLUMN paths text[] NOT NULL DEFAULT '{backups,reports}'
    CONSTRAINT ck_destination__paths CHECK (paths <@ ARRAY['backups', 'reports'] AND cardinality(paths) >= 1);

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE jobs.remote_destination DROP COLUMN IF EXISTS paths;
