# `mod:jobs` — services/jobs

- **Propósito:** Tareas singleton: archivado, copia remota con rclone, cobertura de flujos, mantenimiento.
- **Rol(es) de `horus`:** `jobs`
- **Agente dueño:** PLAT (decisión D21 en docs/po-decisions.md) ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/jobs/api`.

## Aviso de versión nueva (I1-22)

Cada `HORUS_UPDATE_CHECK_INTERVAL` (6 h; la primera, a los 0–2 min de arrancar) consulta la fuente
de versiones y lo sirve en `GET /api/v1/platform/updates` (permiso `platform.updates.read`, solo el
superadministrador; contrato `UpdateStatus` en `packages/schemas/openapi/v0/platform.yaml`). Sin
salida a Internet falla en silencio: `status: unchecked` con el motivo. La UI de la consola de
plataforma (aviso "Hay una versión nueva X.Y.Z" con notas y comando) es de UI.

| Variable | Defecto | Qué |
| --- | --- | --- |
| `HORUS_VERSION` | `0.0.0-dev` | versión instalada (la escribe el instalador) |
| `HORUS_UPDATE_CHANNEL` | `stable` | `stable` (sin prerelease) o `beta` (incluye `-beta.N`/`-rc.N`) |
| `HORUS_UPDATE_SOURCE` | API de releases de GitHub del repo | o la URL de un `latest.json` (espejo propio) |
| `HORUS_UPDATE_CHECK` | `true` | `false` = no consulta (`--no-update-check`) |
| `HORUS_UPDATE_CHECK_INTERVAL` | `6h` | mínimo 1 min |
| `HORUS_REPO` | `hcdestroyer/horus-flow` | para las URLs de notas por defecto |

Métricas: `horus_update_available{channel}` (1 = hay versión nueva) y `horus_update_check_success`
(0 = no comprobado). Cada versión nueva deja un aviso en el log (`event=horus.platform.update_available`).
Pendiente: enviarlo también por los canales de alerta de plataforma (D13) cuando existan.
