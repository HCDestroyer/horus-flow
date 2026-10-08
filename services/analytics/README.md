# `mod:analytics` — services/analytics

- **Propósito:** Consultas analíticas, widgets, dashboards y kiosco; el rol `reporting` genera reportes.
- **Rol(es) de `horus`:** `analytics`, `reporting`
- **Agente dueño:** FLOW (salvo `dashboards`, que es de CORE) ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Submódulo `dashboards`:** dueño CORE (persistencia de dashboards, team.md §2).
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/analytics/api`.

Esqueleto creado en I0-01: todavía sin código. Este README se ampliará con variables de entorno,
métricas y eventos publicados/consumidos cuando el módulo tenga implementación.
