# Horus Flow — Backups, recuperación ante desastres y runbooks

> Estado: **propuesta ronda 2** (aplica [`po-decisions.md`](po-decisions.md) D2, D3, D6, D7) · Responsable: Agente C.
> Relacionados: [ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md) (almacenamiento local + destino remoto),
> [ADR-0025](adr/0025-binario-modular-con-roles.md) (binario modular), [`architecture.md`](architecture.md) §10 (modos
> de fallo y degradación — Agente A), [`storage.md`](storage.md) y [`database.md`](database.md) (Agente B),
> [`events.md`](events.md), [`observability.md`](observability.md), [`security.md`](security.md) §8.
> "Sprint N" = el incremento que entrega esa capacidad (D9).

Este documento cubre **cómo no perder datos y cómo volver**: RPO/RTO, backups, verificación y runbooks. *Cómo se
comporta el sistema mientras algo está caído* lo define [`architecture.md`](architecture.md) §10; aquí se enlaza.

**Contexto que cambia todo respecto al Sprint 0:** una instalación de **un solo servidor**, que aloja a **varios ISP**
(D6), operada por **agentes de IA + una persona** (D7), con el almacenamiento primario **local** y una copia remota
**opcional** por SFTP o nube de consumo (D2). No hay NAS obligatorio, ni MinIO, ni Object Lock, ni guardia 24/7.

## 1. Principios

1. **Copias locales siempre; copia remota cuando exista.** Las copias locales se hacen aunque no haya destino remoto,
   y **nunca** dependen de él. Sin destino remoto el sistema **avisa, no falla** (§3.0).
2. **Disco distinto.** El almacén local (`HORUS_DATA_DIR`, con `backups/`) va en un disco físico distinto del de
   PostgreSQL/ClickHouse/NATS ([ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md)). Si sólo hay un disco,
   la copia local protege frente a errores lógicos (borrado, corrupción), **no** frente a la pérdida del disco: la UI
   de plataforma lo indica.
3. **3-2-1 cuando sea posible**: datos vivos + copia local en otro disco + copia remota. Con destino remoto *pull*
   (el NAS recoge por SFTP) el servidor ni siquiera tiene credenciales para borrar la copia remota.
4. **Backup no verificado = no hay backup.** Restauración de prueba automática y alerta de antigüedad.
5. **Cifrado en cliente** de todo lo que sale del servidor; la clave de `rclone crypt`/pgBackRest se custodia
   **offline**: sin ella no hay recuperación desde el remoto (§3.6).
6. **Infraestructura como código**: compose, configuración, dashboards, reglas, streams NATS y esquemas están en Git;
   se recrean, no se respaldan.
7. **Runbooks ejecutables por un agente, aprobados por la persona** (D7): cada runbook es un script idempotente en
   `scripts/dr/` con `--dry-run`, que un agente de operación (`platform_operator`) puede preparar y ejecutar y cuyas
   acciones destructivas (restaurar sobre datos, rotar la clave del hub) requieren confirmación explícita de la
   persona. El texto de este documento es la especificación; los comandos los escribe quien implemente.
8. **Las copias son de plataforma**: contienen datos de todos los tenants; restaurar afecta a todos (no hay
   restauración por tenant; §3.8).

## 2. RPO / RTO realistas (un servidor, IA + 1 persona)

RPO = pérdida máxima de datos aceptable; RTO = tiempo hasta volver a dar servicio. La persona trabaja en **horario
laboral**; fuera de él sólo hay alertas `page` por Telegram/email y la recuperación automática de procesos. Los RTO se
cuentan **desde que la persona atiende** (en horario laboral, < 1 h desde la alerta; fuera de él, el siguiente día
laborable) y, cuando hay pérdida de hardware, **desde que hay hardware disponible**.

### 2.1 Por escenario

