import type { RemoteDestination, RemoteDestinationTestResult } from '~~/types/api'

/**
 * Destinos remotos de las copias (platform.yaml `/platform/remote-destinations*`, ADR-0019):
 * reglas que la UI comprueba antes de enviar y cómo se presenta cada estado y cada prueba.
 */

export type DestinationStatus = RemoteDestination['status']
export type TestStage = RemoteDestinationTestResult['stage']

/** Valores por defecto del alta (iguales que el contrato). */
export const SFTP_DEFAULTS = { port: 22, retention_days: 30 } as const
export const RETENTION_RANGE = { min: 1, max: 3650 } as const
export const PORT_RANGE = { min: 1, max: 65535 } as const

/** Estado → color e icono (siempre con texto: nunca solo color, WCAG 1.4.1). */
export const DESTINATION_STATUS_VISUAL: Record<
  DestinationStatus,
  { color: 'success' | 'warning' | 'error' | 'neutral' | 'info'; icon: string }
> = {
  ok: { color: 'success', icon: 'i-lucide-circle-check' },
  lagging: { color: 'warning', icon: 'i-lucide-clock-alert' },
  failing: { color: 'error', icon: 'i-lucide-circle-x' },
  disabled: { color: 'neutral', icon: 'i-lucide-circle-pause' },
  pending: { color: 'info', icon: 'i-lucide-hourglass' },
}

/** `sftp://usuario@host:puerto/ruta` (IPv6 entre corchetes; la ruta siempre con `/`). */
export function destinationUrl(d: Pick<RemoteDestination, 'username' | 'host' | 'port' | 'path'>) {
  const host = d.host.includes(':') && !d.host.startsWith('[') ? `[${d.host}]` : d.host
  const path = d.path.startsWith('/') ? d.path : `/${d.path}`
  return `sftp://${d.username}@${host}:${d.port}${path}`
}

/** Clave i18n y parámetro del retraso de la copia ("12 min", "3 h", "2 días"). */
export function lagParts(seconds: number | null | undefined): { key: string; n: number } | null {
  if (seconds === null || seconds === undefined) return null
  const s = Math.max(0, Math.round(seconds))
  if (s < 60) return { key: 'platform.storage.lag.seconds', n: s }
  if (s < 3600) return { key: 'platform.storage.lag.minutes', n: Math.floor(s / 60) }
  if (s < 2 * 86_400) return { key: 'platform.storage.lag.hours', n: Math.floor(s / 3600) }
  return { key: 'platform.storage.lag.days', n: Math.floor(s / 86_400) }
}

/** ¿Puede activarse? Hacen falta credenciales y la huella del servidor confirmada. */
export function canEnable(d: Pick<RemoteDestination, 'has_credentials' | 'host_key_fingerprint'>) {
  return d.has_credentials && !!d.host_key_fingerprint
}

/** Formulario de datos del destino (alta y edición). */
export interface DestinationForm {
  name: string
  host: string
  port: number | null
  username: string
  path: string
  retention_days: number | null
}

export type DestinationField = keyof DestinationForm
/** Errores de validación por campo: clave i18n bajo `platform.storage.form.errors`. */
export type DestinationErrors = Partial<Record<DestinationField, string>>

