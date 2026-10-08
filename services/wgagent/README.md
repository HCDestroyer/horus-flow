# `mod:wg-agent` — services/wgagent

- **Propósito:** Agente que aplica el estado WireGuard en el kernel (netlink, nftables), fail-static, `CAP_NET_ADMIN`.
- **Rol(es) de `horus`:** `wg-agent`
- **Agente dueño:** CORE ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Nombre de carpeta:** `wgagent` ([`docs/conventions.md`](../../docs/conventions.md) §1) para el módulo `mod:wg-agent` y el rol `wg-agent`.
- **Ruta sensible:** privilegios de red del host (`area:security`).
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/wgagent/api`.

Esqueleto creado en I0-01: todavía sin código. Este README se ampliará con variables de entorno,
métricas y eventos publicados/consumidos cuando el módulo tenga implementación.
