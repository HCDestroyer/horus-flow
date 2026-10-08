# `mod:alerts` — services/alerts

- **Propósito:** Reglas, alertas y notificaciones (email, Telegram, LibreNMS — D13).
- **Rol(es) de `horus`:** `alerts`
- **Agente dueño:** CORE (decisión D21 en [`docs/po-decisions.md`](../../docs/po-decisions.md); [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/alerts/api`.

Esqueleto creado en I0-01: todavía sin código. Este README se ampliará con variables de entorno,
métricas y eventos publicados/consumidos cuando el módulo tenga implementación.
