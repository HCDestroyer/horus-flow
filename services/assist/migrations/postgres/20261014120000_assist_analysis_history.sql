-- Historial de análisis de IA (docs/assist.md §Historial): cada análisis correcto se guarda tal como
-- se mostró (Markdown ya enmascarado, sin IPs ni datos personales de clientes) para que cualquier
-- miembro con permiso de lectura del recurso vuelva a verlo sin gastar otra consulta. Retención
-- acotada (HORUS_ASSIST_HISTORY_RETENTION; se purga al guardar). RLS fail-closed por tenant.

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';
SET LOCAL client_min_messages = warning;

CREATE TABLE assist.analysis (
  id         uuid NOT NULL PRIMARY KEY,
  tenant_id  uuid NOT NULL,
  user_id    uuid,
  feature    text NOT NULL CHECK (feature IN ('finding', 'router')),
  context_id text NOT NULL CHECK (length(context_id) BETWEEN 1 AND 100),
  body       jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_analysis__context ON assist.analysis (tenant_id, feature, context_id, created_at DESC);
CREATE INDEX ix_analysis__created ON assist.analysis (tenant_id, created_at);

ALTER TABLE assist.analysis ENABLE ROW LEVEL SECURITY;
ALTER TABLE assist.analysis FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON assist.analysis USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());

GRANT SELECT, INSERT, DELETE ON assist.analysis TO assist_app;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS assist.analysis;
