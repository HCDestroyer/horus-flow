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

## Ingesta de flujos (I1-04, I1-05, I1-07, I1-09)

Con `HORUS_NATS_URL` el rol consume `TLM_FLOWS` (durable `flows-ingester`) y por cada lote:

1. valida que `Horus-Tenant` coincide con el lote y con el tenant del router (si no, DLQ);
2. **atribuye** con la regla verificada con un router real (`docs/traffic-model.md` §4.4):
   `src` en prefijo de clientes → subida; si no, `postNATDestinationIPv4Address` (IE 226) en
   prefijo → bajada; si no, `dst` en prefijo → bajada; si no, transit / infrastructure / unknown.
   IPv6: cliente = prefijo delegado truncado a `ipv6_client_len` (§4.8); `fe80::/10` y `ff02::/16`
   son infraestructura. En `unknown` guarda en `client_ip` la candidata del lado
   `customer_edge` solo si es privada/CGNAT/ULA o de un ASN del ISP (modo descubrimiento, I1-29);
3. **enriquece**: ASN/prefijo/país/organización (snapshot ASN, con los rangos publicados del
   catálogo como respaldo), servicio/categoría del catálogo (`services/traffic/api/catalog`) y
   reputación (`reputation_*`, snapshot de detection). Los snapshots se recargan en caliente desde
   `datasets.SnapshotDir`; uno corrupto se rechaza;
4. inserta en `flows.flows_raw` con `insert_deduplication_token = batch_id` (migración
   `20261009120000`: `non_replicated_deduplication_window`) y confirma tras el INSERT; con
   ClickHouse caído reintenta sin soltar el lote (el stream retiene lo pendiente);
5. **descubre clientes** sin escribir en PostgreSQL: `horus.flows.client.first_seen.<realm_id>`
   (FLOWS_EVENTS, cada 10 s, deduplicado con TTL, límite por realm/minuto) y
   `horus.telemetry.flows.client_activity.<realm_id>` (horario). Conocidos: inventario +
   `customer.discovered/reactivated/purged` (durable `ingester-known-clients`).

Sirve `GET /api/v1/flow-exporters[/{router_id}]` (permiso `flows.read`) con el estado que el
collector guarda en el bucket KV `flow_exporter_state`.

| Variable | Defecto | Uso |
| --- | --- | --- |
| `HORUS_NATS_URL` | — | Activa la ingesta |
| `HORUS_FLOWS_INVENTORY_FILE` | — | Inventario base (con NATS se completa con DEVICES_EVENTS) |
| `HORUS_INGESTER_WORKERS` | 4 | Lotes en paralelo |
| `HORUS_INGESTER_FIRST_SEEN_INTERVAL` / `_FIRST_SEEN_TTL` | 10s / 1h | Descubrimiento |
| `HORUS_INGESTER_DISCOVERY_PER_MINUTE` / `_DISCOVERY_REALM_MAX` | 2000 / 1048576 | Anti-avalancha |
| `HORUS_ASN_SNAPSHOT_DIR`, `HORUS_CATALOG_SNAPSHOT_DIR`, `HORUS_REPUTATION_SNAPSHOT_DIR` | — | Snapshots (catálogo semilla embebido si falta) |
| `HORUS_JWT_PUBLIC_KEYS`, `HORUS_AUTH_ISSUER` | — | API si `auth` no es local |

Métricas: `horus_ingester_{batches,rows}_total`, `horus_ingester_insert_seconds`,
`horus_ingester_consumer_pending`, `horus_customers_discovery_throttled_total`,
`horus_ingester_remote_bytes{,_with_asn,_with_service}_total{tenant_id}` (cobertura de
enriquecimiento por ISP), `horus_ingester_snapshot_reloads_total{kind,result}`.

Tests: `make test-ingester` (unitarios + NATS en proceso + ClickHouse efímero) y el test dorado
`make test-flows-golden` (`tests/flows`).
