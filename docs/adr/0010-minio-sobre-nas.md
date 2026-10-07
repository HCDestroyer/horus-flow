# 0010 — MinIO (S3) sobre el NAS para objetos, archivo y backups

- Estado: Sustituido por ADR-0019
- Fecha: 2026-10-07

## Contexto

Se necesita almacenamiento de objetos para reportes exportados, snapshots del catálogo de
clasificación, archivo histórico de ClickHouse, backups de PostgreSQL/ClickHouse y WAL. El ISP
dispone de un NAS. `vision.md` propone `Aplicación → S3 API → MinIO → NAS` y después cloud.

## Decisión

- Toda la aplicación usa **exclusivamente la API S3** (nunca rutas de archivo del NAS). Así el
  destino puede cambiar a cloud (S3, R2, B2, GCS vía interoperabilidad) sin tocar código.
- **MinIO** como servidor S3. Despliegue preferido: MinIO **en el propio NAS** (si soporta
  contenedores) o en un host con el volumen del NAS montado por **iSCSI/bloque**; **evitar NFS/SMB**
  como backend de MinIO (MinIO no lo soporta oficialmente: semántica de locks y consistencia).
  Decisión final en [Q4](../open-questions/architecture.md#q4).
- **Nada caliente en el NAS**: ni datos de PostgreSQL, ni de ClickHouse, ni de NATS. ClickHouse
  **no** usa MinIO/NAS como disco de tiering en v1; el archivo es una exportación (Parquet) que se
  confirma antes de que la TTL borre la partición.
- Buckets por propósito (`backups-postgres`, `backups-clickhouse`, `wal`, `archive`, `reports`,
  `catalog-snapshots`), con versionado y *object lock* en buckets de backup si el NAS lo permite.
  Detalle en [storage.md](../storage.md).
- Réplica posterior MinIO → cloud (bucket replication o `rclone`) para la copia fuera de sitio.

## Alternativas consideradas

- **Montar el NAS por NFS directamente en los servicios**: acopla código a rutas, sin API de
  objetos, sin URLs prefirmadas, migración a cloud costosa.
- **Usar S3 cloud desde el día uno**: más simple y durable, pero el PO prefiere el NAS existente
  (coste/soberanía); la API S3 deja la puerta abierta.
- **SeaweedFS / Garage / Ceph RGW**: alternativas S3 válidas; MinIO es la más conocida. Se
  reconsidera si cambian las condiciones de licencia/distribución de MinIO ([Q11](../open-questions/architecture.md#q11)).
- **ClickHouse con disco S3 (tiering) sobre MinIO**: reduce disco local, pero hace que consultas
  sobre datos fríos dependan del NAS; se pospone.

## Consecuencias

- (+) Caída del NAS no afecta el producto interactivo ([architecture.md §10.5](../architecture.md#105-el-nas-deja-de-responder)).
- (+) Camino a cloud sin cambios de código.
- (−) El archivado de WAL hacia MinIO puede llenar el disco de PostgreSQL si el NAS cae mucho
  tiempo: requiere alertas y límites.
- (−) El rendimiento y la durabilidad dependen del NAS (RAID, discos); MinIO de nodo único no da
  redundancia propia.
