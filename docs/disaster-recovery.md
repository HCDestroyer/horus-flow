# Horus Flow — Backups, recuperación ante desastres y runbooks

> Estado: borrador Sprint 0 · Responsable: Agente 4.
> Fuente: [`vision.md`](vision.md) Sprints 0, 13, 14 y 16. Relacionados:
> [`architecture.md`](architecture.md) (modos de fallo y degradación — Agente 1),
> [`storage.md`](storage.md) y [`database.md`](database.md) (Agente 2), [`events.md`](events.md)
> (Agente 3), [`observability.md`](observability.md), [`security.md`](security.md).

Este documento cubre **cómo no perder datos y cómo volver**: RPO/RTO, backups, verificación,
runbooks y pruebas de caos. *Cómo se comporta el sistema mientras algo está caído* (degradación)
lo define el Agente 1 en `architecture.md`; aquí se enlaza.

## 1. Principios

1. **3-2-1:** tres copias, dos medios distintos, una fuera del sitio. El NAS no puede ser a la
   vez el almacenamiento primario y el único destino de backup.
2. **Backup no verificado = no hay backup.** Toda política incluye restauración automática de
   prueba y alerta de antigüedad.
3. **Datos primarios en disco local del servidor** (SSD/NVMe), no sobre el NAS por red
   (PostgreSQL, ClickHouse, NATS y Redis sobre NFS/SMB son fuente de corrupción y latencia).
   El NAS es destino de MinIO (archivo y backups).
4. **Infraestructura como código:** todo lo que no son datos (compose, configs, dashboards,
   reglas, definiciones de streams NATS, esquemas) está en Git y se recrea, no se respalda.
5. **Backups cifrados** y con credenciales distintas de las de producción; bucket de backups
   con versionado y Object Lock (ver [`security.md`](security.md) §3.8).

## 2. RPO / RTO por componente

RPO = pérdida máxima de datos aceptable; RTO = tiempo máximo hasta volver a dar servicio.
Valores propuestos para v1 (un nodo, docker compose); a validar con el product owner
([`open-questions/security-ops.md`](open-questions/security-ops.md)). Son coherentes con
[`architecture.md`](architecture.md) §10 (que delega aquí el RPO de PostgreSQL, "≤ 5 min") y con
su disparador de HA ("RTO < 15 min" exige clúster/Kubernetes; v1 asume RTO de 1 h). Los RTO de
esta tabla son de **restauración tras pérdida de datos**; ante una simple caída de proceso el
reinicio automático da RTO de minutos y RPO 0 (ver §10 de architecture).