const HOSTNAME =
  /^(?=.{1,253}$)([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?)(\.[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?)*\.?$/i
const IPV4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/
const USERNAME = /^[a-z0-9._][a-z0-9._@-]{0,127}$/i

/** Host del servidor SFTP: nombre o IP, sin esquema, usuario ni puerto. */
export function isValidHost(raw: string) {
  const host = raw.trim().replace(/^\[(.*)\]$/, '$1')
  if (!host || host.length > 253) return false
  const v4 = host.match(IPV4)
  if (v4) return v4.slice(1).every((n) => Number(n) <= 255)
  if (host.includes(':')) return /^[0-9a-f:.]+$/i.test(host) && host.split('::').length <= 2
  return HOSTNAME.test(host)
}

/** Valida el formulario; `{}` si es correcto. */
export function validateDestination(form: DestinationForm): DestinationErrors {
  const e: DestinationErrors = {}
  const name = form.name.trim()
  if (!name || name.length > 120) e.name = 'name'
  const host = form.host.trim()
  if (/^[a-z]+:\/\//i.test(host)) e.host = 'hostScheme'
  else if (host.includes('@')) e.host = 'hostUser'
  else if (!isValidHost(host)) e.host = 'host'
  const port = form.port
  if (port === null || !Number.isInteger(port) || port < PORT_RANGE.min || port > PORT_RANGE.max) {
    e.port = 'port'
  }
  const username = form.username.trim()
  if (!USERNAME.test(username)) e.username = 'username'
  const path = form.path.trim()
  if (!path || path.length > 1024 || /[\0\n\r]/.test(path)) e.path = 'path'
  else if (!path.startsWith('/')) e.path = 'pathAbsolute'
  const days = form.retention_days
  if (
    days === null ||
    !Number.isInteger(days) ||
    days < RETENTION_RANGE.min ||
    days > RETENTION_RANGE.max
  ) {
    e.retention_days = 'retention'
  }
  return e
}

/** Valores limpios del formulario (ya validado). */
export function destinationValues(form: DestinationForm) {
  return {
    name: form.name.trim(),
    host: form.host.trim().replace(/^\[(.*)\]$/, '$1'),
    port: form.port ?? SFTP_DEFAULTS.port,
    username: form.username.trim(),
    path: form.path.trim(),
    retention_days: form.retention_days ?? SFTP_DEFAULTS.retention_days,
  }
}

/** ¿Cambia el servidor (host o puerto)? Entonces hay que volver a probar y confirmar la huella. */
export function serverChanged(
  base: Pick<RemoteDestination, 'host' | 'port'>,
  next: { host: string; port: number | null },
) {
  const host = next.host.trim().replace(/^\[(.*)\]$/, '$1')
  return (
    (host !== '' && host.toLowerCase() !== base.host.toLowerCase()) ||
    (next.port !== null && next.port !== base.port)
  )
}

export type CredentialMode = 'password' | 'private_key'

export interface CredentialForm {
  mode: CredentialMode
  password: string
  privateKey: string
  passphrase: string
}

const PEM =
  /^-----BEGIN ([A-Z0-9 ]+ )?PRIVATE KEY-----[\s\S]+-----END ([A-Z0-9 ]+ )?PRIVATE KEY-----$/
const PUBLIC_KEY = /^(ssh-(ed25519|rsa|dss)|ecdsa-sha2-[a-z0-9]+) /

/** Error de las credenciales (clave i18n bajo `platform.storage.credentials.errors`) o `null`. */
export function validateCredentials(form: CredentialForm): string | null {
  if (form.mode === 'password') {
    if (!form.password) return 'passwordRequired'
    if (form.password.length > 1024) return 'tooLong'
    return null
  }
  const key = form.privateKey.trim()
  if (!key) return 'keyRequired'
  if (PUBLIC_KEY.test(key) || key.includes('PUBLIC KEY')) return 'keyIsPublic'
  if (key.includes('PuTTY-User-Key-File')) return 'keyPutty'
  if (!PEM.test(key)) return 'keyFormat'
  if (key.length > 16384 || form.passphrase.length > 1024) return 'tooLong'
  return null
}

/** Cuerpo de `PUT .../credentials`: solo lo del modo elegido (nunca los dos). */
export function credentialsBody(form: CredentialForm) {
  if (form.mode === 'password') return { password: form.password }
  const key = `${form.privateKey.trim()}\n`
  return form.passphrase ? { private_key: key, passphrase: form.passphrase } : { private_key: key }
}

/** Pasos de la prueba de conexión, en orden. */
export const TEST_STAGES: readonly TestStage[] = ['connect', 'host_key', 'auth', 'write', 'done']

export type StageState = 'done' | 'failed' | 'waiting' | 'skipped'

/**
 * Resultado global de una prueba:
 * - `ok`: conectó, la huella coincide, autenticó y pudo escribir.
 * - `confirm`: primera conexión (TOFU): hay que verificar y confirmar la huella del servidor.
 * - `mismatch`: la clave del servidor no es la confirmada (posible suplantación).
 * - `failed`: falló otro paso (conexión, autenticación o escritura).
 */
export type TestOutcome = 'ok' | 'confirm' | 'mismatch' | 'failed'

export function testOutcome(
  result: RemoteDestinationTestResult,
  pinned?: string | null,
): TestOutcome {
  if (result.ok) return 'ok'
  if (result.stage === 'host_key' && !result.host_key_confirmed && result.host_key_fingerprint) {
    const known = pinned ?? result.destination?.host_key_fingerprint ?? null
    return known && known !== result.host_key_fingerprint ? 'mismatch' : 'confirm'
  }
  return 'failed'
}

/** Estado de cada paso a partir del último alcanzado (`stage`). */
export function stageStates(result: RemoteDestinationTestResult): Record<TestStage, StageState> {
  const reached = TEST_STAGES.indexOf(result.stage)
  const outcome = testOutcome(result)
  const out = {} as Record<TestStage, StageState>
  TEST_STAGES.forEach((stage, i) => {
    if (result.ok || i < reached) out[stage] = 'done'
    else if (i === reached) out[stage] = outcome === 'confirm' ? 'waiting' : 'failed'
    else out[stage] = 'skipped'
  })
  return out
}