| Escenario | RPO | RTO | Notas |
|-----------|-----|-----|-------|
| Caída de un proceso/rol o reinicio del host | 0 (salvo telemetría en vuelo) | **≤ 10 min, automático** | `restart: unless-stopped`, healthchecks, orden por dependencias; los MikroTik reconectan solos el túnel |
| Corrupción lógica / borrado accidental en PostgreSQL | **≤ 5 min** (PITR) | **≤ 2 h** laborables | PITR desde el repositorio pgBackRest local |
| Pérdida del disco de bases de datos (almacén local en **otro** disco) | PG ≤ 5 min; ClickHouse agregados ≤ 24 h; flujos crudos: se pierden | **≤ 4 h** laborables con disco de repuesto | Restaurar PG y ClickHouse desde `backups/` local |
| Pérdida total del servidor **con** destino remoto | PG: lag de la copia remota (objetivo **≤ 1 h** por SFTP, **≤ 24 h** por nube de consumo); ClickHouse agregados ≤ 24 h | **1–2 días laborables** desde que hay servidor | Restauración desde el remoto (RB-09); el hub WG vuelve con su clave y DNS (RB-11) |
| Pérdida total del servidor **sin** destino remoto | **Todo** (salvo la configuración en Git y los secretos offline) | Reinstalación vacía: 1 día laborable | Riesgo aceptado explícitamente por el PO al no configurar destino; la UI lo advierte de forma permanente |
| Caída del destino remoto | 0 (nada se pierde) | — | Sólo crece el RPO remoto (RB-05) |
| Durante cualquier caída de ingesta | Flujos del periodo: **perdidos** (RouterOS no reintenta IPFIX); SNMP: hueco | — | Huecos marcados en la cobertura por tenant |

### 2.2 Por componente

| Componente | Contenido | RPO local | RPO remoto | Estrategia |
|------------|-----------|-----------|-----------|------------|
| **PostgreSQL** | Tenants, usuarios, inventario, clientes, credenciales cifradas, WireGuard, reglas, hallazgos, alertas, auditoría | ≤ 5 min (`archive_timeout=60`) | ≤ 1 h (SFTP) / ≤ 24 h (nube) | pgBackRest `repo1` **posix local**; copia remota con `repo2` SFTP nativo de pgBackRest o rclone del repositorio (§3.1) |
| **ClickHouse — agregados y dimensiones** | Por cliente 5 min/1 h/1 d (≤ 25 meses), por nodo, métricas SNMP, cobertura, scores | ≤ 24 h | ≤ 48 h | `clickhouse-backup` (o `BACKUP … TO Disk`) diario a `backups/clickhouse/` + rclone |
| **ClickHouse — flujos crudos** | 7 días por defecto | Se acepta perderlos | — | No se respaldan; su "copia" es el archivo Parquet de particiones vencidas si se activa |
| **Archivo** (`archive/<tenant_id>/`) | Parquet de particiones vencidas | — (ya es una copia) | ≤ 24 h | rclone |
| **Reportes** (`reports/<tenant_id>/`) | PDF/CSV/Excel | — | Opcional | Regenerables; se replican sólo si el destino lo indica |
| **Auditoría exportada y anclas** (`audit/`) | Particiones vencidas y hash diario | — | ≤ 24 h | rclone; es la prueba de integridad fuera del alcance de root |
| **NATS JetStream** | Eventos en tránsito | No se respalda | — | Streams como código; outbox (7 d) + idempotencia; Object Store de snapshots se regenera publicando de nuevo |
| **Valkey** | Caché, revocaciones, rate limit | No se respalda | — | Reconstruible |
| **Secretos** | KEK por módulo, clave de firma JWT, claves de copia (`crypt`, pgBackRest), llave age de SOPS, clave privada del hub WG (también en PG) | 0 | 0 | Paquete de secretos offline (§3.6) |
| **Configuración y código** | Compose, configuración, dashboards, migraciones | 0 (Git) | 0 | GitHub + imágenes firmadas |
| **Prometheus / Loki / Tempo** | Telemetría de plataforma | Se acepta pérdida | — | Sin backup |

Estos valores sustituyen a la propuesta del Sprint 0 (PG RTO 1 h, plataforma 4 h), que suponía guardia y hardware de
repuesto inmediato. Se validan con el PO ([`open-questions/security-ops.md`](open-questions/security-ops.md) Q12).

## 3. Estrategia de backups

### 3.0 Sin destino remoto configurado: avisar, no fallar

- **Cero destinos es una configuración válida** ([ADR-0019](adr/0019-almacenamiento-local-y-destino-remoto.md)). Todo
  funciona y las copias locales se hacen igual.
