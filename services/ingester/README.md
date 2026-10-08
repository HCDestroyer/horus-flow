# `mod:ingester` — services/ingester

- **Propósito:** Consumo de lotes de flujos y métricas, enriquecimiento, descubrimiento de IPs de clientes e inserción en ClickHouse.
- **Rol(es) de `horus`:** `ingester`
- **Agente dueño:** FLOW ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/ingester/api`.

## Esquema ClickHouse (I0-13)

Migraciones goose en [`migrations/clickhouse/`](migrations/clickhouse/) (ubicación de
`docs/database.md` §3), derivadas del contrato C3
(`packages/schemas/datastore/v0/clickhouse-contract-v0.sql`), embebidas en el binario
(`chschema.Migrations()`) y aplicadas con [`packages/go/chmigrate`](../../packages/go/chmigrate):

- al arrancar el rol `ingester` si hay `HORUS_CLICKHOUSE_DSN` (desactivable con
  `HORUS_INGESTER_CH_MIGRATE=false`); si falla, el rol no arranca;
- con `make migrate-ch` contra el compose de desarrollo (`services/ingester/cmd/ch-migrate`).

Reglas: nombre `<AAAAMMDDhhmmss>_<desc>.sql`, `-- +goose NO TRANSACTION` (ClickHouse no tiene DDL
transaccional), todo `CREATE … IF NOT EXISTS` para poder reintentar, `Down` solo para desarrollo,
tabla de control `flows.goose_db_version`. Las MV corren como `horus_mv` (`SQL SECURITY DEFINER`);
los diccionarios `dim.*_dict` leen como `horus_dict` (`HOST LOCAL`, sin contraseña, solo `SELECT dim.*`).

| Variable (admite `_FILE`) | Uso |
| --- | --- |
| `HORUS_CLICKHOUSE_DSN` | `clickhouse://usuario@host:9000/base` del migrador |
| `HORUS_CLICKHOUSE_PASSWORD` | contraseña del migrador |
| `HORUS_INGESTER_CH_MIGRATE` | `true` (defecto) migra al arrancar |
| `HORUS_CLICKHOUSE_{INGESTER,ANALYTICS,DETECTION,ALERTS,JOBS}_PASSWORD` | crea/rota el usuario `horus_<módulo>` con el rol `horus_<módulo>_role` (sin contraseña, no se crea) |

Tenancy: los lectores (`horus_tenant_reader`: analytics, detection, alerts) tienen la row policy
`p_tenant` (`tenant_id = toUUID(getSetting('SQL_horus_tenant'))`); una consulta sin
`SETTINGS SQL_horus_tenant = '<uuid>'` falla. `horus_jobs` lee sin filtro (`horus_platform_reader`).

Tests: `go test ./services/ingester/...` (estáticos) y, con el compose levantado,
`go test -tags integration -count=1 ./services/ingester/migrations/clickhouse/` (**borra** `flows`,
`dim` y los usuarios/roles `horus_*` del ClickHouse de destino; otro servidor con
`HORUS_CH_TEST_DSN` + `HORUS_CH_TEST_PASSWORD_FILE`).
