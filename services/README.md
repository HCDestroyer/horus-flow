# services/ — módulos del binario `horus`

Un único módulo Go (`go.mod` en la raíz) y un único binario con roles
([ADR-0025](../docs/adr/0025-binario-modular-con-roles.md)). Cada carpeta es un módulo
`mod:<módulo>` con dueño propio en [`.github/CODEOWNERS`](../.github/CODEOWNERS).

| Carpeta | Rol(es) | Agente |
| --- | --- | --- |
| `cmd/horus/` | composición por `HORUS_ROLES` | PLAT |
| `gateway/` | `gateway` | CORE |
| `auth/` | `auth` | CORE (sensible) |
| `devices/` | `devices` | CORE |
| `wireguard/` | `wireguard` | CORE (sensible) |
| `wgagent/` | `wg-agent` | CORE (sensible) |
| `snmp/` | `snmp` | FLOW (supuesto) |
| `collector/` | `collector` | FLOW |
| `ingester/` | `ingester` | FLOW |
| `traffic/` | `traffic` | FLOW |
| `analytics/` | `analytics`, `reporting` | FLOW (`dashboards`: CORE) |
| `detection/` | `detection` | SEC |
| `alerts/` | `alerts` | SEC (supuesto) |
| `jobs/` | `jobs` | PLAT (supuesto) |

"Supuesto": módulo sin dueño explícito en [`docs/backlog/team.md`](../docs/backlog/team.md) §2;
se confirma al congelar el contrato C1. Layout interno: [`docs/conventions.md`](../docs/conventions.md) §2.1.