- Avisos (nunca errores): `GET /system/status` → `remote_storage: not_configured` (sólo visible con detalle para
  plataforma); banner permanente en la consola de plataforma "Sin copia fuera del servidor: si se pierde el servidor
  se pierden los datos"; alerta `RemoteCopyNotConfigured` de severidad `ticket` a los 7 días y cada 7 días
  ([`observability.md`](observability.md) §6.2); el instalador lo pregunta y deja constancia en la auditoría de
  plataforma si se omite.
- Ningún job falla ni reintenta por no tener destino; no se emiten `jobs.remote_sync.*`.
- Si un destino existe pero falla, el sistema **sigue**: reintentos con backoff, alertas `remote_copy_lagging` /
  `remote_destination_unhealthy`, y las copias pendientes esperan en el almacén local (RB-05).

### 3.1 PostgreSQL — pgBackRest

- Prerrequisito: clúster con `--data-checksums`.
- `archive_mode=on`, `archive_command` vía pgBackRest (`archive-async=y`), `archive_timeout=60` → RPO local ≈ 1 min.
- **`repo1` (obligatorio): tipo `posix`** en `HORUS_DATA_DIR/backups/postgres/` (otro disco), cifrado
  `repo-cipher-type=aes-256-cbc`. Full semanal + diferencial diario + WAL continuo; retención mínima garantizada
  **7 días de PITR y 2 full** aunque el disco se llene (al 95 % se pausa archivado y reportes antes que esto).
- **Copia remota**, una de dos (decide el Agente A/B en la implementación; ambas cumplen):
  - **`repo2` tipo `sftp`** (pgBackRest ≥ 2.46 lo soporta nativamente) cuando el destino es SFTP: el WAL llega al
    remoto casi en continuo (RPO remoto ≤ 15 min) y la retención remota es independiente (p. ej. 12 semanales).
  - **rclone** del repositorio local (`rclone copy`, nunca `sync` destructivo) hacia nube de consumo con `crypt`,
    cada hora para WAL y diario para backups (el repositorio ya va cifrado por pgBackRest; `crypt` añade la
    ofuscación de nombres).
- Ejecución: rol `jobs` (programador singleton) o contenedor `pgbackrest` con el volumen de datos en solo lectura.
  Métricas `horus_backup_last_success_timestamp_seconds{component="postgres"}`.
- Además: `pg_dump` lógico semanal de los esquemas de identidad y auditoría (portable entre versiones mayores).

### 3.2 ClickHouse — clickhouse-backup

- Backup **incremental diario** de agregados, dimensiones, métricas SNMP y cobertura a `backups/clickhouse/`
  (disco local; `clickhouse-backup` con `remote_storage: none` o `BACKUP … TO Disk`), full semanal; retención local
  2 full + incrementales. Remoto: rclone diario (o `remote_storage: sftp` nativo de clickhouse-backup).
- Flujos crudos: **no** se respaldan (7 días de valor; se aceptan perdidos); si el ISP necesita conservarlos, el
  archivo Parquet de particiones vencidas es su copia.
- Esquema (DDL) en Git como migraciones; el backup restaura datos.

### 3.3 NATS JetStream y Valkey

Sin backup (como en el Sprint 0): NATS es buffer de tránsito con streams declarados como código, outbox de 7 días e
idempotencia; Valkey es reconstruible. Los snapshots de catálogo y reputación en **NATS Object Store** se regeneran
re-publicando desde `traffic`/`detection` al arrancar si faltan.

### 3.4 Archivo, reportes y auditoría exportada

Ya son archivos inmutables en el almacén local; `jobs` los copia con `rclone copy` + `rclone check` al destino.
Una partición de ClickHouse no se borra hasta que su Parquet está en `archive/` **local** y verificado (nunca espera
al remoto).

### 3.5 Copia remota (rclone)

- Ejecutada por el rol `jobs` como proceso hijo; configuración generada desde PostgreSQL y pasada **en memoria**
  (variables `RCLONE_CONFIG_*`), nunca escrita en disco ([`security.md`](security.md) §8.5).
- Destinos por orden: **SFTP** (NAS/servidor propio; primero), Google Drive, Dropbox, MEGA. MediaFire no se soporta.
- **`crypt` obligatorio** en nube de consumo; recomendado también en SFTP.
- Semántica: `copy` de archivos inmutables (no `sync`), verificación `rclone check` diaria, retención remota propia
  (borrado remoto sólo por la política remota, nunca reflejando borrados locales).
