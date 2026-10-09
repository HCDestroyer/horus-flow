# `mod:wireguard` — services/wireguard

- **Propósito:** Control WireGuard: IPAM de túneles, peers, enrolamiento y script RouterOS (I1-01, I1-02).
- **Rol(es) de `horus`:** `wireguard`
- **Agente dueño:** CORE ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ruta sensible:** enrolamiento y plantillas `.rsc` requieren aprobación de la persona ([`docs/conventions.md`](../../docs/conventions.md) §6.2).
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · [ADR-0022](../../docs/adr/0022-mikrotik-routeros-v7-primer-fabricante.md) · [`docs/vendors/mikrotik.md`](../../docs/vendors/mikrotik.md) §5 y §7

## Qué hace

| Pieza | Detalle |
| --- | --- |
| IPAM | Rangos de plataforma (`HORUS_WG_TUNNEL_CIDRS`, por defecto `10.255.0.0/16`) con red de servicios (`HORUS_WG_SERVICES_CIDR`, `10.255.0.0/24`: hub, colector, pollers). Validación: IPv4 canónico, sin solapes entre rangos (también `EXCLUDE` en `wireguard.ip_pool`), nunca `100.64.0.0/10`, servicios dentro de un rango. Una `/32` por router, **única en toda la plataforma** (índice único global), cuarentena de 24 h al liberar. Agotado → `WIREGUARD_IP_POOL_EXHAUSTED`. |
| Peer por router | Equivale a consumir `horus.devices.router.created/deleted`: hasta que exista el bus entre módulos, `Run` reconcilia cada `HORUS_WIREGUARD_RECONCILE_EVERY` (5 s) con `devices` (contrato en proceso `services/devices/api.Onboarding`) y además el peer se asegura de forma síncrona al pedir el script. La IP se proyecta en el router (`tunnel_address`, `wireguard_peer_id`, `onboarding_state`), y `devices` publica `horus.devices.router.updated` con `tunnel_address` `/32`: la identidad del exportador que usa flows. |
| Script de alta | `POST /routers/{id}/provisioning-script` (`wireguard.write`, reauth, `Idempotency-Key`): plantillas `internal/script/templates` (7.12 y long-term ≥ 7.18, *a verificar en CHR*), IPFIX sin muestreo **con campos NAT** (traffic-model §4.4.3), SNMPv3 authPriv, usuario `horus` del grupo `horus-ro`, firewall solo desde el túnel, keepalive 25 s, `/tool fetch … check-certificate=yes` a `HORUS_PUBLIC_BASE_URL/api/v1/enroll/wireguard`. Con TLS `self_signed`/`provided` o modo `ip_only` (D19) importa el certificado público (`HORUS_PUBLIC_TLS_CERT_FILE`) antes del fetch. Cada petición crea **token y credenciales nuevos** e invalida los anteriores (auditado). RouterOS < 7.12 → `422 ROUTEROS_VERSION_UNSUPPORTED`. Valores validados antes de insertarlos (sin inyección en el `.rsc`). |
| Script inverso | `POST /routers/{id}/deprovisioning-script`: borra lo que lleva `comment="horus"` y solo el target de Traffic Flow de Horus. |
| Enrolamiento | `POST /enroll/wireguard` (público): token de 256 bits (solo SHA-256 en BD), un uso, TTL 24 h, revocable (`POST /wireguard/enrollment-tokens/{id}/revoke`); usado/caducado/revocado/desconocido → `422 ENROLLMENT_TOKEN_INVALID` sin distinguir; clave inválida (422) o ya registrada (`409 WIREGUARD_PUBLIC_KEY_IN_USE`) cuentan como fallo y 5 fallos invalidan el token; 10/min por IP en el gateway. `202 {"peer_status":"pending_handshake"}`; auditado; el estado deseado se empuja al agente al momento. |
| Estado | `ReportStatus` (wg-agent cada 15 s): primer handshake → `active` (`peer.activated`, `tunnel_up`); sin handshake en 180 s → `handshake_stale`/`recovered`; agente por detrás (p. ej. reinicio, `applied_version = 0`) → reenvío del estado completo. |
| Lectura | `GET /wireguard/peers`, `GET /wireguard/peers/{id}`, `GET /platform/wireguard/hubs`. |

Eventos (outbox `wireguard.outbox`): `horus.wireguard.peer.{created,enrolled,activated,revoked,handshake_stale,handshake_recovered}` y
`horus.wireguard.audit.recorded` (solo si `auth` no está en el proceso; si está, la auditoría va a `auth.Audit`).

## Variables

`HORUS_POSTGRES_DSN`, `HORUS_WG_ENDPOINT`, `HORUS_WG_PORT` (51820), `HORUS_WG_HUB_ID`/`HORUS_WG_HUB_NAME`, `HORUS_WG_HUB_PUBLIC_KEY`
(obligatoria si wg-agent está en otro proceso: el contrato v0 no transporta la clave del hub), `HORUS_WG_TUNNEL_CIDRS`,
`HORUS_WG_SERVICES_CIDR`, `HORUS_COLLECTOR_IP` (por defecto la IP del hub), `HORUS_PUBLIC_BASE_URL`, `HORUS_ACCESS_MODE`,
`HORUS_TLS_MODE`, `HORUS_PUBLIC_TLS_CERT_FILE`, `HORUS_WG_NTP_SERVER`, `HORUS_WG_TRAFFIC_FLOW_CACHE_ENTRIES` (256k),
`HORUS_WIREGUARD_GRPC_ADDR` (:9094, ControlService), `HORUS_WGAGENT_ADDR` (AgentService remoto), `GRPC_TLS_MODE` (`mtls`;
`disabled` solo en dev) + `HORUS_TLS_{CA,CERT,KEY}_FILE`.

## Pruebas

```sh
go test ./services/wireguard/...                                   # dominio (IPAM, tokens) y golden del script
go test ./services/wireguard/internal/script -update               # regenerar golden tras cambiar plantillas
go test -tags=integration ./services/wireguard/... ./tests/tenancy/ # PostgreSQL (testcontainers) + e2e con wg-agent en memoria
sudo go test -tags=wireguard ./services/wgagent/internal/adapters/kernel/  # interfaz real con wireguard-go
```

## Pendiente / notas de contrato

- `agent.proto` v0 no lleva la clave privada ni la pública del hub: la privada vive en wg-agent (`HORUS_WGAGENT_PRIVATE_KEY_FILE`) y la
  pública se configura aquí (`HORUS_WG_HUB_PUBLIC_KEY`) o se toma del agente en proceso. Propuesta de enmienda: `hub_public_key` en
  `ReportStatusRequest`.
- Código Go del proto generado en `services/wgagent/api/agentv1` (no en `packages/protobuf/gen`, que es de INT): moverlo al cablear `make generate`.
- Sin bus entre módulos: consumo de `devices.router.*` por reconciliación y contrato en proceso; `Idempotency-Key` del script se exige
  en el gateway pero no hay *replay* (la respuesta lleva secretos de un solo uso). Telemetría `peer_status.observed` y `hub.status_changed` sin publicar.
- Plantillas `.rsc` sin fixture validado en CHR (I0-12 no grabó scripts): `/file add contents=`, `/certificate import name=`, nombres de
  `/ip traffic-flow ipfix` y `place-before=0` con la cadena vacía, a verificar en I1-25.