| Componente | Contenido | Criticidad | RPO | RTO | Estrategia |
|------------|-----------|------------|-----|-----|------------|
| **PostgreSQL** | Usuarios, roles, inventario, credenciales cifradas, WireGuard, reglas, alertas, auditoría | Crítica | **≤ 5 min** (objetivo 1 min) | **≤ 1 h** | pgBackRest: full semanal + diferencial diario + archivo continuo de WAL |
| **ClickHouse — agregados** (horarios, diarios) | Históricos de 6 meses a 5 años | Alta | ≤ 24 h | ≤ 4 h (servicio analítico degradado mientras tanto) | clickhouse-backup incremental diario a MinIO |
| **ClickHouse — flujo crudo** | 14 días por defecto (rango 7–30, [`database.md`](database.md)) | Media | ≤ 24 h (aceptable perder el día; se recalcula lo posible) | ≤ 8 h, o "arrancar vacío" en < 1 h | Incluido en clickhouse-backup diario **opcional** según volumen; durante caídas de ClickHouse el stream de NATS actúa de buffer (autonomía en [`architecture.md`](architecture.md) §10.1) |
| **NATS JetStream** | Eventos en tránsito, estado de consumidores | Media (es *buffer*, no fuente de verdad) | Mensajes no confirmados en el último segundo (fsync) | ≤ 15 min | **No** se respalda; streams y consumidores declarados como código; los productores usan outbox e idempotencia |
| **Valkey/Redis** | Caché, revocación de sesiones (caché), rate limiting | Baja | N/A (se acepta pérdida total) | ≤ 15 min | Sin backup; fuente de verdad de sesiones en PostgreSQL ([ADR-0009](adr/0009-redis.md)) |
| **MinIO / NAS** | Reportes exportados, archivo de flujos, backups, anclas de auditoría | Alta | ≤ 24 h para la copia offsite | ≤ 24 h (archivo); backups ya deben existir offsite | Versionado + Object Lock (governance en backups, compliance en auditoría) + replicación/mirror offsite |
| **Configuración y código** | Compose, configs, dashboards, reglas, migraciones | Alta | 0 (Git) | ≤ 1 h (redeploy) | Git en GitHub + imágenes firmadas en GHCR |
| **Secretos** | KEK, claves de firma, contraseñas de despliegue | **Crítica** | 0 | ≤ 1 h | SOPS+age en repo de despliegue; KEK con copia offline doble ([`security.md`](security.md) §8.3) |
| **Prometheus / Loki / Tempo** | Telemetría de plataforma | Baja | Se acepta pérdida | ≤ 1 h (arranca vacío) | Sin backup; dashboards/reglas en Git |
| **WireGuard (estado en host)** | Interfaz y peers en kernel | Alta | 0 (se reconstruye desde PostgreSQL) | ≤ 15 min | `wireguard-agent` reconcilia la interfaz desde el estado deseado de `wireguard` (BD) al arrancar y cada 15 s |

RTO global de "plataforma usable" (login, inventario, WireGuard, SNMP) tras pérdida total del
servidor: **≤ 4 h** con hardware de reemplazo disponible (pregunta abierta sobre hardware).

## 3. Estrategia de backups

### 3.1 PostgreSQL — pgBackRest

Elegido frente a WAL-G/Barman: incluye cifrado, verificación de checksums de página, backups
paralelos, múltiples repositorios, restauración *point-in-time* (PITR) y `verify`.

- **Prerrequisito:** cluster inicializado con `--data-checksums` (Agente 2 / Sprint 1).
- `archive_mode=on`, `archive_command` vía pgBackRest (`archive-async=y`),
  `archive_timeout=60` → RPO ≈ 1 min incluso con poca escritura.
- Repositorios:
  - `repo1`: MinIO (S3) bucket `horus-backup-postgres` ([`storage.md`](storage.md) §4.2), cifrado
    `aes-256-cbc` de pgBackRest, versionado + Object Lock **governance** 35 días.
  - `repo2`: copia offsite (S3 en la nube o segundo sitio), credenciales separadas.
- Calendario: full domingo 02:00, diferencial diario 02:00, WAL continuo.
  Retención: 4 full (≈ 1 mes de PITR) en `repo1`; 12 semanales en `repo2`.
- Ejecución: contenedor `pgbackrest` (mismo host, comparte volumen de datos en solo lectura +
  socket), programado por un *scheduler* del compose (p. ej. `ofelia` o cron del host);
  publica `horus_backup_last_success_timestamp_seconds{component="postgres"}` (textfile de
  `node_exporter` o Pushgateway).
- Además: `pg_dump` lógico semanal del esquema de identidad y auditoría (portable entre
  versiones mayores, útil para migraciones de versión).

### 3.2 ClickHouse — clickhouse-backup (Altinity)

- Backup **incremental diario** de las tablas de agregados y dimensiones a MinIO
  (`horus-backup-clickhouse`, Object Lock governance 35 días), full semanal; retención 4 semanas en `repo1`, mensual offsite.
- Tablas de flujo crudo: decisión por volumen (ver [`storage.md`](storage.md)): si el volumen
  diario < 50 GB comprimido, se incluyen; si no, se acepta RPO de pérdida de crudo y la
  protección es el archivo del Sprint 13 (`ClickHouse → Archive → MinIO → NAS`) por partición
  cerrada (diaria).
