# Estrategia de almacenamiento de objetos — Horus Flow

> Estado: **borrador Sprint 0** · Dueño: Agente 2 (Arquitecto de datos) · Fuente: [`vision.md`](vision.md) §4 y Sprint 13.
>
> Relacionados: [`database.md`](database.md) (retención en ClickHouse/PostgreSQL),
> [`traffic-model.md`](traffic-model.md) (artefactos del catálogo),
> [`disaster-recovery.md`](disaster-recovery.md) (Agente 4: RPO/RTO, procedimientos de restauración,
> copia fuera del sitio), [`security.md`](security.md) (cifrado, credenciales de MinIO),
> [`open-questions/data.md`](open-questions/data.md).

---

## 1. Principios

1. **Las aplicaciones solo hablan S3 API** (`Aplicación → S3 API → MinIO → NAS`). Ningún servicio
   monta el NAS ni usa rutas de archivo compartidas. Así el paso a cloud storage (o a otro servidor
   S3) es un cambio de endpoint/credenciales.
2. **El almacenamiento de objetos no está en el camino caliente.** PostgreSQL y ClickHouse usan
   discos locales (SSD/NVMe). MinIO guarda backups, archivos históricos, exportaciones y artefactos.
   Si el NAS cae, la plataforma sigue operando (§8).
3. **Todo objeto es inmutable** (se escribe una vez, nunca se modifica en sitio); los "cambios" son
   claves nuevas. Facilita versionado, object lock, replicación y checksums.
4. **Cada objeto lleva su verificación**: SHA-256 en metadatos (`x-amz-meta-sha256`) y, para
   conjuntos, un `manifest.json`.

---

## 2. Topología MinIO ↔ NAS

Decisión base en [ADR-0010](adr/0010-minio-sobre-nas.md) (MinIO en el NAS o con volumen iSCSI,
nada caliente en el NAS, sin tiering de ClickHouse en v1). Esta sección solo detalla el impacto en
almacenamiento.

**Riesgo importante**: MinIO no soporta oficialmente backends de archivos en red (NFS/SMB): puede
haber corrupción o bloqueos por semántica de `fsync`/locks. Opciones, en orden de preferencia:

| Opción | Descripción | Valoración |
|--------|-------------|------------|
| A | **MinIO corriendo en el propio NAS** (contenedor/app del NAS) sobre sus discos locales | Recomendada si el NAS lo permite (TrueNAS, Synology, QNAP con Docker). |
| B | MinIO en un host Linux con un **LUN iSCSI** del NAS montado como disco de bloques (XFS) | Aceptable; un solo escritor, semántica de bloque. |
| C | MinIO sobre **NFS** | **No recomendada.** Solo para laboratorio. |
| D | NAS con **S3 nativo** (algunos NAS lo ofrecen) en lugar de MinIO | Viable si la API S3 es completa (object lock, versionado, lifecycle). |

Además: el modo de un solo nodo/un solo disco de MinIO **no tiene erasure coding** ⇒ la protección
contra fallo de disco la da el RAID del NAS, y contra pérdida del NAS, la copia fuera del sitio (§7).

**Riesgo de producto**: la edición comunitaria de MinIO (AGPLv3) ha reducido funcionalidades y
distribución en 2025 (consola de administración recortada, foco en la edición comercial). Ya está
recogido como pregunta abierta de arquitectura (Q11, ADR-0010); plan B: un servidor S3 compatible
(p. ej. Garage, SeaweedFS, Ceph RGW o el S3 nativo del NAS). Como las aplicaciones solo usan
S3 API (incluido object lock y lifecycle), el cambio no afecta al modelo.

---

## 3. Qué se guarda en objeto (y qué no)