- Modo **pull** recomendado si hay NAS: el NAS ejecuta rclone/SFTP contra el servidor con una llave de **sólo
  lectura** sobre `backups/`, `archive/`, `audit/`; el servidor no puede borrar esas copias (defensa contra
  ransomware). En ese modo Horus mide el lag por un archivo testigo que el NAS deja en un directorio de acuse.
- Métricas `horus_remote_sync_*` y eventos `horus.jobs.remote_sync.*` ([`events.md`](events.md) §8.13).

### 3.6 Secretos: paquete offline

Sin estos secretos **no hay recuperación** aunque existan las copias:

| Secreto | Para qué |
|---------|----------|
| Clave de `rclone crypt` (+ salt) | Leer cualquier copia de la nube |
| Passphrase de cifrado de pgBackRest | Leer el repositorio de PostgreSQL (local y remoto) |
| KEK de cada módulo (`devices`, `wireguard`, `auth`, `alerts`, `jobs`) | Descifrar credenciales de routers, clave del hub, TOTP, canales, destinos |
| Clave de firma JWT (opcional: se puede regenerar invalidando sesiones) | Evitar cerrar todas las sesiones |
| Llave privada age de SOPS | Descifrar el repo de despliegue |

- Se exportan con `horusctl secrets export` (cifrado con age a la llave pública de la persona) a **dos** sitios
  offline: gestor de contraseñas + medio físico (USB cifrado) fuera del servidor. Se regenera el paquete al rotar
  cualquiera de ellos y la consola de plataforma muestra "paquete de secretos desactualizado" hasta confirmarlo.
- La clave privada del **hub WireGuard** está dentro de PostgreSQL (cifrada con la KEK de `wireguard`), así que viaja
  en las copias; el paquete offline incluye además una copia directa para reconstruir el hub sin restaurar PG (RB-11).
- Prueba trimestral: descifrar una copia remota con el paquete offline en una máquina limpia.

### 3.7 Calendario resumido (UTC, ajustable)

| Hora | Tarea |
|------|-------|
| Continuo | WAL de PostgreSQL → `repo1` local (y `repo2` SFTP si existe) |
| 02:00 diario | pgBackRest diff (full domingo) |
| 03:00 diario | clickhouse-backup incremental (full domingo) |
| 04:00 diario | rclone copy al destino remoto (WAL cada hora si es nube) |
| 05:00 diario | Anclaje de cadena de auditoría + verificación |
| 06:00 domingo | Restauración de prueba automática de PostgreSQL desde `repo1` |
| 1er lunes del mes | Restauración de prueba **completa** (PG + partición de ClickHouse) **desde el destino remoto** si existe |
| Trimestral | Simulacro de DR con el paquete de secretos offline |

### 3.8 Multi-tenant (D6)

- Las copias son **de plataforma**: un único PostgreSQL y un único ClickHouse para todos los ISP. **No** hay
  restauración por tenant: un PITR afecta a todos (comunicarlo a todos los ISP).
- Para recuperar datos de **un** tenant (borrado accidental de un ISP): restaurar en una instancia temporal y
  exportar/reimportar sólo sus filas (`tenant_id`), con un script de `scripts/dr/` revisado por la persona.
- Baja de un tenant: sus datos desaparecen de las copias cuando caducan (retención local 35 días; la remota según su
  política); el certificado de baja lo indica ([`security.md`](security.md) §13.5).

## 4. Verificación

| Verificación | Frecuencia | Cómo | Éxito |
|--------------|------------|------|-------|
| Checksums | En cada backup | pgBackRest valida páginas y archivo; `pgbackrest verify` semanal; clickhouse-backup verifica partes; `rclone check` diario en remoto | Sin errores |
| Restauración automática de PostgreSQL | Semanal (local) | Contenedor PostgreSQL efímero, `pgbackrest restore` al último punto, *smoke queries*: versión de migración, conteos de tenants/routers/clientes > 0, `max(audit_log.occurred_at)` < 24 h, cadena de auditoría de 7 días, descifrado de un secreto de prueba, **RLS activo** (una consulta sin `horus.tenant_id` devuelve 0 filas) | `horus_backup_restore_test_last_success_timestamp_seconds{component="postgres"}` |
| Restauración completa desde el remoto | **Mensual** | Igual que la anterior + una partición de ClickHouse, descargando del destino remoto y descifrando con la clave `crypt` | OK; mide el tiempo real (dato para el RTO) |
| Paquete de secretos | Trimestral | Descifrar una copia remota en máquina limpia sólo con el paquete offline | OK |
| Alertas de antigüedad | Continuo | `BackupTooOld`, `RestoreTestTooOld`, `RemoteCopyLagging`, `RemoteCopyNotConfigured` | — |

