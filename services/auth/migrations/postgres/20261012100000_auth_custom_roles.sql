-- I1 · Roles propios del ISP (POST /roles, PATCH y DELETE /roles/{role_id}; contrato Role con description y version).
-- auth.role ya tenía tenant_id (NULL = plantilla de sistema), is_system y version; se añade la descripción y la
-- forma de la clave de los roles propios (la de las plantillas la fija el catálogo C7).

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE auth.role
  ADD COLUMN description text CONSTRAINT ck_role__description_len CHECK (char_length(description) <= 500),
  ADD CONSTRAINT ck_role__custom_key CHECK (is_system OR key ~ '^[a-z][a-z0-9_]{1,62}$');

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE auth.role
  DROP CONSTRAINT IF EXISTS ck_role__custom_key,
  DROP COLUMN IF EXISTS description;
