# 0008 — ClickHouse para analítica y telemetría

- Estado: Aceptada
- Fecha: 2026-10-07

## Contexto

Flujos (de decenas de miles a millones de registros/s según escala, ver
[architecture.md §9](../architecture.md#9-límites-de-escala-esperados)), métricas SNMP, agregados
por cliente/servicio/ASN/categoría e históricos de años requieren un almacén columnar con
inserción masiva y consultas agregadas rápidas.

## Decisión

**ClickHouse** (single node en v1, en host dedicado con NVMe local) para toda la telemetría y
analítica:

- Motores `MergeTree` particionados por día, `ORDER BY` pensado para las consultas dominantes
  (detalle en [database.md](../database.md) y [traffic-model.md](../traffic-model.md)); vistas
  materializadas para agregados 5 min / 1 h / 1 día (granularidad fijada en [database.md](../database.md)); TTL por tabla según [storage.md](../storage.md).
- Inserción **solo por lotes** (≥ 50k filas o cada 5 s) desde ingesters, con
  `insert_deduplication_token` por lote para reintentos idempotentes.
- **Propiedad**: cada tabla tiene **un único escritor**. Las tablas marcadas como *publicadas*
  tienen esquema versionado y pueden ser **leídas** por `analytics`, `detection` y `alerts`. Esta
  es una excepción explícita a "ningún servicio lee la BD de otro": mover miles de millones de
  filas por eventos o gRPC para respetar la regla sería absurdo. Las tablas no publicadas son
  privadas.
- Usuarios ClickHouse por servicio con permisos de escritura solo en sus tablas y lectura solo en
  publicadas.
- Sin uso del NAS como disco de ClickHouse en v1 (ver [ADR-0010](0010-minio-sobre-nas.md)).

## Alternativas consideradas

- **TimescaleDB / PostgreSQL particionado**: insuficiente para > 100k filas/s y compresión de
  flujos.
- **Elasticsearch/OpenSearch**: más caro en disco y RAM para datos numéricos agregables.
- **Apache Druid / Pinot**: excelentes para OLAP en tiempo real, pero operación mucho más compleja.
- **VictoriaMetrics/Prometheus para SNMP**: apto para series, no para flujos de alta cardinalidad
  (IP, ASN, cliente); se reserva para la observabilidad de la plataforma, no para datos de negocio.

## Consecuencias

- (+) Compresión ~20 B/fila de flujo, consultas agregadas sub-segundo sobre miles de millones de
  filas.
- (+) Un solo motor para flujos y métricas SNMP.
- (−) No transaccional, actualizaciones costosas: los datos son append-only; correcciones se hacen
  por re-agregación.
- (−) Nodo único sin réplica en v1: si se pierde el disco, se pierden los datos no respaldados
  (ver [disaster-recovery.md](../disaster-recovery.md)). Se acepta porque es telemetría
  reconstruible parcialmente y no bloquea el plano de administración.
- (−) A partir de ~200 routers sin muestreo se requiere clúster (shards) o muestreo.