## 5. Runbooks (esqueletos)

Formato: **Síntomas · Impacto · Detección · Diagnóstico · Mitigación · Recuperación · Verificación · Datos
perdidos**. Cada uno será un script de `scripts/dr/` (principio 7). Comportamiento de degradación en
[`architecture.md`](architecture.md) §10.

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
  `horus.flows.exporter.unregistered`)? ¿Se reemplazó el equipo (nuevo `engineID` SNMPv3, nueva clave WG)?
- **Mitigación/Recuperación:** responsabilidad del NOC; en la plataforma: actualizar
  IP/credenciales en el inventario, re-emitir config WG si se sustituyó el equipo, marcar
  mantenimiento para silenciar alertas.
- **Verificación:** estado `online`, flujos visibles, handshake reciente.
### RB-05 — Destino remoto no disponible

- **Síntomas:** `RemoteCopyLagging`, `RemoteDestinationUnhealthy`; banner "copia remota retrasada desde hh:mm".
- **Impacto:** ninguno en el producto ([`architecture.md`](architecture.md) §10.5). Sólo crece el RPO **remoto**: si
  ahora se perdiera el servidor, se perdería lo no copiado.
- **Diagnóstico:** `horus_remote_sync_failures_total{reason}`: `auth` (token OAuth caducado o revocado, llave SFTP
  quitada), `network` (NAS apagado, DNS), `quota` (cuota del proveedor llena), `verify` (corrupción o archivo
  modificado en el destino), `host_key` (la huella SFTP cambió: **no** aceptar sin verificar, posible suplantación).
- **Mitigación:** comprobar que el almacén local no se llena por acumulación (RB-08); si el destino seguirá caído
  días, configurar temporalmente un segundo destino (`POST /platform/remote-destinations`).
- **Recuperación:** automática al volver (`rclone copy` reanuda; `rclone check`). Tras un retraso > 72 h, forzar
  `POST /platform/remote-destinations/{id}/sync` y verificar. Credenciales caducadas → `PUT .../credentials`.
- **Verificación:** `horus_remote_sync_lag_seconds` < 24 h; `rclone check` sin diferencias.
- **Datos perdidos:** ninguno.

### RB-06 — PostgreSQL caído

- **Síntomas:** login y casi toda la API fallan (503), `PostgresDown`, `/readyz?role=auth|devices|…` en 503.
- **Impacto:** alto para todos los ISP. Siguen: colectores e ingesta (flujos a ClickHouse, `first_seen` se acumula en
  JetStream), túneles WG (`wg-agent` *fail-static*), lecturas analíticas con token vigente.
- **Diagnóstico:** logs (disco lleno, OOM, corrupción), `pg_isready`.
- **Recuperación:** (a) proceso → reinicio; (b) disco lleno → liberar (nunca borrar WAL a mano); (c) corrupción o
  pérdida de volumen → `pgbackrest restore` desde `repo1` local (PITR al último WAL), o desde `repo2`/remoto si se
  perdió también el almacén; (d) tras restaurar: `wg-agent` reconcilia con el estado deseado restaurado (revisar
  peers enrolados después del punto de restauración: sus routers deberán re-enrolarse), los relays del outbox
  republican (idempotencia), `devices` vuelve a crear clientes con los `first_seen` pendientes.
- **Verificación:** smoke queries de §4 (incluido RLS), login, CRUD de prueba, cadena de auditoría.
- **Datos perdidos:** ninguno en caída de proceso; ≤ 5 min en pérdida de disco.

### RB-07 — Valkey caído

- **Impacto:** bajo: revocación vía `auth` con caché de 30 s; rate limit en memoria; cachés de widgets frías (más
  carga en ClickHouse si hay muchas pantallas NOC); no se emiten tickets WebSocket (la UI pasa a *polling*).
- **Recuperación:** reinicio; las cachés se repueblan solas.

### RB-08 — Almacén local lleno

