-- I1 · Invitaciones a un tenant (POST /members, POST /auth/invitations/accept; docs/api.md §2.1–§2.2): token de un
-- uso con caducidad de 72 h. Solo se guarda el SHA-256 del token. La invitación cuelga de la membresía invitada y se
-- borra con ella. El canje público la busca por hash con auth_platform (como los códigos de kiosco) y la consume
-- dentro de su tenant (RLS).

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE auth.invitation (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  membership_id uuid NOT NULL,
  user_id       uuid NOT NULL REFERENCES auth."user" (id),
  token_hash    bytea NOT NULL UNIQUE,
  expires_at    timestamptz NOT NULL,
  used_at       timestamptz,
  created_by    uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, membership_id) REFERENCES auth.tenant_membership (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX ix_invitation__membership ON auth.invitation (tenant_id, membership_id) WHERE used_at IS NULL;

ALTER TABLE auth.invitation ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth.invitation FORCE ROW LEVEL SECURITY;
CREATE POLICY p_tenant ON auth.invitation USING (tenant_id = public.horus_current_tenant())
  WITH CHECK (tenant_id = public.horus_current_tenant());
GRANT SELECT, INSERT, UPDATE, DELETE ON auth.invitation TO auth_app, auth_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS auth.invitation;