- Esquema (DDL) siempre en Git como migraciones; el backup restaura datos.
- Alternativa evaluada: `BACKUP ... TO S3` nativo de ClickHouse (≥ 23.x). Se puede adoptar si
  simplifica; clickhouse-backup se prefiere hoy por retención/incrementales gestionados.
- ClickHouse **no** debe ser fuente de verdad de nada que no pueda reconstruirse o perderse
  parcialmente; las reglas/categorías viven en PostgreSQL.

### 3.3 NATS JetStream — ¿se respalda?

**No como sistema.** Razones: es un buffer de tránsito; respaldar streams en caliente da una
foto inconsistente con PostgreSQL/ClickHouse; restaurar eventos viejos provocaría reprocesos.

En su lugar:
- Definición declarativa de streams/consumidores en el repo (Agente 3, [`events.md`](events.md))
  aplicada al arrancar (idempotente).
- Almacenamiento `file` con `sync_interval` por defecto; `R1` en v1 y `R3` en HA (Sprint 14).
- Garantías de recuperación en las aplicaciones: **outbox transaccional** en productores con
  PostgreSQL; **idempotencia** en consumidores por `event_id`; colectores con buffer local
  acotado si NATS no está disponible.
- Si un stream concreto llegara a ser fuente de verdad (no previsto), `nats stream backup`
  diario para ese stream.

### 3.4 Valkey/Redis

Sin backup. `appendonly no`; RDB opcional solo para acelerar el *warm-up*. Al perderse: los
usuarios conservan sesión (se rehidrata desde PostgreSQL), los contadores de rate limit se
reinician, los locks expiran.

### 3.5 MinIO / NAS

- **Versionado** y Object Lock según [`storage.md`](storage.md) §4.2: **governance** 35 días en
  `horus-backup-postgres` y `horus-backup-clickhouse` (rol *break-glass* custodiado puede
  liberar), **compliance** en `horus-audit` (exportaciones y anclas de la cadena de auditoría).
- **Lifecycle** alineado con la retención (Sprint 13): archivo de flujo crudo y reportes
  caducan según política; versiones no actuales expiran a los 30 días.
