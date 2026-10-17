import type { RemoteDestination, RemoteDestinationTestResult } from '~~/types/api'
import { forbidden, ifMatch, json, noContent, notFound, problem } from './http'
import { hash, iso, mockUuid, rng } from './random'
import { mockState } from './state'

/**
 * Destinos remotos SFTP simulados (platform.yaml `/platform/remote-destinations*`) con la misma
 * máquina de estados que el rol `jobs`:
 *
 * - Alta (`POST`) deshabilitada, sin credenciales ni huella → `PUT .../credentials` →
 *   `POST .../test`: la primera vez para en `host_key` con la huella del servidor sin confirmar
 *   (TOFU); con `confirm_host_key` igual a esa huella la fija y sigue (auth → write → done).
 * - `PATCH` con `If-Match` (428/412); cambiar host o puerto borra la huella y deshabilita.
 *   `enabled=true` exige credenciales y huella (`REMOTE_DESTINATION_NOT_READY`).
 * - `POST .../sync` (`Idempotency-Key`): `pending` → copia en curso → `ok`; una segunda copia
 *   mientras hay otra en curso da `409 REMOTE_DESTINATION_SYNC_RUNNING`.
 *
 * Todo se guarda en el storage de la sesión simulada (`sessionStorage['horus.mock.remoteDestinations']`).
 *
 * Interruptores (`localStorage`):
 * - `horus.mock.sftpSeed = '1'`: si no hay destinos, se crean cuatro de ejemplo (al día, con
 *   retraso, fallando y desactivado).
 * - `horus.mock.sftpHostKeyChanged = '1'`: los servidores presentan otra clave (la huella ya
 *   confirmada no coincide: "La clave del servidor cambió").
 * - `horus.mock.sftpTestFail = 'connect' | 'auth' | 'write'`: la prueba falla en ese paso.
 * - `horus.mock.sftpConflict = '1'`: el primer `PATCH`/`DELETE` de cada destino responde `412`
 *   (otra persona cambió la retención a 14 días).
 * - `horus.mock.sftpSyncMs`: duración de cada copia simulada (3000 ms por defecto).
 */

export interface StoredDestination {
  id: string
  name: string
  host: string
  port: number
  username: string
  path: string
  retention_days: number
  /** Rutas que replica (por defecto backups y reports, como la API). */
  paths?: RemoteDestination['paths']
  enabled: boolean
  /** Tipo de credencial guardada (el secreto nunca se guarda ni se devuelve). */
  credentials: 'password' | 'private_key' | null
  host_key_fingerprint: string | null
  version: number
  created_at: string
  last_success_at: string | null
  last_attempt_at: string | null
  last_error: string | null
  /** Copia en curso: instante de inicio (ms) y su `Idempotency-Key`. */
  sync_started_at: number | null
  sync_key: string | null
  /** Pendiente de subir (sin copia aún o retraso simulado). */
  files_pending: number
  bytes_pending: number
  conflictServed?: boolean
}

const STORAGE_KEY = 'horus.mock.remoteDestinations'
/** Una copia con más de una hora de retraso se considera atrasada (RPO remoto ≤ 1 h). */
const LAG_THRESHOLD_S = 3600
const INITIAL_FILES = 46
const INITIAL_BYTES = 3_850_000_000

type Storage = Pick<globalThis.Storage, 'getItem' | 'setItem'> | null | undefined
let storage: Storage

/** Recupera los destinos de esta sesión simulada (al recargar). */
export function restoreRemoteDestinations(s: Storage) {
  storage = s
  try {
    const saved = JSON.parse(s?.getItem(STORAGE_KEY) ?? 'null') as StoredDestination[] | null
    if (Array.isArray(saved) && mockState.remoteDestinations === null) {
      mockState.remoteDestinations = saved
    }
  } catch {
    // estado guardado ilegible: se ignora
  }
}

function save() {
  try {
    storage?.setItem(STORAGE_KEY, JSON.stringify(mockState.remoteDestinations ?? []))
  } catch {
    // sin storage: solo en memoria
  }
}

function readLocal(key: string) {
  try {
    return globalThis.localStorage?.getItem(key) ?? null
  } catch {
    return null
  }
}

