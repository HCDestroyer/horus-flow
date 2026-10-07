# 0019 — Almacenamiento local primario + destino remoto opcional (sin MinIO ni NAS obligatorio)

- Estado: Aceptada
- Fecha: 2026-10-07
- Decisores: product owner ([D2](../po-decisions.md), [D3](../po-decisions.md)), Agente A
  (arquitectura, ronda 2)
- Sustituye: [ADR-0010](0010-minio-sobre-nas.md) (MinIO sobre el NAS). Ajusta
  [ADR-0015](0015-enriquecimiento-de-flujos-en-ingesta.md) (dónde se publica el snapshot del
  catálogo) y [ADR-0007](0007-postgresql-fuente-de-verdad.md) (destino del archivado de WAL).

## Contexto

ADR-0010 ponía toda la persistencia de objetos (backups, WAL, archivo de ClickHouse, reportes,
snapshots del catálogo) en **MinIO sobre el NAS** del ISP. El PO decidió que el **NAS es
opcional** ([D2](../po-decisions.md)): solo sirve para guardar copias, puede no existir, y lo
práctico es transferir por **SFTP** a almacenamiento privado (NAS, Google Drive, MEGA, MediaFire,
Dropbox). Además, con [D3](../po-decisions.md) MinIO deja de ser necesario (y su edición
comunitaria redujo funciones y distribución de binarios en 2025). Con multi-tenant
([ADR-0017](0017-multi-tenant-desde-v1.md)) habrá además varios ISP sin un NAS común.

## Decisión

### 1. Primario: sistema de archivos local

- Todo lo que antes iba a MinIO se escribe en un **volumen local de datos de Horus**
  (`HORUS_DATA_DIR`, p. ej. `/var/lib/horus/store`), idealmente en un **disco distinto** del de
  PostgreSQL/ClickHouse para que un backup sobreviva a la pérdida del disco de la base de datos.
- Estructura (detalle y retenciones en [storage.md](../storage.md), Agente B):

  | Ruta | Contenido | Productor |
  | --- | --- | --- |
  | `backups/postgres/` | Repositorio pgBackRest (base + WAL, PITR) | pgBackRest (`repo1`, tipo posix) |
  | `backups/clickhouse/` | Backups de ClickHouse | `clickhouse-backup` (disco local) o `BACKUP … TO Disk` |
  | `archive/<tenant_id>/` | Particiones vencidas exportadas a Parquet | rol `jobs` (archiver) |
  | `reports/<tenant_id>/` | PDF/CSV/Excel generados | rol `reporting` |
  | `audit/` | Exportaciones de auditoría vencidas | rol `jobs` |

- La aplicación accede a archivos solo a través de un **puerto `BlobStore`** (Put/Get/List/Delete,
  claves lógicas) con un adaptador `fs` en v1. Las descargas de reportes se sirven a través de la
  API (`/api/v1/reports/{id}/download`, autorizada por tenant), no por URLs prefirmadas.
- **Snapshots del catálogo de clasificación y de reputación**: se publican en **NATS Object Store**
  (bucket `catalog-snapshots`, `reputation-snapshots`), no en el sistema de archivos. Así cualquier
  proceso en cualquier host los descarga sin compartir disco y sin un servidor S3; el ingester
  conserva la última copia en su disco local como hoy.

### 2. Secundario: destino remoto opcional vía rclone

- Un **destino remoto** es configuración de plataforma (`platform.storage.manage`): tipo, ruta,
  credenciales cifradas, qué rutas replicar, calendario y retención remota. Puede haber 0, 1 o
  varios. **Cero destinos es una configuración válida** (el sistema avisa de que no hay copia fuera
  del servidor, pero funciona).
