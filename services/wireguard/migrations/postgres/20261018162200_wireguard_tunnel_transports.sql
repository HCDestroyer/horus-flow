-- Hub de túneles multitransporte (docs/tunnels.md, ADR-0031): un peer es el túnel de un router con cualquier
-- transporte. La /32 sale de la misma IPAM (identidad del exportador); wg-agent aplica los peers wireguard y
-- tunnel-agent los ipsec (IKEv2 route-based, interfaz XFRM hxfrm<tunnel_index>) y gre (ip_gre, hgre<tunnel_index>).
--   * transport: 'wireguard' (por defecto, filas existentes) | 'ipsec' | 'gre'. Solo wireguard tiene public_key.
--   * tunnel_index: índice en el hub (if_id de la XFRM, nombre de la interfaz), de una secuencia que nunca se
--     reutiliza; único entre túneles vivos. gre_key: clave GRE (0 = sin clave, RouterOS no la admite).
--   * remote_address / remote_id: IP pública e identidad IKE del router (proyección de devices).
--   * tunnel_state, last_rekey_at, last_packet_at, tunnel_error: estado observado por tunnel-agent.
--   * server.tunnel_*: versión deseada/aplicada y último reporte de tunnel-agent (independientes de wg-agent).
-- La PSK no está aquí: es una credencial de devices (kind ipsec_psk, cifrada con su KEK).

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE wireguard.peer
  ADD COLUMN transport       text NOT NULL DEFAULT 'wireguard',
  ADD COLUMN tunnel_index    bigint,
  ADD COLUMN gre_key         bigint NOT NULL DEFAULT 0,
  ADD COLUMN remote_address  inet,
  ADD COLUMN remote_id       text,
  ADD COLUMN tunnel_state    text,
  ADD COLUMN last_rekey_at   timestamptz,
  ADD COLUMN last_packet_at  timestamptz,
  ADD COLUMN tunnel_error    text,
  ADD CONSTRAINT ck_peer__transport CHECK (transport IN ('wireguard', 'ipsec', 'gre')),
  ADD CONSTRAINT ck_peer__transport_fields CHECK (
    (transport = 'wireguard' AND tunnel_index IS NULL AND remote_address IS NULL AND remote_id IS NULL)
    OR (transport IN ('ipsec', 'gre') AND public_key IS NULL AND tunnel_index BETWEEN 1 AND 2147483647)),
  ADD CONSTRAINT ck_peer__gre_remote CHECK (transport <> 'gre' OR remote_address IS NOT NULL),
  ADD CONSTRAINT ck_peer__ipsec_remote_id CHECK (transport <> 'ipsec' OR remote_id IS NOT NULL),
  ADD CONSTRAINT ck_peer__gre_key CHECK (gre_key BETWEEN 0 AND 4294967295),
  ADD CONSTRAINT ck_peer__tunnel_state CHECK (tunnel_state IS NULL OR tunnel_state IN ('established', 'connecting', 'down', 'up'));

CREATE SEQUENCE wireguard.tunnel_index_seq AS bigint MINVALUE 1 MAXVALUE 2147483647 NO CYCLE;
CREATE UNIQUE INDEX ux_peer__tunnel_index ON wireguard.peer (tunnel_index) WHERE revoked_at IS NULL AND tunnel_index IS NOT NULL;

ALTER TABLE wireguard.server
  ADD COLUMN tunnel_desired_version bigint NOT NULL DEFAULT 1,
  ADD COLUMN tunnel_applied_version bigint NOT NULL DEFAULT 0,
  ADD COLUMN tunnel_status          text NOT NULL DEFAULT 'down',
  ADD COLUMN tunnel_last_report_at  timestamptz,
  ADD CONSTRAINT ck_server__tunnel_status CHECK (tunnel_status IN ('up', 'down', 'degraded'));

GRANT USAGE ON SEQUENCE wireguard.tunnel_index_seq TO wireguard_app, wireguard_platform;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE wireguard.server
  DROP CONSTRAINT IF EXISTS ck_server__tunnel_status,
  DROP COLUMN IF EXISTS tunnel_last_report_at,
  DROP COLUMN IF EXISTS tunnel_status,
  DROP COLUMN IF EXISTS tunnel_applied_version,
  DROP COLUMN IF EXISTS tunnel_desired_version;
DROP INDEX IF EXISTS wireguard.ux_peer__tunnel_index;
DROP SEQUENCE IF EXISTS wireguard.tunnel_index_seq;
-- Los túneles ipsec/gre se revocan antes de quitar las columnas (sus IP pasan a cuarentena).
UPDATE wireguard.ip_allocation a SET released_at = now()
  FROM wireguard.peer p WHERE a.peer_id = p.id AND a.released_at IS NULL AND p.transport <> 'wireguard';
UPDATE wireguard.peer SET status = 'revoked', revoked_at = now(), revoked_reason = 'transport_removed', updated_at = now()
  WHERE transport <> 'wireguard' AND revoked_at IS NULL;
ALTER TABLE wireguard.peer
  DROP CONSTRAINT IF EXISTS ck_peer__tunnel_state,
  DROP CONSTRAINT IF EXISTS ck_peer__gre_key,
  DROP CONSTRAINT IF EXISTS ck_peer__ipsec_remote_id,
  DROP CONSTRAINT IF EXISTS ck_peer__gre_remote,
  DROP CONSTRAINT IF EXISTS ck_peer__transport_fields,
  DROP CONSTRAINT IF EXISTS ck_peer__transport,
  DROP COLUMN IF EXISTS tunnel_error,
  DROP COLUMN IF EXISTS last_packet_at,
  DROP COLUMN IF EXISTS last_rekey_at,
  DROP COLUMN IF EXISTS tunnel_state,
  DROP COLUMN IF EXISTS remote_id,
  DROP COLUMN IF EXISTS remote_address,
  DROP COLUMN IF EXISTS gre_key,
  DROP COLUMN IF EXISTS tunnel_index,
  DROP COLUMN IF EXISTS transport;
