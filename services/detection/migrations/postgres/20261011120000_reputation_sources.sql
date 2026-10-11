-- D20 · Fuentes de reputación gestionadas por la API de plataforma
-- (/platform/reputation/sources, platform.yaml). Catálogo base (feeds.yaml) y listas
-- personalizadas del superadministrador. Las filas del catálogo las siembra y concilia el módulo
-- al arrancar desde la declaración de feeds (embebida o HORUS_FEEDS_CONFIG): guardan su identidad
-- (id, clave, numeric_id) y su activación; las personalizadas guardan la definición completa.
-- Es de plataforma (sin tenant_id ni RLS): solo la usa detection_platform.
-- La cabecera de autenticación del feed va cifrada (envelope encryption, KEK del módulo en
-- HORUS_DETECTION_KEK_FILE; AAD "reputation_source:<id>"); el valor nunca sale por la API.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

CREATE TABLE detection.reputation_source (
  id                    uuid NOT NULL PRIMARY KEY,
  -- = reputation.SourceID(dataset_id) (FNV-1a de 16 bits): flows_raw.reputation_source_id.
  numeric_id            integer NOT NULL CHECK (numeric_id BETWEEN 1 AND 65535),
  key                   text NOT NULL CHECK (key ~ '^[a-z][a-z0-9_]{2,47}$'),
  -- Id en el almacén de datasets y en el snapshot (feeds.yaml en el catálogo, custom-<key> si no).
  dataset_id            text NOT NULL CHECK (dataset_id ~ '^[a-z0-9][a-z0-9_-]{1,62}$'),
  origin                text NOT NULL CHECK (origin IN ('catalog', 'custom')),
  name                  text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
  url                   text NOT NULL CHECK (char_length(url) <= 2048),
  format                text NOT NULL,
  frequency             text NOT NULL CHECK (frequency ~ '^[1-9][0-9]*(m|h|d)$'),
  category              text NOT NULL CHECK (category IN ('botnet_cc', 'scanner', 'malware_dist', 'mining_pool', 'proxy_vpn', 'tor_exit', 'blocklist')),
  confidence            integer NOT NULL CHECK (confidence BETWEEN 0 AND 100),
  enabled               boolean NOT NULL DEFAULT true,
  license               text,
  notes                 text,
  min_entries           integer NOT NULL DEFAULT 1 CHECK (min_entries >= 0),
  max_bytes             bigint NOT NULL DEFAULT 67108864 CHECK (max_bytes BETWEEN 1024 AND 536870912),
  auth_header_name      text CHECK (auth_header_name ~ '^[A-Za-z0-9-]{1,64}$'),
  auth_ciphertext       bytea,
  auth_dek_wrapped      bytea,
  auth_kek_id           text,
  terms_acknowledged_at timestamptz,
  created_by            uuid,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  version               integer NOT NULL DEFAULT 1 CHECK (version >= 1),
  CONSTRAINT ux_reputation_source__key UNIQUE (key),
  CONSTRAINT ux_reputation_source__dataset UNIQUE (dataset_id),
  CONSTRAINT ux_reputation_source__numeric UNIQUE (numeric_id),
  CONSTRAINT ck_reputation_source__auth CHECK (
    (auth_ciphertext IS NULL) = (auth_dek_wrapped IS NULL) AND (auth_ciphertext IS NULL) = (auth_kek_id IS NULL)
    AND (auth_ciphertext IS NULL OR auth_header_name IS NOT NULL)),
  CONSTRAINT ck_reputation_source__custom CHECK (origin = 'catalog' OR terms_acknowledged_at IS NOT NULL)
);
CREATE INDEX ix_reputation_source__origin ON detection.reputation_source (origin, created_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON detection.reputation_source TO detection_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS detection.reputation_source;
