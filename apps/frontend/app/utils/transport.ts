import type { Router } from '~~/types/api'

/**
 * Transportes con los que un router se conecta a Horus y su nivel de protección
 * (docs/tunnels.md, ADR-0031). La UI propone; el backend vuelve a aplicar las reglas
 * (`PUT /routers/{id}/transport`).
 */
export type ConnectionMode = Router['connection_mode']
export type ProtectionLevel = Router['protection_level']
export type TransportPath = 'private' | 'internet'

/** Transportes que admite cada fabricante, del más recomendado al menos. */
export function modesFor(vendor: string | null | undefined): ConnectionMode[] {
  return vendor === 'huawei' ? ['ipsec', 'gre', 'direct'] : ['wireguard', 'ipsec', 'gre']
}

/** El mejor transporte estándar cifrado que admite el fabricante. */
export function suggestedMode(vendor: string | null | undefined): ConnectionMode {
  return vendor === 'huawei' ? 'ipsec' : 'wireguard'
}

export function isEncrypted(mode: ConnectionMode): boolean {
  return mode === 'wireguard' || mode === 'ipsec'
}

export function hasTunnel(mode: ConnectionMode): boolean {
  return mode !== 'direct'
}

/** Nivel de protección de un transporte y el camino declarado (solo cuenta sin cifrado). */
export function protectionFor(mode: ConnectionMode, path: TransportPath | null): ProtectionLevel {
  if (isEncrypted(mode)) return 'encrypted'
  return path === 'private' ? 'unencrypted_private' : 'unencrypted_internet'
}

/** Icono + color semántico de cada nivel (nunca solo color: siempre con su texto). */
export function protectionVisual(level: ProtectionLevel): {
  icon: string
  color: 'success' | 'warning' | 'error'
} {
  switch (level) {
    case 'encrypted':
      return { icon: 'i-lucide-lock', color: 'success' }
    case 'unencrypted_private':
      return { icon: 'i-lucide-lock-open', color: 'warning' }
    default:
      return { icon: 'i-lucide-shield-alert', color: 'error' }
  }
}

const IPV4_RE = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/
const FQDN_RE =
  /^(?=.{1,253}$)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$/i
const KEYID_RE = /^keyid:[A-Za-z0-9._-]{1,64}$/
const USER_RE = /^[A-Za-z0-9._-]{1,64}@/

/** IPv4 literal (la de `utils/capacity.ts` admite además espacios alrededor). */
function ipv4(v: string): boolean {
  return IPV4_RE.test(v.trim())
}

/** Identidad IKE aceptable: IPv4, nombre DNS, usuario@dominio o keyid:<id>. */
export function isIkeId(raw: string): boolean {
  const v = raw.trim()
  if (!v) return false
  if (ipv4(v)) return true
  if (KEYID_RE.test(v)) return true
  if (v.includes('@')) return USER_RE.test(v) && FQDN_RE.test(v.split('@')[1] ?? '')
  return FQDN_RE.test(v)
}
