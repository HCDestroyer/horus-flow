# `mod:jobs` — services/jobs

- **Propósito:** Tareas singleton: archivado, copia remota de los backups (SFTP + age), cobertura de flujos, mantenimiento.
- **Rol(es) de `horus`:** `jobs`
- **Agente dueño:** PLAT (decisión D21 en docs/po-decisions.md) ([`docs/backlog/team.md`](../../docs/backlog/team.md) §2, [`.github/CODEOWNERS`](../../.github/CODEOWNERS))
- **Ficha del módulo:** [`docs/services.md`](../../docs/services.md) §3 · Roles y fronteras: [ADR-0025](../../docs/adr/0025-binario-modular-con-roles.md)
- **Layout y reglas:** [`docs/conventions.md`](../../docs/conventions.md) §2.1 — `api/` (contrato público), `internal/` (`config`, `domain`, `app`, `adapters`), `migrations/` (si tiene esquema PostgreSQL). Otros módulos solo importan `services/jobs/api`.

## Aviso de versión nueva (I1-22)

Cada `HORUS_UPDATE_CHECK_INTERVAL` (6 h; la primera, a los 0–2 min de arrancar) consulta la fuente
de versiones y lo sirve en `GET /api/v1/platform/updates` (permiso `platform.updates.read`, solo el
superadministrador; contrato `UpdateStatus` en `packages/schemas/openapi/v0/platform.yaml`). Sin
salida a Internet falla en silencio: `status: unchecked` con el motivo. La UI de la consola de
plataforma (aviso "Hay una versión nueva X.Y.Z" con notas y comando) es de UI.

| Variable | Defecto | Qué |
| --- | --- | --- |
| `HORUS_VERSION` | `0.0.0-dev` | versión instalada (la escribe el instalador) |
| `HORUS_UPDATE_CHANNEL` | `stable` | `stable` (sin prerelease) o `beta` (incluye `-beta.N`/`-rc.N`) |
| `HORUS_UPDATE_SOURCE` | API de releases de GitHub del repo | o la URL de un `latest.json` (espejo propio) |
| `HORUS_UPDATE_CHECK` | `true` | `false` = no consulta (`--no-update-check`) |
| `HORUS_UPDATE_CHECK_INTERVAL` | `6h` | mínimo 1 min |
| `HORUS_REPO` | `hcdestroyer/horus-flow` | para las URLs de notas por defecto |

Métricas: `horus_update_available{channel}` (1 = hay versión nueva) y `horus_update_check_success`
(0 = no comprobado). Cada versión nueva deja un aviso en el log (`event=horus.platform.update_available`).
Pendiente: enviarlo también por los canales de alerta de plataforma (D13) cuando existan.

## Copia remota de los backups (ADR-0019, nota 2026-10-12)

Destinos SFTP de plataforma en `/api/v1/platform/remote-destinations` (permiso
`platform.storage.manage`; altas, ediciones, credenciales y pruebas con reautenticación; todo
auditado en la cadena de plataforma como `platform.storage.destination_*`, sin secretos). Esquema
PostgreSQL `jobs` (`installation`, `remote_destination`, `remote_copy`).

1. `POST` crea el destino (deshabilitado). 2. `PUT .../credentials`: contraseña o clave privada
OpenSSH (+ passphrase), cifradas con la KEK de jobs; nunca se devuelven ni se registran.
3. `POST .../test`: conecta (10 s), devuelve la huella `SHA256:…` del servidor; con
`confirm_host_key` igual a la presentada la fija (TOFU), autentica y escribe y borra un archivo de
prueba. Si la clave cambia: `ok=false`, `stage=host_key`, "La clave del servidor cambió".
4. `PATCH {"enabled": true}` (exige credenciales, huella y una prueba correcta). Cambiar host o
puerto borra la huella y deshabilita; cambiar host, puerto, usuario o ruta reinicia el ledger.
5. `POST .../sync` (Idempotency-Key) encola una copia inmediata (`409 REMOTE_DESTINATION_NOT_READY`
o `REMOTE_DESTINATION_SYNC_RUNNING`).

El motor recorre `backups/postgres`, `backups/clickhouse` y `reports/` del almacén cada 10 min
y, de lo que el destino tiene seleccionado (`paths`: `backups` y/o `reports`, las dos por defecto),
sube cada archivo nuevo o cambiado cifrado con age a `<ruta>/horus-<id de instalación>/<relativa>.age`
(temporal `.partial` + renombrado), en serie y un destino cada vez, y apunta en el ledger su
tamaño, mtime y SHA-256 en claro. La retención remota borra lo subido hace más de
`retention_days` que ya no existe en local (quitar una ruta de `paths` no borra nada del destino).
Se omiten los temporales (`*.tmp`, `.tmp-*` del render de reportes). Estado: `disabled`, `failing` (último intento
fallido), `pending` (sin copia aún), `lagging` (última copia correcta > 26 h), `ok`. Un fallo
nunca afecta al resto de Horus.

| Variable | Defecto | Qué |
| --- | --- | --- |
| `HORUS_JOBS_KEK_FILE` | — (obligatoria fuera de dev) | KEK de 32 B (hex/base64) de las credenciales de los destinos (`jobs_kek`) |
| `HORUS_BACKUP_AGE_RECIPIENT_FILE` | — | clave pública age de las copias (`backup_age_recipient`); sin ella no se copia |
| `HORUS_DATA_DIR` | `/var/lib/horus/store` | almacén local (lectura de `backups/`; horus-app con `group_add` 70 y 101) |
| `HORUS_REMOTE_SYNC_INTERVAL` | `10m` | periodo entre pasadas (mínimo 1 min) |
| `HORUS_REMOTE_SYNC_SETTLE` | `2m` | antigüedad mínima de un archivo para copiarlo |
| `HORUS_REMOTE_SYNC_LAG_THRESHOLD` | `26h` | a partir de cuándo el destino está `lagging` |
| `HORUS_JOBS_MIGRATE` | `true` | aplica las migraciones del esquema `jobs` al arrancar |

Métricas: `horus_remote_sync_lag_seconds{destination}`,
`horus_remote_sync_failures_total{destination,reason}` (`connect`, `host_key`, `auth`, `write`,
`local`, `config`) y `horus_remote_sync_bytes_total{destination}`. Restaurar una copia remota:
`horus-ctl remote-decrypt` o `age -d -i backup_age_identity` (docs/disaster-recovery.md RB-09).