- **Síntomas:** `LocalStoreFilling` (70/85/95 %), reportes en cola, archivado pausado.
- **Impacto:** al 85 % se pausan reportes nuevos; al 95 % se pausa el archivado (ClickHouse crece porque no borra
  particiones sin Parquet). Las bases de datos **no** se detienen (otro disco) ([`architecture.md`](architecture.md) §10.8).
- **Diagnóstico:** ocupación por ruta (`backups/`, `archive/`, `reports/`, `audit/`); ¿destino remoto caído con
  retención local basada en "ya copiado"? ¿retención local mal dimensionada?
- **Mitigación:** reducir retención local de reportes y archivo **por encima** del mínimo garantizado (7 días de PITR,
  2 full de ClickHouse), ampliar disco. Nunca borrar a mano el repositorio de pgBackRest (usar `expire`).
- **Verificación:** < 70 %; archivado y reportes reanudados.

### RB-09 — Pérdida total del servidor

1. Conseguir servidor (o VM) con discos para datos y almacén separados; instalar el sistema base con el script de
   `deployments/`.
2. Recuperar el **paquete de secretos offline** (§3.6) → descifrar el repo de despliegue (SOPS), KEK, claves de copia.
3. Recuperar las copias: desde el destino remoto (`rclone copy` inverso con `crypt`) a `backups/` y `audit/`; si el
   disco del almacén sobrevivió, montarlo.
4. Levantar datastores vacíos; `pgbackrest restore`; ClickHouse: migraciones + restore (agregados primero).
5. Levantar `horus-app`, `horus-collector`, `horus-wg-agent` (imágenes por digest, `cosign verify`).
6. **Hub WireGuard**: si la IP pública cambió, actualizar el **registro DNS** del endpoint del hub (TTL bajo); la
   clave del hub viene restaurada en PG (RB-11). Los MikroTik reconectan solos.
7. Verificar (§4), revisar cobertura por tenant, comunicar a los ISP el hueco de datos.
8. Documentar tiempos reales vs RTO y actualizar §2.

### RB-10 — KEK perdida o comprometida

- Perdida sin copia: credenciales de routers, clave del hub y destinos son irrecuperables → **rotar la clave del hub
  obliga a re-enrolar todos los routers de todos los ISP** (RB-11 b): por eso la KEK de `wireguard` es el secreto más
  protegido del paquete offline.
- Comprometida: nueva KEK, re-envolver DEKs, rotar credenciales en los routers (lista exportable por tenant y nodo),
  rotar la clave del hub, auditar accesos, notificar a los ISP afectados.

### RB-11 — Pérdida o compromiso del hub WireGuard

Todos los routers de todos los ISP llegan por el hub: es el punto único de fallo de la recolección
([`architecture.md`](architecture.md) §10.9).

- **Síntomas:** `WireGuardHubDown`, `RouterMassOffline` en varios tenants a la vez; `horus.wireguard.hub.status_changed`.
- **(a) Caída del contenedor o reinicio del host:** recuperación automática; `wg-agent` reaplica el estado y los
  routers (iniciadores, keepalive 25 s) reconectan. Verificar handshakes recientes.
- **(b) Pérdida del host o de su IP pública:** reconstruir el hub en otro host con **la misma clave privada** (de la
  copia de PG o del paquete offline) y apuntar el **nombre DNS** del endpoint a la nueva IP (TTL bajo; los scripts de
  los routers usan el nombre, nunca la IP). No hace falta tocar ningún router.
- **(c) Clave privada del hub comprometida:** generar clave nueva y **re-enrolar todos los routers** (script de
  rotación por tenant generado por Horus, que el técnico de cada ISP aplica); mientras tanto, revocar la clave antigua
  sólo cuando los routers críticos hayan migrado (ventana coordinada con cada ISP). Auditar y notificar.
- **Datos perdidos:** flujos y métricas de todo el periodo sin túnel (huecos de cobertura por tenant).
- **Verificación:** > 95 % de peers con handshake < 3 min; flujos entrando de todos los tenants.

### RB-12 — Clave de `rclone crypt` o de pgBackRest perdida

- Las copias remotas existentes son **ilegibles**. Mientras el servidor viva no hay pérdida: generar clave nueva,
  hacer **full** nuevo de PostgreSQL y ClickHouse, re-subir archivo y auditoría, y retirar las copias antiguas del
  destino tras verificar. Actualizar el paquete offline.