function syncMs() {
  const v = Number(readLocal('horus.mock.sftpSyncMs'))
  return Number.isFinite(v) && v >= 0 && readLocal('horus.mock.sftpSyncMs') !== null ? v : 3000
}

/**
 * Huella SHA-256 (formato OpenSSH, base64 sin relleno) estable por servidor. `original`: la de
 * antes de rotar la clave (`horus.mock.sftpHostKeyChanged`), con la que nacen los de ejemplo.
 */
export function serverFingerprint(host: string, port: number, original = false) {
  const changed = !original && readLocal('horus.mock.sftpHostKeyChanged') === '1'
  const rotated = changed ? ':rotated' : ''
  const next = rng(hash(`${host.toLowerCase()}:${port}${rotated}`))
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'
  return `SHA256:${Array.from({ length: 43 }, () => alphabet[Math.floor(next() * 64)]).join('')}`
}

function seed(now: Date): StoredDestination[] {
  const t = now.getTime()
  const base = (
    n: number,
    name: string,
    host: string,
    extra: Partial<StoredDestination>,
  ): StoredDestination => ({
    id: mockUuid('0192eaaa', n),
    name,
    host,
    port: 22,
    username: 'horus',
    path: '/srv/copias/horus',
    retention_days: 30,
    enabled: true,
    credentials: 'private_key',
    host_key_fingerprint: serverFingerprint(host, 22, true),
    version: 3,
    created_at: iso(t - 40 * 86_400_000),
    last_success_at: iso(t - 9 * 60_000),
    last_attempt_at: iso(t - 9 * 60_000),
    last_error: null,
    sync_started_at: null,
    sync_key: null,
    files_pending: 0,
    bytes_pending: 0,
    ...extra,
  })
  return [
    base(1, 'NAS de la oficina', 'nas.fibranorte.example', {}),
    base(2, 'Servidor del datacenter', '198.51.100.20', {
      port: 2222,
      host_key_fingerprint: serverFingerprint('198.51.100.20', 2222, true),
      last_success_at: iso(t - 3 * 3600_000),
      last_attempt_at: iso(t - 5 * 60_000),
      files_pending: 12,
      bytes_pending: 840_000_000,
    }),
    base(3, 'Respaldo en casa', 'backup.example.net', {
      username: 'copias',
      credentials: 'password',
      last_success_at: iso(t - 2 * 86_400_000),
      last_attempt_at: iso(t - 15 * 60_000),
      last_error: 'Tiempo de espera agotado al conectar con backup.example.net:22',
      files_pending: 120,
      bytes_pending: 9_600_000_000,
    }),
    base(4, 'Prueba antigua', 'sftp.example.org', {
      enabled: false,
      retention_days: 7,
      last_success_at: iso(t - 20 * 86_400_000),
      last_attempt_at: iso(t - 20 * 86_400_000),
    }),
  ]
}

function list(now: Date): StoredDestination[] {
  if (mockState.remoteDestinations === null) {
    mockState.remoteDestinations = readLocal('horus.mock.sftpSeed') === '1' ? seed(now) : []
    save()
  }
  return mockState.remoteDestinations
}

/** Termina la copia en curso si ya pasó su duración. */
function settle(d: StoredDestination, now: Date) {
  if (d.sync_started_at === null) return
  const end = d.sync_started_at + syncMs()
  if (now.getTime() < end) return
  d.sync_started_at = null
  d.sync_key = null
  d.last_attempt_at = iso(end)
  d.last_success_at = iso(end)
  d.last_error = null
  d.files_pending = 0
  d.bytes_pending = 0
  save()
}

function present(d: StoredDestination, now: Date): RemoteDestination {
  settle(d, now)
  const lag = d.last_success_at
    ? Math.max(0, Math.round((now.getTime() - new Date(d.last_success_at).getTime()) / 1000))
    : null
  const status: RemoteDestination['status'] = !d.enabled
    ? 'disabled'
    : d.last_error
      ? 'failing'
      : !d.last_success_at
        ? 'pending'
        : lag !== null && lag > LAG_THRESHOLD_S
          ? 'lagging'
          : 'ok'
  return {
    id: d.id,
    kind: 'sftp',
    name: d.name,
    status,
    host: d.host,
    port: d.port,
    username: d.username,
    path: d.path,
    enabled: d.enabled,
    has_credentials: d.credentials !== null,
    host_key_fingerprint: d.host_key_fingerprint,
    retention_days: d.retention_days,
    paths: d.paths ?? ['backups', 'reports'],
    last_success_at: d.last_success_at,
    last_attempt_at: d.last_attempt_at,
    last_error: d.last_error,
    lag_seconds: d.enabled ? lag : null,
    files_pending: d.files_pending,
    bytes_pending: d.bytes_pending,
    version: d.version,
  }
}

