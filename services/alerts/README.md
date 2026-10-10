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
