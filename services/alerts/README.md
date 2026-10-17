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
entrega se reenvía una vez; en LibreNMS la línea de syslog termina en `| id=<entrega>` para reconocer el duplicado
(LibreNMS no deduplica) ([`docs/architecture.md`](../../docs/architecture.md) §10.18).

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

## Webhook firmado (ADR-0030)

Canal `webhook` (p. ej. el receptor de ArgosISP): `POST` JSON `{id, event, occurred_at, tenant, data}` firmado con
`X-Webhook-Id` (id de la entrega, estable entre reintentos), `X-Webhook-Timestamp` y
`X-Webhook-Signature: v1=hex(HMAC-SHA256(secreto, timestamp + "." + cuerpo))` (`packages/go/webhooksig`). Secreto
write-only (`webhook_secret`, cifrado con la KEK de alerts). Eventos del contrato §3.4: `finding.opened`,
`finding.resolved`, `customer.security_state_changed`, `router.status_changed`, `infra_service.state_changed`; con
identidad ArgosISP del cliente, `data.external` lleva sus ids (de `devices.CustomerDirectory`). Reintentos 1 min,
5 min, 30 min, 2 h y 12 h; sin agrupación. Solo https y destinos públicos (guardia de egreso);
`HORUS_ALERTS_WEBHOOK_ALLOW_PRIVATE=true` permite http, privadas y loopback en laboratorios.

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

## Integración con LibreNMS (D17): Horus aprovisiona, LibreNMS alerta

Horus no envía alertas propias a LibreNMS por un recurso inventado (el antiguo `POST /api/v0/eventlog` no existe:
404). En su lugar **prepara LibreNMS** por su API v0 y le **delega el alertado**: cada notificación del canal
`librenms` es una línea de syslog del dispositivo «Horus Flow» y una regla de LibreNMS la convierte en alerta, que sale
por los transportes de la instancia. Código: `internal/librenms` (cliente, aprovisionamiento y prueba) y
`internal/notify/librenms*.go` (casos de uso, tablas y rutas). UI: *Administración › Integración LibreNMS*.

### Puesta en marcha (la persona, una vez)

1. En LibreNMS, crea un usuario administrador para Horus y un **token de API** (Horus crea dispositivos, grupos,
   plantillas y reglas).
2. Activa el syslog en el servidor de LibreNMS: `lnms config:set enable_syslog true`.
3. En *Alertas › Transportes de alerta*, marca al menos un transporte como **«Default Alert»**. Los transportes **no se
   pueden crear por la API**: las reglas que crea Horus no tienen transportes asignados y LibreNMS usa los marcados por
   defecto (versiones con mapa de transportes) o, en las versiones con «operaciones» de alerta (rama master), crea al
   guardar la regla una operación con los transportes por defecto de ese momento. Sin ninguno marcado, la regla se
   guarda sin avisos y Horus lo advierte (`no_default_transport`): márcalo y vuelve a sincronizar.
4. En Horus, crea el canal LibreNMS (URL base + token) y pulsa **Conectar con LibreNMS**; después **Enviar prueba**.
5. Para cada router, indica una **IP de gestión alcanzable desde LibreNMS** (la IP del túnel WireGuard hacia Horus no
   lo es) y el modo: solo ping (por defecto), SNMP v2c/v3 propio para LibreNMS (credenciales write-only cifradas con la
   KEK de alerts) o las credenciales SNMPv3 de solo lectura de Horus con consentimiento explícito por router (auditado;
   el router debe aceptar SNMP desde la IP de LibreNMS). Sincroniza para aplicarlo.

### Qué crea la sincronización (idempotente)

Todo lleva el prefijo «Horus · » y sus ids de LibreNMS se guardan en `alerts.librenms_object` (RLS por tenant):

| Objeto | Llamada | Detalle |
| --- | --- | --- |
| Dispositivo «Horus Flow» | `POST /api/v0/devices` | `hostname` = host de `HORUS_PUBLIC_BASE_URL` o el indicado; `snmp_disable`, `os=ping`, `force_add`. Destino del syslog de Horus |
| Grupos «Horus · Routers» e «Horus · Infraestructura» | `POST /api/v0/devicegroups` (`type=static`), `POST/DELETE /api/v0/devicegroups/{id}/devices` | Pertenencia sincronizada; quien sale se quita del grupo, nunca se borra |
| Routers | `POST /api/v0/devices` | Ping: `snmp_disable` + `force_add`; SNMP: `snmpver` v2c/v3 sin `force_add` (LibreNMS valida las credenciales). Si ya existe un dispositivo con ese host o IP, se usa sin modificarlo |
| Reglas | `POST /api/v0/rules`, `PUT /api/v0/rules` (`rule_id`) | Router caído (`macros.device_down`), CPU alta (`processors.processor_usage` > umbral y `macros.device_up`), memoria alta (`mempools.mempool_perc` ≥ umbral, clase `system`), interfaz caída (`macros.port_down`), reinicio (`devices.uptime` < 300), servicio de infraestructura caído y evento de seguridad Horus (`syslog.timestamp >= macros.past_5m` y `syslog.msg REGEXP '^HORUS '` sobre «Horus Flow»). Umbrales por defecto 85 % / 90 % (editables; pendiente leerlos de los umbrales SNMP por ISP) |
| Plantillas | `POST/PUT /api/v0/alert_templates` | Blade en español con título de alerta y de recuperación, bucle de `faults` y texto de recuperación; asociadas a su regla |

`add_rule` no devuelve el id: Horus relee `GET /api/v0/rules` y busca por nombre. Si alguien borra en LibreNMS un
objeto de Horus, la siguiente sincronización lo **recrea** (`recreated`); nunca se modifica ni se borra lo que Horus no
creó. Un dispositivo creado por Horus solo se borra a petición explícita (`DELETE …/librenms/devices/{router_id}`).
Al sincronizar, Horus mide el desfase horario de LibreNMS (entrada en el registro de eventos de «Horus Flow»): el
syslog de LibreNMS guarda la marca de tiempo tal cual y la regla compara con `NOW()` local.

### Entregas y prueba

- Entrega: `POST /api/v0/syslogsink` con `host` = «Horus Flow» y `msg` =
  `HORUS <SEVERIDAD> <tipo> <asunto> | <texto> | <enlace> | id=<entrega>` (una línea, ≤ 900 caracteres, sin la IP del
  cliente), más `POST /api/v0/devices/{id}/eventlog` como historial (best-effort). Sin aprovisionar: error
  `librenms_not_provisioned` (se reintenta con el backoff de la cola).
- Prueba de extremo a extremo (`POST …/librenms/test`): autenticación → dispositivo → regla → envío → la línea aparece
  en `GET /api/v0/logs/syslog/{id}` en unos segundos. `syslog_not_stored` suele ser el syslog desactivado o un host que
  no coincide. La línea de prueba coincide con la regla: LibreNMS emite la alerta y la resuelve a los 5 minutos.
- Gancho para el inventario de servicios: `api.LibreNMSInfrastructure` (`alerts.LibreNMSInfrastructure` en
  `module.Services`) sincroniza «Horus · Infraestructura» con una lista `{nombre, IP}`.
