# `mod:alerts` — services/alerts

- **Propósito:** Reglas, alertas y notificaciones (email, Telegram, LibreNMS — D13).
- **Rol(es) de `horus`:** `alerts`
- **Agente dueño:** CORE (decisión D21 en [`docs/po-decisions.md`](../../docs/po-decisions.md); [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/alerts/api`.

## Entregas: cola persistente (D23)

El durable `alerts-notify-*` solo **encola** cada entrega (una por canal y evento de origen, con el mensaje ya
renderizado y sin datos personales) en `alerts.notification_delivery` y confirma el mensaje NATS; un despachador
reclama las entregas vencidas por lease (`FOR UPDATE SKIP LOCKED`), envía y registra el resultado o reprograma con
backoff. Tras un reinicio retoma lo pendiente y lo que quedó a medio enviar (al vencer el lease de 2 min). Un
fallo definitivo emite `horus.alerts.notification.failed` y el evento de plataforma `alert_delivery_failed`.
Deduplicación: única por (canal, evento origen); si el proceso muere entre la aceptación remota y el registro, la
entrega se reenvía una vez y LibreNMS recibe `X-Horus-Delivery-Id` para descartarla
([`docs/architecture.md`](../../docs/architecture.md) §10.18).

| Variable | Defecto | Uso |
| --- | --- | --- |
| `HORUS_ALERTS_DISPATCH_INTERVAL` | `5s` | Periodo del despachador (además se despierta al encolar) |
| `HORUS_ALERTS_MAX_ATTEMPTS` | `8` | Intentos antes de dar la entrega por fallida |
| `HORUS_ALERTS_RETRY_BACKOFF` | `30s,1m,2m,5m,10m,30m,1h` | Espera tras cada intento fallido (el último valor se repite) |

## Qué se notifica

Durables `alerts-notify-<stream>` (deliver new) sobre `DETECTION_EVENTS`, `FLOWS_EVENTS`, `WIREGUARD_EVENTS` y
`SNMP_EVENTS`. Cada evento se traduce a un tipo de notificación agrupado en una **categoría**:

| Categoría | Tipos | Origen |
| --- | --- | --- |
| `security` | `finding_opened`, `finding_reopened` | `horus.detection.finding.*` |
| `routers` | `router_offline`, `router_recovered`, `router_cpu_high`, `router_memory_high`, `router_temperature_high`, `router_hardware_failed`, `router_interface_down`, `router_interface_up`, `router_rebooted` | `horus.snmp.router.state_changed`, `…router.rebooted`, `…interface.oper_status_changed` |
| `infrastructure` | `service_down`, `service_degraded`, `service_recovered` | `horus.snmp.service.state_changed` (servicios vigilados) |
| `exporter_tunnel` | `exporter_silent`, `exporter_recovered`, `tunnel_down`, `tunnel_recovered` | `horus.flows.exporter.*`, `horus.wireguard.peer.handshake_*` |

La suscripción del canal acepta `categories` (incluye los tipos que se añadan a la categoría) y/o `event_types`
(las suscripciones antiguas, solo con `event_types`, siguen valiendo). Los nombres de router y el enlace a la
consola (`/t/<slug>/routers/<id>`) salen del evento (`router_name`, `site_name`) o, si faltan, de `devices` y `auth`
cuando están en el mismo proceso.

**Recuperaciones** (`*_recovered`, `router_interface_up`, severidad `info`): con `notify_recovery` (por defecto
`true`) llegan siempre al canal que recibió la alarma del mismo recurso (la última entrega enviada o en cola de ese
recurso es una alarma), sin filtro de severidad ni agrupación; si el canal no recibió la alarma, solo por su
suscripción normal.

## Telegram

Bot API oficial: mensajes `parse_mode=HTML` con todo el texto dinámico escapado (`<`, `>`, `&`), emoji de severidad,
enlace `<a href>` y `link_preview_options.is_disabled`; `message_thread_id` opcional (temas de un foro). Un `429`
reprograma la entrega a `retry_after` sin gastar intento; `migrate_to_chat_id` (grupo convertido en supergrupo)
actualiza el `chat_id` del canal, lo audita (`alerts.channel.telegram_chat_migrated`, actor `svc:alerts`) y reintenta.
La prueba de conexión hace `getMe` + `getChat` + `getChatMember` (en un canal, administrador con
`can_post_messages`) y muestra la `description` de Telegram saneada (nunca el token).

Asistentes de la consola: `POST /notification-channels/telegram/bot-validation` (`getMe` de un token pegado, sin
guardarlo), `…/telegram/chat-detection` (`getUpdates` sin espera larga, confirmando con `offset`; falla con
`NOTIFICATION_TELEGRAM_WEBHOOK_ACTIVE` si el bot tiene webhook) y, para el bot compartido de la instalación
(`HORUS_TELEGRAM_BOT_TOKEN_FILE`, cuyas actualizaciones mezclan a todos los ISP), `…/telegram/link-codes`: un código de
un solo uso (15 min, tabla `alerts.telegram_link_code`) que la persona envía con `/start <código>`; la detección solo
devuelve el chat que lo envió.