### RB-13 — Fallo de aislamiento entre tenants detectado

- **Síntomas:** `TenantIsolationViolation` o reporte de un ISP.
- **Acción inmediata:** desactivar la ruta/topic/consumidor implicado (flag de configuración), preservar logs y
  auditoría; seguir el playbook (d) de [`security.md`](security.md) §14.
- **Recuperación:** corregir, añadir el caso a la batería de aislamiento, desplegar; informar a los ISP afectados.

## 6. Pruebas de caos

Objetivo: comprobar la **degradación correcta** y la reanudación sin intervención manual. Con D7 los experimentos son
**scripts automatizados** (CI nocturno contra el compose de staging), no *game days* con un equipo.

### 6.1 Método

Entorno de staging con el mismo compose que producción y generadores: replay de IPFIX sintético (2 tenants), `snmpsim`, k6 contra API y WebSocket. Inyección con `docker stop/kill/pause`, Toxiproxy, `tc netem`, nftables y `fallocate`. Cada experimento declara hipótesis, métrica de estado estable, criterio de aborto y resultado.

### 6.2 Experimentos

| # | Fallo | Duración | Hipótesis / criterio de éxito |
|---|-------|----------|-------------------------------|
| C1 | `docker stop postgres` | 10 min | Login falla con 503 limpio; colectores e ingesta siguen; al volver, roles *ready* en < 1 min sin intervención; outboxes vacían; 0 pérdida de eventos; clientes nuevos creados desde los `first_seen` acumulados |
| C2 | Pérdida del volumen de PostgreSQL + restauración desde `repo1` local | — | RTO ≤ 2 h medido; RPO ≤ 5 min; RLS activo tras restaurar |
| C3 | `docker stop clickhouse` | 30 min | Login, administración, WireGuard, inventario funcionan; widgets de tráfico muestran "datos no disponibles"; NATS acumula sin desbordar; al volver, lag < 60 s en ≤ 15 min |
| C4 | `docker restart nats` y `docker kill nats` | — | Reconexión automática < 30 s; sin duplicados visibles (idempotencia); el collector usa su buffer |
| C5 | NATS caído 15 min con flujos a 10k/s | 15 min | El buffer del collector se llena y descarta con métrica; sin OOM; `login` no se ve afectado (contenedor separado) |
| C6 | `docker stop valkey` | 10 min | Usuarios siguen trabajando; rate limit degradado; kioscos pasan a *polling*; sin 5xx sostenidos |
| C7 | Destino remoto inaccesible (nftables al SFTP) | 24 h | Nada del producto falla; `RemoteCopyLagging` a las 36 h; al volver, la copia se pone al día sola y `rclone check` pasa |
| C8 | Sin destino remoto configurado | — | Banner y `RemoteCopyNotConfigured` (ticket); ningún job en error |
| C9 | Almacén local al 96 % (`fallocate`) | — | Reportes y archivado pausados; PostgreSQL/ClickHouse siguen; alertas 85/95 % |
| C10 | `docker stop horus-wg-agent` y pérdida simulada del hub (nueva IP + cambio de DNS) | 15 min | Una sola alerta de hub (sin tormenta por router); routers simulados reconectan tras el cambio de DNS sin tocarlos |
| C11 | Un tenant con todos sus exportadores parados | 15 min | `TenantFlowsSilent` sólo para ese tenant; otros tenants intactos; ninguna alerta `page` |
| C12 | Un tenant inyecta 3× su cuota de flujos/s | 15 min | Descarte por cuota sólo en ese tenant; lag de los demás < 60 s |
| C13 | Mensaje NATS sin `Horus-Tenant` / con tenant incoherente | — | DLQ, `TenantIsolationViolation`, ningún efecto en datos |
| C14 | Reinicio completo del host | — | Todo vuelve solo en < 10 min; túneles reconectan |
| C15 | Simulacro DR (RB-09) desde el destino remoto con el paquete offline | — | Restauración dentro del RTO de §2 (medido) |

### 6.3 Entregables

Informe automático por experimento (artefacto de CI), issues de los fallos encontrados, runbooks de §5 convertidos en
scripts con tiempos medidos y actualización de RPO/RTO si los reales difieren.