| Contenido | ¿En MinIO? | Bucket | Justificación |
|-----------|-----------|--------|---------------|
| Backups de PostgreSQL (base) | **Sí** | `backups-postgres` | Recuperación punto en el tiempo (con WAL). |
| WAL de PostgreSQL | **Sí** | `wal` | Archivado continuo; bucket separado para lifecycle y permisos propios. |
| Backups de ClickHouse (nativos) | **Sí** | `backups-clickhouse` | DR de agregados (lo crudo se puede perder sin drama, lo agregado no). |
| Archivo histórico de ClickHouse (Parquet) | **Sí** | `archive` | Retención larga fuera de CH, consultable con `s3()`/DuckDB. |
| Exportaciones de reportes (PDF, CSV, XLSX) | **Sí** | `reports` | Descarga mediante URL prefirmada de corta duración, emitida por `analytics` (rol reporting-worker) vía `api-gateway`. |
| Snapshots del catálogo de clasificación | **Sí** | `catalog-snapshots` | Inmutables por versión; los publica `traffic-intelligence` y los cargan todas las réplicas del ingester de `flows` (y `analytics` para `dim.*`). |
| Snapshots de fuentes externas (RIB, iptoasn, PeeringDB, rangos cloud, GeoLite2) | **Sí** | `datasets` | Reproducibilidad de qué prefijo→ASN se usó; respetar licencias (no redistribuir). |
| Snapshots de reputación | **Sí** | `catalog-snapshots` (`reputation/`) | Igual que catálogo; los publica `detection`. |
| Exportaciones de auditoría (particiones antiguas) | **Sí** | `audit` (object lock) | Evidencia inalterable. |
| Configuraciones de routers (backup de config, futuro) | Sí (futuro) | `device-configs` | Cifradas por objeto (SSE-KMS); contienen secretos. Fuera de alcance v1. |
| **Configuraciones WireGuard (`.conf`)** | **No** | — | Contienen la clave privada del peer. Se generan bajo demanda desde PostgreSQL ([`database.md` §2.3](database.md)). El estado de WireGuard queda protegido por el backup de PostgreSQL (claves cifradas con KEK que **no** está en MinIO). |
| Claves de cifrado (KEK), secretos | **Nunca** | — | Si un backup y su KEK viven juntos, el cifrado no protege nada. Ver [`security.md`](security.md). |
| Datos calientes de PG/CH (tablespaces, discos S3 de CH) | **No en v1** | — | Ver §6.3. |
| Logs (Loki) | Opcional | `loki` | Si Loki usa backend S3; lo decide Agente 4 ([`observability.md`](observability.md)). |

---

## 4. Buckets, convención de claves, versionado, object lock y lifecycle

Un MinIO por entorno (dev/staging/prod) ⇒ nombres de bucket sin sufijo de entorno. Nombres base
de [ADR-0010](adr/0010-minio-sobre-nas.md) (`backups-postgres`, `backups-clickhouse`, `wal`,
`archive`, `reports`, `catalog-snapshots`) más `datasets` y `audit` propuestos aquí.

### 4.1 Convención de claves

```
<bucket>/<servicio|origen>/<tipo>/[<dimensión>=<valor>/...]<fecha-particion>/<nombre>.<ext>
```

- Fechas siempre UTC, estilo Hive (`dt=2026-10-07/`, `month=2026-10/`) para que ClickHouse `s3()`,
  DuckDB o Spark hagan *partition pruning*.
- Nombres de archivo con UUIDv7 o con el ID lógico (`v42`, `run_<uuid>`); nunca nombres
  proporcionados por el usuario sin sanear.
- Sin datos personales en las claves (no nombres de cliente; sí `customer_id`).

Ejemplos:

```
backups-postgres/horus/base/dt=2026-10-07/base_20261007T020000Z.tar.zst
wal/horus/000000010000002A000000F3.zst
backups-clickhouse/native/dt=2026-10-07/full_20261007T030000Z/...
archive/clickhouse/flows.customer_1h/month=2026-09/part-0000.parquet
archive/clickhouse/flows.customer_1h/month=2026-09/manifest.json
archive/clickhouse/flows.flows_raw/dt=2026-10-06/site_id=<uuid>/part-0000.parquet
reports/analytics/run_<uuidv7>/consumo_clientes_2026-09.xlsx
catalog-snapshots/traffic/v42/catalog.pb.zst
catalog-snapshots/traffic/v42/manifest.json
catalog-snapshots/reputation/v1871/snapshot.pb.zst
datasets/routeviews_rib/dt=2026-10-07/rib.20261007.0000.bz2
audit/auth/month=2024-09/audit_log.parquet
```

### 4.2 Configuración por bucket

