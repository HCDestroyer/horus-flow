# 0021 — ClickHouse desde el primer incremento que guarde series o flujos

- Estado: Aceptada
- Fecha: 2026-10-07
- Decisores: product owner ([D4](../po-decisions.md)), Agente A (arquitectura, ronda 2)
- Modifica: [vision.md §14](../vision.md) (MVP técnico `… → SNMP → PostgreSQL`, ClickHouse
  "después") y el calendario de [roadmap.md](../roadmap.md) que lo dejaba para el Sprint 6.
  Complementa [ADR-0008](0008-clickhouse-para-analitica.md), que sigue vigente en el *qué*.

## Contexto

`vision.md §14` validaba la arquitectura con SNMP guardado en PostgreSQL y dejaba ClickHouse para
después; el Sprint 0 detectó que series SNMP en PostgreSQL no escalan y propuso adelantarlo o
usar una tabla puente particionada en PostgreSQL con migración posterior (roadmap C-03, database.md
§10). El PO decidió ([D4](../po-decisions.md)) que ClickHouse puede existir desde donde se
necesite. Además, con [D1](../po-decisions.md) y [D5](../po-decisions.md) el primer valor del
producto (clientes descubiertos, tráfico por IP, señales de botnet) **sale de los flujos**, que
solo pueden vivir en ClickHouse.

## Decisión

- **ClickHouse se despliega en el primer incremento que guarde series temporales o flujos**
  (métricas SNMP o flujos, lo que llegue antes según [roadmap.md](../roadmap.md)). Con la
  ordenación por valor de [ADR-0023](0023-entrega-por-incrementos-y-equipo-ia.md), eso es el
  **primer incremento con datos de red**.
- **Se elimina la tabla puente** de series en PostgreSQL: ninguna métrica ni flujo se guarda en
  PostgreSQL ni siquiera temporalmente. PostgreSQL guarda solo el estado actual/deseado
  (inventario, último estado observado proyectado, configuración).
- El esquema ClickHouse nace **multi-tenant** (`tenant_id` primero en el `ORDER BY`, row policies;
  [ADR-0017](0017-multi-tenant-desde-v1.md)) y con la clave natural del cliente
  (`realm_id`, `client_ip`; [ADR-0018](0018-la-ip-es-el-cliente.md)).
- Migraciones de esquema ClickHouse versionadas desde el primer día, igual que las de PostgreSQL,
  con tablas *publicadas* como contrato ([ADR-0008](0008-clickhouse-para-analitica.md)).
- Topología inicial: nodo único. En el despliegue mínimo de un solo servidor
  ([ADR-0025](0025-binario-modular-con-roles.md)) comparte host con PostgreSQL, con límites de
  memoria explícitos (`max_server_memory_usage_to_ram_ratio`) para proteger el plano de
  administración; se separa a su propio host cuando la ingesta lo exija
  ([architecture.md §9](../architecture.md#9-límites-de-escala-esperados)).

## Alternativas consideradas

- **SNMP en PostgreSQL y ClickHouse más tarde** (vision §14): obliga a escribir y luego migrar un
  almacén de series y retrasa el valor de los flujos.
- **Tabla puente en PostgreSQL con 7 días de retención**: menos piezas al inicio, pero es código
  desechable y una migración segura; con agentes de IA es más barato hacerlo bien una vez.
- **TimescaleDB para series SNMP**: añadiría una extensión y no sirve para flujos.

## Consecuencias

- (+) Un solo almacén de telemetría desde el principio; sin migraciones de datos de series.
- (+) El primer incremento ya valida el camino caliente (collector → NATS → ingester → ClickHouse)
  con datos reales de MikroTik.
- (−) El primer incremento opera dos bases de datos (PostgreSQL + ClickHouse); se acepta porque
  el riesgo operativo se adelanta a un momento de poco volumen.
- (−) Más RAM en el despliegue mínimo (ClickHouse ≥ 8–16 GB recomendados).
- Impacto: [database.md](../database.md) (eliminar la alternativa de tabla puente),
  [roadmap.md](../roadmap.md) (ClickHouse en el primer incremento con datos),
  [storage.md](../storage.md).