- **Offsite:** replicación de bucket (*site replication*/*bucket replication*) o
  `mc mirror --watch` / `rclone sync` diario hacia almacenamiento en la nube o segundo sitio con
  credenciales de solo escritura (no borrado).
- **Riesgo a validar con el Agente 2:** MinIO no admite bien NFS/SMB como backend; debe correr
  sobre discos locales (en el propio NAS si soporta contenedores, o en el servidor con volúmenes
  iSCSI del NAS). Además, el estado de la edición comunitaria de MinIO (imágenes y consola
  restringidas desde 2025) puede obligar a evaluar alternativas S3 compatibles (Garage,
  SeaweedFS, Ceph RGW). Ver [`open-questions/security-ops.md`](open-questions/security-ops.md).

### 3.6 Secretos y configuración

- Repo de despliegue con archivos `*.enc.yaml` cifrados con **SOPS + age**; las llaves age
  privadas de los operadores no están en el repo (2+ operadores con acceso).
- **KEK** y claves de firma: copia offline doble (gestor de contraseñas corporativo + medio
  físico en caja fuerte). Procedimiento de recuperación probado en el simulacro trimestral.
- Al migrar a OpenBao: backup de su almacenamiento (Raft snapshot diario) + llaves de *unseal*
  repartidas (Shamir 3 de 5).

### 3.7 Calendario resumido

| Hora (UTC, ajustable) | Tarea |
|-----------------------|-------|
| Continuo | WAL de PostgreSQL → MinIO |
| 02:00 diario | pgBackRest diff (full domingo) |
| 03:00 diario | clickhouse-backup incremental (full domingo) |
| 04:00 diario | Mirror MinIO → offsite |
| 05:00 diario | Anclaje de cadena de auditoría + verificación |
| 06:00 domingo | Restauración de prueba automática de PostgreSQL |
| 1er lunes del mes | Restauración de prueba de una partición de ClickHouse |
| Trimestral | Simulacro completo de DR (game day) |

## 4. Verificación

| Verificación | Frecuencia | Cómo | Éxito |
|--------------|------------|------|-------|
| Checksums de backup | En cada backup | pgBackRest valida checksums de página y del archivo; `pgbackrest verify` semanal; clickhouse-backup verifica checksums de partes; MinIO verifica integridad (ETag/bitrot) | Sin errores |
| Restauración automática PostgreSQL | Semanal | Job levanta un contenedor PostgreSQL efímero, `pgbackrest restore` al último punto, arranca, ejecuta *smoke queries*: versión de migración esperada, conteo de usuarios/routers > 0, `max(audit_log.occurred_at)` reciente (< 24 h), verificación de cadena de auditoría de los últimos 7 días, descifrado de un secreto de prueba con la KEK | Todas pasan; publica métrica `horus_backup_restore_test_last_success_timestamp_seconds{component="postgres"}` |
| PITR | Mensual | Restaurar a "hace 3 h" y comprobar un registro conocido | OK |
| Restauración ClickHouse | Mensual | Restaurar la partición de ayer en una BD temporal y comparar conteos/sumas con producción | Diferencia 0 |
| Restauración desde offsite | Trimestral | Igual que PostgreSQL pero desde `repo2` | OK |
| Simulacro completo | Trimestral y antes de Release 1.0 | Servidor limpio → plataforma operativa con datos restaurados | Dentro de RTO; se mide y documenta |
| Alertas de antigüedad | Continuo | `BackupTooOld`, `RestoreTestTooOld` (> 8 días) | — |

## 5. Runbooks (esqueletos)

Formato común: **Síntomas · Impacto · Detección · Diagnóstico · Mitigación · Recuperación ·
Verificación · Post-mortem**. Se completan con comandos concretos al implementar cada sprint;
el comportamiento de degradación esperado viene de [`architecture.md`](architecture.md).
Cada alerta `page` enlaza a su sección (p. ej. `docs/disaster-recovery.md#rb-01--clickhouse-caído`).

### RB-01 — ClickHouse caído

- **Síntomas:** dashboards analíticos y reportes fallan/vacíos; `ClickHouseDown`; crece
  `jetstream_consumer_num_pending` del consumidor de ingesta.
- **Impacto:** sin analítica ni reportes nuevos. **Siguen funcionando** login, administración,
  inventario, WireGuard, SNMP en tiempo real y alertas no analíticas (vision Sprint 14).
- **Detección:** `ClickHouseDown`, `/readyz` de `analytics`/`reporting` en 503,
  `FlowIngestLagHigh`.
- **Diagnóstico:** `docker compose ps clickhouse`, logs del contenedor (OOM, disco lleno,
  "too many parts"), espacio en disco, `system.errors`, memoria del host.
- **Mitigación:** comprobar que NATS acumula sin llegar a `max_bytes` (si > 80 %, ampliar
  límite temporalmente o pausar ingestión de flujo crudo dejando agregados); comunicar
  degradación en la UI (banner).
- **Recuperación:** reiniciar; si hay corrupción de partes, `DETACH` de partes dañadas; si se
  perdió el volumen: recrear esquema (migraciones) + `clickhouse-backup restore` del último
  backup; los consumidores de NATS reanudan y vacían el backlog.
- **Verificación:** `num_pending` vuelve a ~0; lag de ingesta < 60 s; conteos del día coherentes.
- **Pérdida de datos esperada:** ninguna si el stream no se desbordó; si se desbordó, hueco de
  flujos en el periodo (documentar en la UI como "datos incompletos").

### RB-02 — SNMP detenido

- **Síntomas:** routers pasan a `unknown`/obsoletos; `SNMPFreshnessBurn`, `SNMPCollectorDown`.
- **Impacto:** sin métricas de routers ni alertas de disponibilidad basadas en SNMP; el resto
  funciona.
- **Diagnóstico:** ¿cae el servicio (crash loop, OOM) o falla el polling (timeouts masivos →
  problema de red de gestión/WireGuard)? Ver `horus_snmp_polls_total{result}` por `vendor`,
  estado de túneles (`horus_wireguard_peers`), logs del poller.
- **Mitigación:** si es red: escalar a red/NOC; si es un fabricante: desactivar temporalmente
  el adaptador problemático; si es carga: reducir concurrencia/intervalo.
- **Recuperación:** reinicio; al volver, el colector reanuda en el siguiente ciclo (no
  "rellena" el hueco: SNMP es muestreo; los contadores acumulativos permiten calcular el delta
  del periodo completo).
- **Verificación:** > 99 % routers frescos; hueco marcado en gráficos.

### RB-03 — NATS reiniciado o caído

- **Síntomas:** errores de publicación, `OutboxStuck`, consumidores desconectados, WebSocket
  sin eventos en tiempo real.
- **Impacto:** procesamiento asíncrono detenido; operaciones síncronas (login, CRUD) siguen;
  los eventos se acumulan en outboxes y buffers de colectores.
- **Diagnóstico:** logs de NATS (almacenamiento corrupto, disco lleno), `nats server check`,
  `nats stream report`.
- **Recuperación:** al reiniciar, JetStream recupera streams desde disco; el job de
  aprovisionamiento re-aplica la definición declarativa; los relays de outbox publican
  pendientes; consumidores durables continúan desde su último ack. Si el almacenamiento se
  perdió: recrear streams vacíos; los productores con outbox no pierden nada; los colectores
  pierden lo que no cupo en su buffer.
- **Verificación:** `horus_outbox_pending` → 0; `num_pending` bajando; prueba de evento
  extremo a extremo (crear router de prueba → evento → WebSocket).
- **Atención:** posibles duplicados → la idempotencia por `event_id` es obligatoria.

### RB-04 — Router desaparecido

- **Síntomas:** un router pasa a `offline`; sin flujos de ese exportador; túnel WG sin handshake.
- **Impacto:** solo ese router/sitio; posible caída real del servicio a clientes del ISP
  (incidente de red, no de plataforma).
- **Diagnóstico:** ¿falla solo SNMP (credenciales/ACL cambiadas) o también ICMP y WG (caída de
  enlace/energía)? ¿Cambió de IP (exportador desconocido nuevo en
  `horus.flows.exporter.unknown`)? ¿Se reemplazó el equipo (nuevo `engineID` SNMPv3, nueva clave WG)?
- **Mitigación/Recuperación:** responsabilidad del NOC; en la plataforma: actualizar
  IP/credenciales en el inventario, re-emitir config WG si se sustituyó el equipo, marcar
  mantenimiento para silenciar alertas.
- **Verificación:** estado `online`, flujos visibles, handshake reciente.

### RB-05 — NAS no responde

- **Síntomas:** `NASUnreachable`, errores de MinIO (si MinIO usa el NAS), backups y archivado
  fallando, exportaciones de reportes encoladas.
- **Impacto:** sin archivo histórico nuevo, sin descarga de reportes antiguos, **backups
  pausados** (RPO en riesgo). Operación en tiempo real intacta si los datos primarios están en
  disco local (principio 3).
- **Mitigación:** los jobs de archivado reintentan con backoff y no borran datos de ClickHouse
  hasta confirmar archivo. **Riesgo principal** (señalado en [`architecture.md`](architecture.md)
  §10.5): el WAL no archivado se acumula en `pg_wal` y puede llenar el disco y **detener
  PostgreSQL**. Defensas, en orden: (1) alertas `PostgresWALArchiveFailing` (> 15 min) y
  `PostgresWALTooLarge` (> 20 GB o > 50 % del volumen); (2) `archive-async=y` con
  `archive-push-queue-max` (p. ej. 30 % del volumen de datos) en pgBackRest: al superarse,
  pgBackRest descarta WAL y declara el archivado como correcto, **rompiendo la cadena PITR** pero
  salvando la disponibilidad — tras ello es obligatorio un backup completo; (3) volumen de
  `pg_wal` dimensionado para ≥ 24 h de WAL a la tasa normal; (4) si la caída supera 12 h,
  apuntar `repo2` (offsite) como destino de archivado temporal. Nunca borrar WAL a mano.
- **Recuperación:** restablecer NAS; jobs retoman; verificar que el WAL pendiente se archivó;
  lanzar backup completo de PostgreSQL inmediato.
- **Verificación:** `BackupTooOld` resuelto; mirror offsite al día.

### RB-06 — PostgreSQL caído

- **Síntomas:** login y casi toda la API fallan (503), `PostgresDown`, `/readyz` de `auth`,
  `devices`, `wireguard`, `alerts` en 503.
- **Impacto:** **alto**. Los colectores siguen recogiendo (SNMP con lista cacheada, flujos
  hacia NATS → ClickHouse); WireGuard en kernel sigue funcionando (no hay cambios posibles).
- **Diagnóstico:** logs (disco lleno, WAL lleno por archivado bloqueado, OOM, corrupción),
  `pg_isready`.
- **Recuperación:** (a) proceso caído → reiniciar; (b) disco lleno por WAL → resolver archivado
  (RB-05) o liberar espacio, nunca borrar WAL a mano; (c) corrupción/pérdida de volumen →
  `pgbackrest restore` (PITR al último WAL disponible), verificar, rotar si se sospecha
  compromiso; (d) tras restaurar: reconciliar `wireguard-agent` (la interfaz se ajusta al estado deseado restaurado; revisar peers creados después del punto de restauración) y
  revisar outboxes.
- **Verificación:** smoke queries de §4, login, CRUD de prueba, cadena de auditoría íntegra.

### RB-07 — Valkey/Redis caído

- **Síntomas:** latencia mayor en el gateway, `RedisDown`.
- **Impacto:** bajo: la revocación se consulta a `auth` por gRPC con caché de 30 s (una sesión
  revocada puede aceptarse hasta 30 s más); rate limiting cae a modo local en memoria por
  instancia (más permisivo); cachés de analytics frías.
- **Recuperación:** reiniciar; la caché se repuebla sola.
- **Verificación:** hit ratio recuperado, latencia normal.

### RB-08 — MinIO caído

- **Síntomas:** `MinIODown`; fallan exportaciones, archivado y backups.
- **Impacto:** como RB-05 sin afectar al NAS en sí.
- **Recuperación:** reiniciar; si hay pérdida de disco en MinIO: *heal* (si hay erasure coding)
  o restaurar buckets desde la copia offsite (los de backups primero).
- **Verificación:** escritura/lectura de objeto de prueba; backups reanudados.

### RB-09 — Pérdida total del servidor (escenario de desastre)

1. Provisionar host (script de `deployments/`), instalar Docker, clonar repo de despliegue.
2. Recuperar llave age del operador → descifrar secretos (SOPS); recuperar KEK.
3. Levantar datastores vacíos; `pgbackrest restore` desde `repo1` o `repo2`.
4. Levantar servicios (imágenes por digest, verificadas con `cosign verify`).
5. ClickHouse: migraciones + `clickhouse-backup restore` (agregados primero).
6. Verificar (§4), reconfigurar DNS/IP si cambió; los routers re-establecen WG solos si el
   endpoint no cambió.
7. Documentar tiempos reales vs RTO.

### RB-10 — KEK perdida o comprometida

- Perdida sin copia: las credenciales de routers y claves WG cifradas son irrecuperables →
  re-cargar credenciales (inventario exportable sin secretos) y rotar todas las claves WG.
- Comprometida: generar nueva KEK, re-envolver DEKs, **rotar credenciales en los routers** y
  claves WG (las antiguas pudieron descifrarse), auditar accesos.

## 6. Plan de pruebas de caos — Sprint 14

Objetivo (vision Sprint 14): comprobar la **degradación correcta** y la reanudación
`Collector → NATS → procesamiento` sin intervención manual.

### 6.1 Método

- Entorno: *staging* con docker compose idéntico a producción + generadores de carga:
  replay de flujos (pcap/archivo de NetFlow/IPFIX sintético con `nfreplay` o generador Go
  propio a 5–20k flujos/s), simulador SNMP (`snmpsim`) con 100–1.000 agentes, y k6 contra la
  API y WebSocket.
- Herramientas de inyección: `docker stop`/`kill`, `docker pause` (proceso colgado),
  **Toxiproxy** (latencia, cortes, ancho de banda entre servicios y datastores),
  **Pumba** o `tc netem` (pérdida de paquetes), `iptables/nftables` (partición), llenado de
  disco con `fallocate`.
- Cada experimento: **hipótesis**, métrica de estado estable, inyección, duración, criterio de
  aborto, resultado y acciones. Se ejecutan primero en horario laboral con todo el equipo
  (game day), luego algunos se automatizan en CI nocturno.

### 6.2 Experimentos

| # | Fallo | Duración | Hipótesis / criterio de éxito |
|---|-------|----------|-------------------------------|
| C1 | `docker stop postgres` | 10 min | Login falla con 503 limpio; colectores siguen; al volver, servicios *ready* en < 1 min sin reinicio manual; outboxes vacían; 0 pérdida de eventos |
| C2 | `docker kill -s KILL postgres` + restauración desde backup en volumen nuevo | — | RTO ≤ 1 h; RPO ≤ 5 min medido |
| C3 | `docker stop clickhouse` | 30 min | Login, administración, WireGuard, configuración, inventario funcionan; NATS acumula sin desbordar; al volver, lag < 60 s en ≤ 15 min; 0 flujos perdidos |
| C4 | `docker restart nats` y `docker kill nats` | — | Reconexión automática de todos los clientes < 30 s; sin duplicados visibles (idempotencia); colectores usan buffer |
| C5 | NATS caído 15 min con flujos a 10k/s | 15 min | El buffer del colector se llena y descarta con métrica `buffer_full`; no hay OOM |
| C6 | `docker stop redis` | 10 min | Usuarios siguen logueados; rate limit degradado; sin errores 5xx sostenidos |
| C7 | `docker stop minio` / desconectar NAS (nftables) | 1 h | Reportes muestran "exportación en cola"; backups reintentan; disco local no se llena; alerta en < 5 min |
| C8 | `docker stop snmp` | 10 min | Alerta `SNMPCollectorDown` < 3 min; al volver, polling normal en 1 ciclo |
| C9 | Desconectar un router (simulado) / 10 % de routers | 15 min | Solo esos routers `offline`; alertas de red correctas y sin tormenta (agrupación) |
| C10 | Latencia de 500 ms PostgreSQL (Toxiproxy) | 10 min | p95 API sube pero sin cascada; timeouts y *circuit breakers* actúan; pools no se agotan |
| C11 | Disco de ClickHouse al 95 % | — | Alerta `DiskWillFillIn24h`; ClickHouse rechaza inserts sin corromper; recuperación al liberar |
| C12 | `docker pause api-gateway` (colgado) | 2 min | Healthcheck lo reinicia; WebSocket reconectan con backoff y resincronizan |
| C13 | Pérdida del 5 % de paquetes UDP hacia el colector | 10 min | Métricas de descarte/huecos de secuencia visibles; sin errores en cascada |
| C14 | Reinicio completo del host | — | Todo vuelve solo en orden correcto (dependencias con `healthcheck`/`depends_on: condition`) en < 10 min |
| C15 | Simulacro DR (RB-09) | — | Restauración completa dentro de RTO global ≤ 4 h |

### 6.3 Entregables del Sprint 14

Informe por experimento, issues de los fallos encontrados, runbooks de §5 completados con
comandos reales y tiempos medidos, y actualización de RPO/RTO si los reales difieren.
