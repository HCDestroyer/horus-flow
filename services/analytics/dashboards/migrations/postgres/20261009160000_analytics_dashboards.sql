-- I1-15 · Esquema analytics (submódulo dashboards, dueño CORE): dashboards como documento (layout + widgets en
-- jsonb, una sola versión/ETag), playlists de kiosco y outbox. Las plantillas de sistema (tenant_id NULL,
-- visibility = system) las siembra el módulo al arrancar desde packages/schemas/dashboard/v0/templates.
-- RLS: cada tenant ve lo suyo y las plantillas; solo escribe lo suyo. Roles analytics_app / analytics_platform.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL client_min_messages = warning;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.horus_current_tenant() RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT nullif(current_setting('horus.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
  BEGIN
    CREATE ROLE analytics_app NOLOGIN;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
  BEGIN
    CREATE ROLE analytics_platform NOLOGIN BYPASSRLS;
  EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
  END;
END $$;
-- +goose StatementEnd
GRANT analytics_app, analytics_platform TO CURRENT_USER;

CREATE TABLE analytics.dashboard (
  id               uuid PRIMARY KEY,
  tenant_id        uuid,
  owner_id         uuid,
  name             text NOT NULL,
  visibility       text NOT NULL CHECK (visibility IN ('system', 'tenant', 'private', 'platform')),
  template_key     text,
  template_version integer,
  layout           jsonb NOT NULL,
  default_range    text NOT NULL DEFAULT '6h',
  refresh_seconds  integer NOT NULL DEFAULT 30 CHECK (refresh_seconds BETWEEN 10 AND 3600),
  variables        jsonb NOT NULL DEFAULT '{}',
  widgets          jsonb NOT NULL DEFAULT '[]',
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  version          integer NOT NULL DEFAULT 1,
  CONSTRAINT ck_dashboard__system CHECK ((visibility = 'system') = (tenant_id IS NULL))
);
CREATE INDEX ix_dashboard__tenant ON analytics.dashboard (tenant_id, created_at, id);

CREATE TABLE analytics.playlist (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  name        text NOT NULL,
  items       jsonb NOT NULL,
  transition  text NOT NULL DEFAULT 'fade' CHECK (transition IN ('fade', 'none')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  version     integer NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id)
);
CREATE INDEX ix_playlist__tenant ON analytics.playlist (tenant_id, created_at, id);

-- Outbox estándar (sin RLS: solo la lee el relay con analytics_platform).
CREATE TABLE analytics.outbox (
  id uuid PRIMARY KEY, seq bigint GENERATED ALWAYS AS IDENTITY, tenant_id uuid, subject text NOT NULL,
  aggregate_id uuid NOT NULL, headers jsonb NOT NULL DEFAULT '{}', payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL, published_at timestamptz, attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX ix_outbox__pending ON analytics.outbox (seq) WHERE published_at IS NULL;

ALTER TABLE analytics.dashboard ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics.dashboard FORCE ROW LEVEL SECURITY;
CREATE POLICY p_read ON analytics.dashboard FOR SELECT USING (tenant_id = public.horus_current_tenant() OR tenant_id IS NULL);
CREATE POLICY p_write ON analytics.dashboard FOR ALL USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
ALTER TABLE analytics.playlist ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics.playlist FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON analytics.playlist USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT USAGE ON SCHEMA analytics TO analytics_app, analytics_platform;
GRANT SELECT, INSERT, UPDATE, DELETE ON analytics.dashboard, analytics.playlist, analytics.outbox TO analytics_app, analytics_platform;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA analytics TO analytics_app, analytics_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS analytics.outbox;
DROP TABLE IF EXISTS analytics.playlist;
DROP TABLE IF EXISTS analytics.dashboard;
REVOKE ALL ON SCHEMA analytics FROM analytics_app, analytics_platform;