| Bucket | Versionado | Object lock | Lifecycle | Escritor / lector |
|--------|-----------|-------------|-----------|-------------------|
| `backups-postgres` | Sí | **Sí, governance, 35 días** | base: expira a 35 d (diarios), conservar 12 mensuales 13 meses (tag `retention=monthly`) | herramienta de backup (rol dedicado) / restore |
| `wal` | Sí | **Sí, governance, 35 días** | 35 d (debe cubrir desde el base backup más antiguo que se quiera restaurar con PITR) | `archive_command` de PostgreSQL / restore |
| `backups-clickhouse` | Sí | **Sí, governance, 35 días** | 35 d semanales/diarios incrementales; 12 mensuales | job de backup CH |
| `archive` | Sí | Opcional (governance 1 año) | flujos crudos archivados: 90 d (si se activa); agregados: 5 años; versiones no actuales: 7 d | archiver de `analytics` / `analytics` (solo lectura) |
| `reports` | No | No | **7 días** (los reportes se regeneran) | `analytics` (reporting-worker) |
| `catalog-snapshots` | Sí | No | conservar todo (pequeño, < 100 MB/año) | `traffic-intelligence`, `detection` / ingesters de `flows`, `analytics` |
| `datasets` | No | No | 90 días | traffic-intelligence |
| `audit` | Sí | **Sí, compliance, = retención legal (propuesta 5 años)** | expira al final del lock | auth |

- **Governance** en backups: protege contra borrado accidental o ransomware con credenciales
  normales; un rol de "break-glass" con `s3:BypassGovernanceRetention` (custodiado, auditado) puede
  liberar. **Compliance** solo en auditoría (ni el administrador puede borrar antes de tiempo):
  elegir solo si hay obligación legal, porque es irreversible.
- Object lock exige crear el bucket con lock habilitado desde el inicio (no se puede activar
  después) ⇒ lo crea el script de infraestructura del Sprint 1.
- Credenciales: un usuario/política MinIO por servicio, con acceso solo a su prefijo/bucket; los
  backups usan credenciales distintas de las de la aplicación (una app comprometida no puede borrar
  backups). Gestión de secretos: [`security.md`](security.md).
- Cifrado en reposo: SSE de MinIO (KMS/KES) recomendado para `backups-*`, `audit`,
  `device-configs`; además los dumps de PostgreSQL ya contienen secretos cifrados por la
  aplicación.

---

## 5. Política de retención por tipo de dato

| Tipo de dato | Sistema caliente | Retención caliente | Archivo (MinIO) | Retención archivo |
|--------------|------------------|--------------------|-----------------|-------------------|
| Flujos crudos | ClickHouse `flows.flows_raw` | 7 d (7–30) | Parquet diario **opcional** (default: on ≤ 100 routers, off > 100) | 90 d |
| Tráfico 5 min | ClickHouse | 90 d | No | — |
| Tráfico 1 h | ClickHouse | 13 meses | Parquet mensual | 5 años |
| Tráfico 1 día | ClickHouse | 5 años | Parquet anual | 10 años (barato) |
| SNMP crudo / 5 min / 1 h / 1 d | ClickHouse | 30 d / 90 d / 13 m / 5 a | No (salvo 1 d anual) | — |
| WireGuard métricas | ClickHouse | 90 d / 13 m | No | — |
| Scores de detección | ClickHouse | 5 años | Parquet anual | 5 años |
| Inventario, usuarios, config | PostgreSQL | indefinida (borrado lógico) | backups | ver §4.2 |
| Asignaciones IP→cliente | PostgreSQL | 13 meses (≥ mayor retención que las use) | Parquet mensual | **retención legal** (Q4) |
| Auditoría | PostgreSQL | 2 años | Parquet mensual con object lock | 5 años (o legal) |
| Alertas y notificaciones | PostgreSQL | 13 meses / 90 d | Parquet mensual (alertas) | 5 años |
| Sesiones, refresh tokens | PostgreSQL / Redis | hasta expiración + 30 d | No | — |
| Reportes exportados | MinIO | — | `reports` | 7 d |
| Catálogos de clasificación | PostgreSQL + MinIO | todas las versiones | `catalog-snapshots` | indefinida |
| Snapshots de fuentes externas | MinIO | — | `datasets` | 90 d |
| Backups PostgreSQL / ClickHouse | MinIO | — | `backups-*`, `wal` | 35 d + 12 mensuales |

Todos los valores son **propuestas por defecto configurables** por instalación; los números
definitivos dependen de requisitos legales del país del ISP (Q4) y de disco disponible
([`database.md` §8](database.md)).

---

## 6. Archivado ClickHouse → MinIO

### 6.1 Dos mecanismos con propósitos distintos