- Herramienta: **rclone** (licencia MIT), ejecutado por el rol `jobs` como proceso externo con
  configuración generada desde la BD. Orden de soporte:
  1. **SFTP** (NAS, servidor propio) — primer incremento.
  2. Google Drive, MEGA, Dropbox (backends nativos de rclone) — después.
  3. **MediaFire**: rclone no tiene backend estable; no se soporta salvo que el proveedor exponga
     WebDAV/SFTP ([Q24](../open-questions/architecture.md#q24)).
- **Cifrado del lado de Horus obligatorio** para destinos de nube de consumo (overlay `crypt` de
  rclone; clave en el gestor de secretos de Horus). Los backups contienen datos de clientes de
  todos los tenants.
- Semántica: `rclone copy` (no `sync` destructivo) de archivos inmutables; la retención remota es
  independiente de la local; verificación con `rclone check` periódico; estado por destino
  (`last_success_at`, bytes pendientes, error) en PostgreSQL y métricas
  (`horus_remote_sync_lag_seconds`, `horus_remote_sync_failures_total`).
- Destinos **por tenant** (cada ISP su NAS) quedan fuera de v1 ([Q20](../open-questions/architecture.md#q20));
  el modelo de configuración admite `tenant_id` nulo (plataforma) para añadirlos después.

### 3. Reglas que nada debe romper

1. **Ningún componente depende del destino remoto para funcionar.** Si no responde, solo crece el
   retraso de la copia remota (RPO fuera del sitio).
2. **El borrado local nunca espera a la copia remota** de datos calientes; sí espera a la
   exportación *local* (una partición de ClickHouse no se borra hasta que su Parquet está en
   `archive/` y verificado).
3. **Retención local mínima garantizada** (p. ej. 7 días de PITR de PostgreSQL y 2 backups
   completos de ClickHouse) independiente del remoto.
4. Cuota del volumen local con alertas a 70/85/95 %; al 95 % se pausan archivado y reportes antes
   de afectar a las bases de datos.

### 4. ¿Se mantiene algún object store S3?

**No en v1.** Nadie necesita la API S3: los backups usan repositorios posix con soporte remoto
propio (pgBackRest también soporta SFTP y S3; `clickhouse-backup` soporta SFTP y S3), el archivo y
los reportes son archivos locales, y los snapshots viajan por NATS Object Store. Un servidor S3
sería un proceso más sin consumidor.

Se añadirá un adaptador `s3` del `BlobStore` (y un servidor S3) **solo** si se cumple alguno de
estos disparadores: despliegue en varios hosts que necesiten compartir archivos de reportes/archivo
(p. ej. Kubernetes con réplicas de `reporting`), *tiering* de ClickHouse a almacenamiento de
objetos, o un cliente que exija S3 como destino. En ese caso, por licencia ([D3](../po-decisions.md)):
**SeaweedFS** (Apache-2.0) o un S3 gestionado; **Garage** se descarta por ser AGPL-3.0, y **MinIO**
por su licencia y su distribución comunitaria reducida.

## Alternativas consideradas

- **MinIO sobre el NAS** (ADR-0010): el NAS pasa a ser obligatorio y MinIO añade un proceso con
  licencia AGPL y distribución reducida. Contrario a D2/D3.
- **SeaweedFS/Garage local como capa S3**: mantiene la API S3 "por si acaso", pero es un proceso
  más que operar sin ningún consumidor que lo necesite hoy.
- **Montar el NAS por NFS/SMB y escribir directamente**: vuelve a hacer del NAS una dependencia
  en caliente y acopla rutas.
- **restic/borg en lugar de rclone**: excelentes para backups deduplicados y cifrados, pero con
  menos destinos (sin MEGA/Dropbox nativos en restic salvo vía rclone). Se puede reevaluar para
  la parte de backups; rclone cubre todos los destinos pedidos con una sola herramienta.
- **Backups directos a la nube desde pgBackRest/clickhouse-backup**: posible para SFTP/S3, pero
  no cubre Drive/MEGA/Dropbox y duplica la configuración de destinos.

## Consecuencias

- (+) Ningún componente depende del NAS ni de un servidor S3; un despliegue mínimo es un servidor
  con discos locales.
- (+) Un único mecanismo (rclone) para todos los destinos pedidos por el PO; añadir uno es
  configuración.
- (+) El archivado de WAL ya no puede llenar el disco de PostgreSQL por una caída del NAS: el WAL
  va a un repositorio local.
- (−) Sin destino remoto, un desastre del servidor (incendio, robo, disco de datos y de backups a
  la vez) pierde todo; se documenta como riesgo y la UI de plataforma lo muestra.
- (−) Los archivos viven en un host: escalar `reporting` a varios hosts exigirá el adaptador `s3`
  (disparador explícito arriba).
- (−) Hay que dimensionar el disco local para retención local + archivo; ver
  [storage.md](../storage.md).
- Impacto: [storage.md](../storage.md) (reescritura: rutas, retenciones local/remota),
  [disaster-recovery.md](../disaster-recovery.md) (pgBackRest posix + copia remota, RPO local vs
  remoto), [observability.md](../observability.md) (métricas de disco y de sincronización),
  [security.md](../security.md) (cifrado `crypt`, credenciales de destinos).