export function remoteDestinations(now: Date): RemoteDestination[] {
  return list(now).map((d) => present(d, now))
}

export interface RemoteDestinationRouteContext {
  req: Request
  path: string
  method: string
  body: Record<string, unknown>
  can: (permission: string) => boolean
  now: Date
  reauthRequired?: boolean
}

type FieldError = { field: string; code: string; message: string }

function invalid(errors: FieldError[], code = 'VALIDATION_FAILED') {
  return problem(422, code, 'Revisa los campos marcados', {}, { errors })
}

const HOSTNAME =
  /^(?=.{1,253}$)([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?)(\.[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?)*$/i

function hostOk(value: string) {
  const v4 = value.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/)
  if (v4) return v4.slice(1).every((n) => Number(n) <= 255)
  if (value.includes(':')) return /^[0-9a-f:.]+$/i.test(value)
  return HOSTNAME.test(value)
}

const FIELDS = {
  name: (v: unknown) =>
    typeof v === 'string' && v.trim().length >= 1 && v.trim().length <= 120
      ? null
      : 'Entre 1 y 120 caracteres',
  host: (v: unknown) =>
    typeof v === 'string' && hostOk(v.trim())
      ? null
      : 'Escribe un nombre de host o una IP, sin sftp:// ni puerto',
  port: (v: unknown) =>
    typeof v === 'number' && Number.isInteger(v) && v >= 1 && v <= 65535 ? null : 'Entre 1 y 65535',
  username: (v: unknown) =>
    typeof v === 'string' && v.trim().length >= 1 && v.trim().length <= 128
      ? null
      : 'Entre 1 y 128 caracteres',
  path: (v: unknown) =>
    typeof v === 'string' && v.trim().length >= 1 && v.trim().length <= 1024
      ? null
      : 'Escribe la ruta del directorio remoto',
  retention_days: (v: unknown) =>
    typeof v === 'number' && Number.isInteger(v) && v >= 1 && v <= 3650
      ? null
      : 'Entre 1 y 3650 días',
} as const

type Field = keyof typeof FIELDS

function check(body: Record<string, unknown>, fields: Field[]): FieldError[] {
  return fields.flatMap((f) => {
    const message = f in body ? FIELDS[f](body[f]) : null
    return message ? [{ field: f, code: 'INVALID', message }] : []
  })
}

const trimmed = (v: unknown) => (typeof v === 'string' ? v.trim() : v)

function create(ctx: RemoteDestinationRouteContext) {
  const body = ctx.body
  const allowed = ['kind', 'name', 'host', 'port', 'username', 'path', 'retention_days']
  const unknown = Object.keys(body).find((k) => !allowed.includes(k))
  if (unknown) {
    return invalid([{ field: unknown, code: 'INVALID', message: 'Campo no admitido' }])
  }
  const errors: FieldError[] = []
  if (body.kind !== 'sftp') {
    errors.push({ field: 'kind', code: 'INVALID', message: 'Por ahora solo SFTP' })
  }
  for (const f of ['name', 'host', 'username', 'path'] as const) {
    if (!(f in body)) errors.push({ field: f, code: 'REQUIRED', message: 'Obligatorio' })
  }
  errors.push(
    ...check(body, ['name', 'host', 'port', 'username', 'path', 'retention_days'] as Field[]),
  )
  if (errors.length) return invalid(errors)
  const all = list(ctx.now)
  const name = String(trimmed(body.name))
  if (all.some((d) => d.name.toLowerCase() === name.toLowerCase())) {
    return problem(
      409,
      'CONFLICT',
      'Ya hay un destino con ese nombre',
      {},
      {
        errors: [{ field: 'name', code: 'DUPLICATE', message: 'Ya hay un destino con ese nombre' }],
      },
    )
  }
  const d: StoredDestination = {
    id: mockUuid('0192eaab', hash(`${name}:${ctx.now.getTime()}`)),
    name,
    host: String(trimmed(body.host)),
    port: (body.port as number | undefined) ?? 22,
    username: String(trimmed(body.username)),
    path: String(trimmed(body.path)),
    retention_days: (body.retention_days as number | undefined) ?? 30,
    paths: (body.paths as RemoteDestination['paths'] | undefined) ?? ['backups', 'reports'],
    enabled: false,
    credentials: null,
    host_key_fingerprint: null,
    version: 1,
    created_at: ctx.now.toISOString(),
    last_success_at: null,
    last_attempt_at: null,
    last_error: null,
    sync_started_at: null,
    sync_key: null,
    files_pending: INITIAL_FILES,
    bytes_pending: INITIAL_BYTES,
  }
  all.push(d)
  save()
  return json(present(d, ctx.now), 201, {
    ETag: `"${d.version}"`,
    Location: `/api/v1/platform/remote-destinations/${d.id}`,
  })
}

/** `If-Match` (428/412); con `horus.mock.sftpConflict` el primero de cada destino da 412. */
function precondition(ctx: RemoteDestinationRouteContext, d: StoredDestination) {
  const version = ifMatch(ctx.req)
  if (version === null) return problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')
  if (readLocal('horus.mock.sftpConflict') === '1' && !d.conflictServed) {
    // Otra persona cambió la retención justo antes.
    d.conflictServed = true
    d.version += 1
    d.retention_days = 14
    save()
  }
  if (version !== d.version) {
    return problem(
      412,
      'PRECONDITION_FAILED',
      'El destino cambió',
      {},
      { current: present(d, ctx.now) as unknown as Record<string, unknown> },
    )
  }
  return null
}

function update(ctx: RemoteDestinationRouteContext, d: StoredDestination) {
  const failed = precondition(ctx, d)
  if (failed) return failed
  const body = ctx.body
  const allowed = ['name', 'host', 'port', 'username', 'path', 'retention_days', 'enabled']
  const keys = Object.keys(body)
  const unknown = keys.find((k) => !allowed.includes(k))
  if (!keys.length || unknown) {
    return invalid([{ field: unknown ?? '_', code: 'INVALID', message: 'Campo no editable' }])
  }
  const errors = check(body, ['name', 'host', 'port', 'username', 'path', 'retention_days'])
  if ('enabled' in body && typeof body.enabled !== 'boolean') {
    errors.push({ field: 'enabled', code: 'INVALID', message: 'Debe ser verdadero o falso' })
  }
  if (errors.length) return invalid(errors)
  if ('name' in body) {
    const name = String(trimmed(body.name))
    const all = list(ctx.now)
    if (all.some((o) => o.id !== d.id && o.name.toLowerCase() === name.toLowerCase())) {
      return problem(
        409,
        'CONFLICT',
        'Ya hay un destino con ese nombre',
        {},
        {
          errors: [
            { field: 'name', code: 'DUPLICATE', message: 'Ya hay un destino con ese nombre' },
          ],
        },
      )
    }
  }
  const next = { ...d }
  for (const f of ['name', 'host', 'username', 'path'] as const) {
    if (f in body) next[f] = String(trimmed(body[f]))
  }
  if ('port' in body) next.port = body.port as number
  if ('retention_days' in body) next.retention_days = body.retention_days as number
  if ('paths' in body) next.paths = body.paths as RemoteDestination['paths']
  // Otro servidor: la huella confirmada ya no vale y se deshabilita hasta volver a probarlo.
  const serverMoved = next.host.toLowerCase() !== d.host.toLowerCase() || next.port !== d.port
  if (serverMoved) {
    next.host_key_fingerprint = null
    next.enabled = false
    // El servidor nuevo aún no tiene ninguna copia.
    next.last_success_at = null
    next.last_error = null
    next.sync_started_at = null
    next.sync_key = null
    next.files_pending = INITIAL_FILES
    next.bytes_pending = INITIAL_BYTES
  }
  if ('enabled' in body && !serverMoved) {
    if (body.enabled === true && (!next.credentials || !next.host_key_fingerprint)) {
      return problem(
        422,
        'REMOTE_DESTINATION_NOT_READY',
        'Faltan las credenciales o la huella confirmada del servidor',
        {},
        {
          errors: [
            {
              field: 'enabled',
              code: 'REMOTE_DESTINATION_NOT_READY',
              message: 'Pon las credenciales y confirma la huella antes de activarlo',
            },
          ],
        },
      )
    }
    next.enabled = body.enabled as boolean
  }
  Object.assign(d, next, { version: d.version + 1 })
  save()
  return json(present(d, ctx.now), 200, { ETag: `"${d.version}"` })
}

function remove(ctx: RemoteDestinationRouteContext, d: StoredDestination) {
  const failed = precondition(ctx, d)
  if (failed) return failed
  const all = list(ctx.now)
  all.splice(all.indexOf(d), 1)
  save()
  return noContent()
}

const PEM =
  /-----BEGIN ([A-Z0-9 ]+ )?PRIVATE KEY-----[\s\S]+-----END ([A-Z0-9 ]+ )?PRIVATE KEY-----/

function putCredentials(ctx: RemoteDestinationRouteContext, d: StoredDestination) {
  const body = ctx.body
  const unknown = Object.keys(body).find(
    (k) => !['password', 'private_key', 'passphrase'].includes(k),
  )
  if (unknown) return invalid([{ field: unknown, code: 'INVALID', message: 'Campo no admitido' }])
  const password = body.password
  const key = body.private_key
  if ((password === undefined) === (key === undefined)) {
    return invalid([
      {
        field: '_',
        code: 'INVALID',
        message: 'Envía una contraseña o una clave privada (no ambas)',
      },
    ])
  }
  if (password !== undefined) {
    if (typeof password !== 'string' || !password || password.length > 1024) {
      return invalid([{ field: 'password', code: 'INVALID', message: 'Entre 1 y 1024 caracteres' }])
    }
    if (body.passphrase) {
      return invalid([
        {
          field: 'passphrase',
          code: 'INVALID',
          message: 'La frase de paso es de la clave privada',
        },
      ])
    }
  } else if (typeof key !== 'string' || !PEM.test(key) || key.length > 16384) {
    return invalid([
      {
        field: 'private_key',
        code: 'INVALID',
        message: 'Pega la clave privada OpenSSH completa (-----BEGIN … PRIVATE KEY-----)',
      },
    ])
  }
  d.credentials = password !== undefined ? 'password' : 'private_key'
  d.version += 1
  save()
  return json(present(d, ctx.now), 200, { ETag: `"${d.version}"` })
}

function testResult(
  d: StoredDestination,
  now: Date,
  result: Omit<RemoteDestinationTestResult, 'destination'>,
) {
  d.last_attempt_at = now.toISOString()
  save()
  return json({ ...result, destination: present(d, now) })
}

function test(ctx: RemoteDestinationRouteContext, d: StoredDestination) {
  const confirm = typeof ctx.body.confirm_host_key === 'string' ? ctx.body.confirm_host_key : null
  const fail = readLocal('horus.mock.sftpTestFail')
  if (fail === 'connect' || d.host.endsWith('.invalid')) {
    return testResult(d, ctx.now, {
      ok: false,
      stage: 'connect',
      host_key_fingerprint: null,
      host_key_confirmed: false,
      error: `No se pudo conectar con ${d.host}:${d.port}: tiempo de espera agotado`,
    })
  }
  const fp = serverFingerprint(d.host, d.port)
  if (d.host_key_fingerprint !== fp) {
    if (confirm !== fp) {
      const changed = d.host_key_fingerprint !== null
      return testResult(d, ctx.now, {
        ok: false,
        stage: 'host_key',
        host_key_fingerprint: fp,
        host_key_confirmed: false,
        error: changed
          ? 'La clave del servidor no coincide con la confirmada'
          : confirm
            ? 'La huella enviada no es la del servidor'
            : null,
      })
    }
    d.host_key_fingerprint = fp
    d.version += 1
  }
  if (!d.credentials) {
    return testResult(d, ctx.now, {
      ok: false,
      stage: 'auth',
      host_key_fingerprint: fp,
      host_key_confirmed: true,
      error: 'Faltan las credenciales del destino',
    })
  }
  if (fail === 'auth') {
    return testResult(d, ctx.now, {
      ok: false,
      stage: 'auth',
      host_key_fingerprint: fp,
      host_key_confirmed: true,
      error: `El servidor rechazó al usuario ${d.username} (Permission denied)`,
    })
  }
  if (fail === 'write') {
    return testResult(d, ctx.now, {
      ok: false,
      stage: 'write',
      host_key_fingerprint: fp,
      host_key_confirmed: true,
      error: `Sin permiso de escritura en ${d.path}`,
    })
  }
  return testResult(d, ctx.now, {
    ok: true,
    stage: 'done',
    host_key_fingerprint: fp,
    host_key_confirmed: true,
    error: null,
  })
}

function sync(ctx: RemoteDestinationRouteContext, d: StoredDestination) {
  const key = ctx.req.headers.get('Idempotency-Key')
  if (!key) return problem(428, 'PRECONDITION_REQUIRED', 'Falta Idempotency-Key')
  settle(d, ctx.now)
  if (d.sync_started_at !== null) {
    if (d.sync_key === key) return json(present(d, ctx.now), 202)
    return problem(409, 'REMOTE_DESTINATION_SYNC_RUNNING', 'Ya hay una copia en curso')
  }
  if (!d.enabled || !d.credentials || !d.host_key_fingerprint) {
    return problem(
      409,
      'REMOTE_DESTINATION_NOT_READY',
      'El destino no está activo o le faltan credenciales o la huella confirmada',
    )
  }
  d.sync_started_at = ctx.now.getTime()
  d.sync_key = key
  d.last_attempt_at = ctx.now.toISOString()
  if (!d.files_pending) {
    d.files_pending = 3
    d.bytes_pending = 120_000_000
  }
  save()
  return json(present(d, ctx.now), 202)
}

/** Rutas `/platform/remote-destinations*` (lectura incluida). */
export function remoteDestinationRoute(ctx: RemoteDestinationRouteContext): Response | undefined {
  if (ctx.path === '/platform/remote-destinations') {
    if (ctx.method !== 'GET' && ctx.method !== 'POST') return
    if (!ctx.can('platform.storage.manage')) return forbidden()
    if (ctx.method === 'GET') return json({ data: remoteDestinations(ctx.now) })
    if (ctx.reauthRequired) return reauth()
    return create(ctx)
  }
  const m = ctx.path.match(
    /^\/platform\/remote-destinations\/([^/]+)(?:\/(credentials|test|sync))?$/,
  )
  if (!m) return
  const action = m[2] ?? null
  const route =
    action === null && ctx.method === 'PATCH'
      ? 'patch'
      : action === null && ctx.method === 'DELETE'
        ? 'delete'
        : action === 'credentials' && ctx.method === 'PUT'
          ? 'credentials'
          : action === 'test' && ctx.method === 'POST'
            ? 'test'
            : action === 'sync' && ctx.method === 'POST'
              ? 'sync'
              : null
  if (!route) return
  if (!ctx.can('platform.storage.manage')) return forbidden()
  const d = list(ctx.now).find((x) => x.id === m[1])
  if (!d) return notFound('REMOTE_DESTINATION_NOT_FOUND')
  if (route === 'sync') return sync(ctx, d)
  if (ctx.reauthRequired) return reauth()
  if (route === 'patch') return update(ctx, d)
  if (route === 'delete') return remove(ctx, d)
  if (route === 'credentials') return putCredentials(ctx, d)
  return test(ctx, d)
}

const reauth = () =>
  problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para administrar los destinos remotos')

/** Componente `remote_storage` de `GET /system/status` según los destinos. */
export function remoteStorageComponent(now: Date): {
  name: string
  status: 'up' | 'degraded' | 'down' | 'not_configured'
  detail?: string | null
} {
  const all = remoteDestinations(now)
  if (!all.length) return { name: 'remote_storage', status: 'not_configured' }
  const enabled = all.filter((d) => d.enabled)
  const ok = enabled.filter((d) => d.status === 'ok').length
  const status = !enabled.length
    ? 'degraded'
    : ok === enabled.length
      ? 'up'
      : enabled.every((d) => d.status === 'failing')
        ? 'down'
        : 'degraded'
  return { name: 'remote_storage', status, detail: `${ok} de ${all.length} destinos al día` }
}
