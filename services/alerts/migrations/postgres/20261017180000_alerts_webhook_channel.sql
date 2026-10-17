-- Canal de notificación `webhook` (ADR-0030; contrato docs/integrations/argosisp.md §1.3 y §3.4): POST JSON
-- firmado con HMAC-SHA256 (secreto write-only cifrado como las demás credenciales). Solo amplía el CHECK del
-- tipo de canal (NOT VALID + VALIDATE: sin bloqueo exclusivo durante el recorrido).

-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
ALTER TABLE alerts.notification_channel DROP CONSTRAINT IF EXISTS notification_channel_kind_check;
ALTER TABLE alerts.notification_channel ADD CONSTRAINT ck_channel__kind
  CHECK (kind IN ('email', 'telegram', 'librenms', 'webhook')) NOT VALID;
ALTER TABLE alerts.notification_channel VALIDATE CONSTRAINT ck_channel__kind;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DELETE FROM alerts.notification_channel WHERE kind = 'webhook';
ALTER TABLE alerts.notification_channel DROP CONSTRAINT IF EXISTS ck_channel__kind;
ALTER TABLE alerts.notification_channel ADD CONSTRAINT notification_channel_kind_check
  CHECK (kind IN ('email', 'telegram', 'librenms'));
