# `mod:auth` — services/auth

- **Propósito:** Tenants (ISP), usuarios, membresías, roles, sesiones, tokens por tenant, auditoría.
- **Rol(es) de `horus`:** `auth`
- **Agente dueño:** CORE ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ruta sensible:** aprobación de la persona obligatoria ([`docs/conventions.md`](../../docs/conventions.md) §6.2).
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/auth/api`.

Esqueleto creado en I0-01: todavía sin código. Este README se ampliará con variables de entorno,
métricas y eventos publicados/consumidos cuando el módulo tenga implementación.