| | **Backup nativo** (`BACKUP … TO S3`) | **Archivo Parquet** (`INSERT INTO FUNCTION s3(…, 'Parquet')`) |
|---|---|---|
| Propósito | Disaster recovery: restaurar la base tal cual | Retención larga y consulta fuera de CH |
| Contenido | Agregados, dimensiones, metadatos (esquema) — **no** `flows_raw` por defecto (volumen; es reconstruible hasta donde importa) | Particiones cerradas de agregados (y crudo si se activa) |
| Formato | Formato interno de CH (partes + checksums propios), incrementales con `base_backup` | Parquet (ZSTD), columnar, abierto |
| Restaura | `RESTORE` completo o por tabla | `INSERT INTO tabla SELECT * FROM s3(...)` o consulta directa |
| Frecuencia | diario incremental, semanal completo | al cerrar partición (día+1 / mes+1) |
| Herramienta | SQL `BACKUP/RESTORE` de ClickHouse o `clickhouse-backup` | job *archiver* de `analytics` ([`services.md`](services.md)) |

### 6.2 Proceso de archivado Parquet (por partición)

1. Elegir partición cerrada (p. ej. `customer_1h` de `2026-09`, en `2026-10-02`).
2. `INSERT INTO FUNCTION s3('…/month=2026-09/part-{_partition_id}.parquet', 'Parquet') SELECT …
   FINAL` — se exportan **valores finalizados** (`sumMerge`, `uniqMerge` → `UInt64`), no estados
   binarios de `AggregateFunction`, para que el Parquet sea legible por cualquier herramienta y
   estable entre versiones de CH. Se exporta también la versión de esquema.
3. Escribir `manifest.json`: tabla, partición, esquema (columnas/tipos), versión de catálogo
   min/max, `row_count`, `sum(bytes)`, `min/max(bucket)`, SHA-256 de cada archivo, versión de CH,
   fecha de exportación.
4. **Verificación**: leer de vuelta con `SELECT count(), sum(bytes) FROM s3(...)` y comparar con el
   manifiesto y con la tabla de origen. Solo si coincide se marca la partición como archivada en
   `analytics.archive_manifest` (PostgreSQL, [`database.md` §2.6](database.md)).
5. **Borrado condicionado al archivo** (como exige [`architecture.md` §10.5](architecture.md)): en
   las tablas con archivo activado, la expiración la hace el archiver con
   `ALTER TABLE … DROP PARTITION` **solo** para particiones con manifiesto verificado; el TTL de la
   tabla se fija en `retención + 30 días` como red de seguridad (evita disco lleno si el archiver
   muere). Como `analytics` no es escritor de `flows.*`, el `DROP PARTITION` se ejecuta con un
   usuario CH de mantenimiento con permiso `ALTER DELETE/DROP PARTITION` sobre esas tablas
   (excepción acotada al escritor único; anotarla en ADR-0008 si el Agente 1 lo acepta). Tablas
   sin archivo (5 min, SNMP crudo) expiran por TTL normal.
6. **Restauración de prueba** mensual automatizada: restaurar una partición aleatoria en una base
   temporal y comparar agregados (requisito del Sprint 13: "restauración, checksum").

### 6.3 Tiered storage de ClickHouse sobre S3 (descartado en v1)

ClickHouse puede mover partes viejas a un disco S3 (`TTL … TO VOLUME 'cold'`). Se descarta en v1:
si el NAS cae, consultas y merges sobre esas partes fallan o se bloquean, acoplando la
disponibilidad de ClickHouse a la del NAS. Se reevalúa con almacenamiento en cloud fiable o con
MinIO distribuido.

---

## 7. Camino a cloud storage

Fases:

1. **v1**: MinIO sobre NAS, un sitio. Copia fuera del sitio de backups (requisito de DR, regla
   3-2-1; define RPO el Agente 4).
2. **Replicación**: replicación de buckets de MinIO (*bucket replication*, asíncrona) o `rclone`/
   `mc mirror` programado hacia un S3 en la nube (AWS S3, Backblaze B2, Wasabi, Cloudflare R2, GCS
   en modo interoperable) para `backups-*`, `audit`, `archive`, `catalog-snapshots`.
   Object lock también en destino.
3. **Tiering**: lifecycle con *transition* a clase fría (p. ej. S3 Glacier Instant/Flexible) para
   `archive` > 1 año.
4. **Cloud primario** (si se migra la plataforma): cambiar endpoint; mismas claves y buckets.

