# services/cmd/horus — binario único `horus`

- **Propósito:** `main` único del backend. Lee `HORUS_ROLES` (lista separada por comas o `all`)
  y compone los módulos de `services/<módulo>/` correspondientes
  ([ADR-0025](../../../docs/adr/0025-binario-modular-con-roles.md),
  [`docs/services.md`](../../../docs/services.md) §1).
- **Dueño:** PLAT (arranque y registro de roles; revisa CORE) — [`docs/backlog/team.md`](../../../docs/backlog/team.md) §2.
- **Estado (I0-01):** solo imprime la versión, los roles disponibles y los seleccionados; valida
  que los roles existan. Registro de módulos, configuración, logs, métricas, health checks por rol
  y apagado ordenado llegan con **I0-04**.

```bash
make build                          # ./bin/horus con la versión de git
HORUS_ROLES=collector ./bin/horus
go run ./services/cmd/horus         # HORUS_ROLES vacío = all
```

Roles: `gateway`, `auth`, `devices`, `wireguard`, `snmp`, `traffic`, `detection`, `alerts`,
`analytics`, `reporting`, `ingester`, `jobs`, `collector`, `wg-agent`.
