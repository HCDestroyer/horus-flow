import type { CredentialStatus, ProblemDetails, Router } from '~~/types/api'

/**
 * Reglas de nodos, routers y credenciales que la UI comprueba antes de enviar (el backend
 * las vuelve a aplicar; devices.yaml, D15): RouterOS 7.x ≥ 7.12, huella TLS SHA-256,
 * longitudes mínimas de los secretos y merge-patch solo con lo que cambió.
 */

/** `routeros_version` del contrato: solo RouterOS 7.x (`7.16` o `7.16.2`). */
export const ROUTEROS_VERSION_RE = /^7\.[0-9]+(\.[0-9]+)?$/
/** Primera versión de RouterOS compatible (D15). */
export const ROUTEROS_MIN_MINOR = 12

export const SNMP_SECRET_MIN = 8
export const ROUTEROS_API_PASSWORD_MIN = 16

export type RouterOsCheck = 'empty' | 'invalid' | 'unsupported' | 'ok'

/** Valida la versión declarada de RouterOS: vacía, fuera de 7.x, < 7.12 o compatible. */
export function checkRouterOsVersion(raw: string | null | undefined): RouterOsCheck {
  const v = (raw ?? '').trim()
  if (!v) return 'empty'
  if (!ROUTEROS_VERSION_RE.test(v)) return 'invalid'
  const minor = Number(v.split('.')[1])
  return minor >= ROUTEROS_MIN_MINOR ? 'ok' : 'unsupported'
}

/**
 * Huella TLS SHA-256: 64 dígitos hex con o sin `:` (o prefijo `SHA256:`) → `AA:BB:…`;
 * `null` si no es válida.
 */
export function normalizeFingerprint(raw: string): string | null {
  const hex = raw
    .trim()
    .replace(/^sha256:/i, '')
    .replaceAll(':', '')
    .replace(/\s+/g, '')
  if (!/^[0-9a-f]{64}$/i.test(hex)) return null
  return hex.toUpperCase().match(/../g)!.join(':')
}

/** Texto de un formulario → valor del contrato (`''` → `null`). */
export function nullable(value: string | null | undefined): string | null {
  const v = (value ?? '').trim()
  return v ? v : null
}

/** Coordenada opcional: `''` → `null`; no numérica o fuera de rango → `undefined` (inválida). */
export function parseCoordinate(raw: string | number | null | undefined, limit: number) {
  if (raw === null || raw === undefined || String(raw).trim() === '') return null
  const n = Number(String(raw).replace(',', '.'))
  if (!Number.isFinite(n) || Math.abs(n) > limit) return undefined
  return n
}

/**
 * Merge-patch (RFC 7396) con solo los campos que cambiaron respecto al original; arrays por
 * valor. `{}` si no cambió nada.
 */
export function mergePatch<T extends object>(original: Partial<T>, next: Partial<T>): Partial<T> {
  const out: Partial<T> = {}
  for (const key of Object.keys(next) as (keyof T)[]) {
    const a = original[key] ?? null
    const b = next[key] ?? null
    if (JSON.stringify(a) !== JSON.stringify(b)) out[key] = next[key]
  }
  return out
}

/** Errores de campo de un Problem (`errors[]`) → `{ campo: mensaje }` (el primero de cada uno). */
export function fieldErrors(problem: ProblemDetails | undefined | null): Record<string, string> {
  const out: Record<string, string> = {}
  for (const e of problem?.errors ?? []) {
    const field = e.field ?? '_'
    out[field] ??= e.message ?? e.code ?? ''
  }
  return out
}

/** Estado de una credencial: el devuelto al guardarla en esta sesión o el que trae el router. */
export function credentialStatus(
  router: Pick<Router, 'credentials'> | null | undefined,
  kind: CredentialStatus['kind'],
  saved?: CredentialStatus | null,
): CredentialStatus {
  if (saved && saved.kind === kind) return saved
  return router?.credentials?.find((c) => c.kind === kind) ?? { kind, configured: false }
}

export interface ReadOnlySnippetOptions {
  /** Usuario de la API de RouterOS (solo lectura). */
  apiUser: string
  /** Usuario SNMPv3. */
  snmpUser: string
  /** IP de Horus en el túnel (origen permitido). */
  horusAddress?: string
}

/**
 * Comandos RouterOS 7 para crear a mano el grupo y el usuario de solo lectura de la API y el
 * usuario SNMPv3 authPriv que Horus usa para leer. Las contraseñas quedan como marcadores:
 * nunca se escriben aquí.
 */
export function routerOsReadOnlySnippet(o: ReadOnlySnippetOptions) {
  const from = o.horusAddress ?? '10.255.0.1'
  return [
    '# Grupo de solo lectura: lectura, API y REST; sin escritura, política, FTP ni reinicio.',
    '/user group add name=horus-ro policy=read,api,rest-api,!write,!policy,!ftp,!reboot,!password,!sensitive,!sniff,!test,!romon comment="horus"',
    `/user add name=${o.apiUser} group=horus-ro address=${from}/32 password="CAMBIA-ESTA-CLAVE-16+" comment="horus"`,
    '# API-SSL solo desde el túnel de Horus.',
    `/ip service set api-ssl address=${from}/32 disabled=no`,
    '# SNMPv3 authPriv de solo lectura (SHA-1 + AES-128, lo que acepta todo RouterOS 7).',
    `/snmp community add name=${o.snmpUser} security=private authentication-protocol=SHA1 authentication-password="CLAVE-AUTH-8+" encryption-protocol=AES encryption-password="CLAVE-PRIV-8+" read-access=yes write-access=no addresses=${from}/32 comment="horus"`,
    '/snmp set enabled=yes',
    '',
  ].join('\n')
}