Requisitos que ya cumplimos para que esto sea barato: solo S3 API; sin dependencias de
características propias de MinIO en la aplicación (salvo administración); claves estables; checksums
en metadatos; cifrado del lado de aplicación para datos sensibles (no depender del KMS del proveedor).

Costo orientativo (a validar): 100 routers ⇒ agregados archivados ~0,2 TB/año + backups ~1 TB ⇒
pocos USD/mes en almacenamiento frío; el egress de restauración es el costo relevante.

---

## 8. ¿Qué pasa si el NAS (MinIO) no responde?

Responde a la pregunta del Sprint 0 *"¿si el NAS deja de responder?"* desde el punto de vista del
almacenamiento (el plan operativo completo está en [`disaster-recovery.md`](disaster-recovery.md)).

| Función | Comportamiento | Impacto |
|---------|----------------|---------|
| Login, inventario, WireGuard, SNMP, flujos, dashboards | **Sin impacto**: PG/CH/Redis/NATS en discos locales | ninguno |
| Backups de PostgreSQL | WAL se acumula localmente (`archive_command` reintenta); alerta si el directorio `pg_wal` supera un umbral (propuesta 70 % del volumen) | RPO de DR se degrada mientras dure; riesgo de llenar disco si dura días ⇒ alerta crítica |
| Backups de ClickHouse | Job falla, reintenta con backoff, alerta `backup_missed` | RPO se degrada |
| Archivado Parquet | Se pausa; el **ledger** registra particiones pendientes | ninguno si el corte < margen (abajo) |
| TTL de ClickHouse | Sigue borrando según calendario | **Riesgo de perder particiones no archivadas** ⇒ mitigación: margen |
| Exportación de reportes | El run queda `failed` con error explícito `storage_unavailable` y es reintentable; reportes pequeños (CSV < 10 MB) pueden servirse en streaming directo sin MinIO | degradado |
| Catálogo de clasificación | El ingester de `flows` usa la versión cargada en memoria y su copia en disco local; `traffic-intelligence` **no puede publicar** una versión nueva | ninguno en ingesta |
| Fuentes externas (RIB, iptoasn) | Import diario se salta; se mantiene el snapshot vigente | prefijos un día más viejos |
| Arranque en frío de un ingester de `flows` | Usa la última copia en disco local; si no tiene ninguna, inserta con clasificación `unknown` y `catalog_version = 0` (ADR-0015) y esas filas son candidatas a reclasificación | clasificación degradada |

**Archivo antes de borrar**: en tablas con archivo activado, una partición solo se elimina cuando
su manifiesto está verificado (§6.2 paso 5). Con el NAS caído las particiones se **retienen** en
ClickHouse (consumen disco extra: a 100 routers, ~69 GB/día de crudo si su archivo está activado) hasta
el TTL de seguridad (retención + 30 días). Alerta `archive_lag > 3 días`; si el disco de ClickHouse
supera el 80 %, el operador decide (runbook en [`disaster-recovery.md`](disaster-recovery.md))
entre ampliar disco o aceptar perder el archivo de crudo (los agregados no se ven afectados).

**Integridad tras la recuperación**: al volver el NAS, un job de verificación recorre los manifiestos
de los últimos N días y comprueba existencia + SHA-256 de los objetos (detecta escrituras parciales).
MinIO marca escrituras incompletas como inexistentes (las subidas multiparte no completadas se
limpian con la regla de lifecycle `AbortIncompleteMultipartUpload` = 2 días).

---

## 9. Dependencias

| Con | Tema |
|-----|------|
| Agente 4 | RPO/RTO; copia fuera del sitio; gestión de credenciales MinIO y KEK; retención legal de auditoría y asignaciones IP; SSE/KMS. |
| Agente 3 | URLs prefirmadas de reportes en `api.md`; evento `horus.reporting.report.generated` con `object_key`; `catalog.published` con `artifact_object_key`. |
| Agente 1 | Permiso de `DROP PARTITION` del archiver sobre tablas `flows.*` (excepción acotada a "escritor único" de ADR-0008); prefijos de MinIO de `services.md` (`reports/`, `archive/`, `catalog-snapshots/`) se tratan aquí como buckets. |
| Agente 5 | Historias Sprint 1 (crear buckets con object lock desde el inicio), Sprint 13 (archivado, verificación, restore de prueba). |
