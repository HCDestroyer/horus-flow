# 0007 — PostgreSQL como fuente de verdad transaccional

- Estado: Aceptada (ajustada por ADR-0017: `tenant_id` + RLS; y ADR-0019: WAL a repositorio local)
- Fecha: 2026-10-07

## Contexto

Usuarios, roles, ACL, routers, sitios, clientes, WireGuard, configuración, categorías, reglas,
alertas y auditoría son datos relacionales, de bajo volumen, con integridad referencial y
transacciones. Varios servicios los poseen y no deben compartir tablas (P2).

## Decisión

**PostgreSQL** (versión mayor estable más reciente, fijada) como fuente de verdad transaccional.

- **Una instancia** en v1, con **un esquema y un rol por servicio** (`auth`, `devices`,
  `wireguard`, `traffic`, `detection`, `alerts`, `analytics`). Cada rol solo tiene permisos sobre
  su esquema. Sin claves foráneas entre esquemas: las referencias cruzadas son UUIDv7 "lógicos"
  validados por eventos/gRPC.
- Tipos: UUIDv7 como PK, `timestamptz` en UTC, `organization_id` en entidades raíz.
- Migraciones versionadas por servicio (herramienta a definir en [conventions.md](../conventions.md)), ejecutadas como paso explícito.
- Tabla `outbox` por esquema ([ADR-0016](0016-transactional-outbox.md)).
- Backups: base + archivado continuo de WAL en almacenamiento local con pgBackRest (PITR; ADR-0019) según [disaster-recovery.md](../disaster-recovery.md).
- **No** se guardan series temporales de telemetría en PostgreSQL (van a ClickHouse); como
  máximo el "último valor" proyectado.
- Pool de conexiones por servicio (`pgxpool`, máx. ~10 por réplica); PgBouncer solo si el número
  de réplicas lo exige.

## Alternativas consideradas

- **Una base por servicio en instancias separadas**: aislamiento físico, pero multiplica
  operación y backups; el esquema por servicio da el mismo límite lógico y permite separar
  después con `pg_dump` del esquema.
- **MySQL/MariaDB**: menos capacidades (tipos, JSONB, índices parciales, `SKIP LOCKED` maduro).
- **MongoDB**: descartado explícitamente en `vision.md`.
- **TimescaleDB para métricas**: tentador para SNMP, pero duplicaría el motor analítico; ClickHouse
  ya es necesario para flujos.

## Consecuencias

- (+) Integridad, transacciones, tooling maduro, PITR.
- (+) Separación por esquema permite extraer un servicio a su propia instancia sin cambiar código.
- (−) Instancia única = SPOF del plano de administración; HA (réplica en streaming + failover) se
  aborda en Sprint 14.
- (−) Sin FKs entre servicios: la consistencia entre dominios es eventual.
