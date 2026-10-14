import type { InfraCheck, InfraCheckType, InfraService, InfraServiceState } from '~~/types/api'

/**
 * Servicios de infraestructura del ISP (IPTV, HTTP, DNS, RADIUS…): estados, orden, resumen y
 * el constructor de chequeos del formulario. Los servidores son del ISP (no clientes); el
 * estado del servicio es el peor de sus chequeos conocidos (lo calcula el rol snmp).
 */

export const INFRA_STATES = [
  'down',
  'degraded',
  'unknown',
  'up',
] as const satisfies InfraServiceState[]

export const INFRA_CHECK_TYPES = [
  'icmp',
  'tcp',
  'http',
  'no_traffic',
  'ddos_inbound',
] as const satisfies InfraCheckType[]

export const INFRA_KINDS = ['iptv', 'http', 'dns', 'radius', 'other'] as const

export const INFRA_STATE_META: Record<
  InfraServiceState,
  { color: 'success' | 'error' | 'warning' | 'neutral'; icon: string }
> = {
  up: { color: 'success', icon: 'i-lucide-circle-check' },
  down: { color: 'error', icon: 'i-lucide-circle-x' },
  degraded: { color: 'warning', icon: 'i-lucide-triangle-alert' },
  unknown: { color: 'neutral', icon: 'i-lucide-circle-help' },
}

const ORDER: Record<InfraServiceState, number> = { down: 0, degraded: 1, unknown: 2, up: 3 }

/** Problemas primero (caído, degradado, desconocido) y después por nombre. */
export function sortInfraServices<T extends Pick<InfraService, 'name' | 'status' | 'enabled'>>(
  rows: T[],
): T[] {
  return [...rows].sort((a, b) => {
    const sa = a.enabled ? ORDER[a.status.state] : 4
    const sb = b.enabled ? ORDER[b.status.state] : 4
    return sa - sb || a.name.localeCompare(b.name, 'es')
  })
}

/** Cuántos servicios hay en cada estado (los deshabilitados aparte). */
export function summarizeInfra(rows: Pick<InfraService, 'status' | 'enabled'>[]) {
  const out: Record<InfraServiceState | 'disabled', number> = {
    up: 0,
    down: 0,
    degraded: 0,
    unknown: 0,
    disabled: 0,
  }
  for (const r of rows) {
    if (!r.enabled) out.disabled++
    else out[r.status.state]++
  }
  return out
}

export function isInfraProblem(s: Pick<InfraService, 'status' | 'enabled'>) {
  return s.enabled && (s.status.state === 'down' || s.status.state === 'degraded')
}

/** Latencia del último chequeo activo con respuesta (ms) o null. */
export function infraLatency(s: Pick<InfraService, 'check_status'>): number | null {
  const active = s.check_status.filter(
    (c) => (c.type === 'icmp' || c.type === 'tcp' || c.type === 'http') && c.latency_ms != null,
  )
  if (!active.length) return null
  return Math.min(...active.map((c) => c.latency_ms!))
}

export function formatLatency(ms: number | null | undefined): string {
  if (ms == null) return '—'
  if (ms < 10) return `${ms.toFixed(1).replace('.', ',')} ms`
  return `${Math.round(ms)} ms`
}

/** Puerto efectivo de un chequeo tcp/http (http: 80 o 443 por defecto). */
export function checkPort(c: InfraCheck): number | null {
  if (c.port != null) return c.port
  if (c.type === 'http') return c.https ? 443 : 80
  return null
}

const nf = new Intl.NumberFormat('es')

/**
 * Resumen corto de un chequeo para la lista (`HTTPS :443 /health`, `TCP :554`, `Sin tráfico
 * 15 min`). `label` traduce las claves de `infra.checkShort.*`.
 */
export function describeCheck(
  c: InfraCheck,
  label: (key: string, p?: Record<string, unknown>) => string,
) {
  switch (c.type) {
    case 'icmp':
      return label('infra.checkShort.icmp')
    case 'tcp':
      return label('infra.checkShort.tcp', { port: checkPort(c) })
    case 'http':
      return label(c.https ? 'infra.checkShort.https' : 'infra.checkShort.http', {
        port: checkPort(c),
        path: c.path || '/',
      })
    case 'no_traffic':
      return label('infra.checkShort.noTraffic', { minutes: c.window_minutes ?? 15 })
    case 'ddos_inbound': {
      const parts: string[] = []
      if (c.pps_threshold != null) parts.push(`${nf.format(c.pps_threshold)} pps`)
      if (c.bps_threshold != null) parts.push(`${nf.format(c.bps_threshold)} bps`)
      return label('infra.checkShort.ddos', { threshold: parts.join(' / ') })
    }
  }
  return c.type
}

/** Chequeo nuevo con los valores por defecto del contrato. */
export function blankCheck(type: InfraCheckType): InfraCheck {
  switch (type) {
    case 'tcp':
      return { type, port: 443 }
    case 'http':
      return {
        type,
        port: 80,
        path: '/',
        https: false,
        tls_verify: true,
        expected_status_min: 200,
        expected_status_max: 399,
      }
    case 'no_traffic':
      return { type, window_minutes: 15, min_bytes: 1 }
    case 'ddos_inbound':
      return { type, pps_threshold: 50000, bps_threshold: null }
    default:
      return { type }
  }
}

const FIELDS: Record<InfraCheckType, (keyof InfraCheck)[]> = {
  icmp: [],
  tcp: ['port', 'forward_port'],
  http: [
    'port',
    'path',
    'https',
    'tls_verify',
    'expected_status_min',
    'expected_status_max',
    'forward_port',
  ],
  no_traffic: ['window_minutes', 'min_bytes'],
  ddos_inbound: ['pps_threshold', 'bps_threshold'],
}

/**
 * Cuerpo de un chequeo para la API: solo los campos de su tipo (el servidor rechaza los demás)
 * y sin `forward_port` si el servicio no se sondea por un router.
 */
export function checkPayload(c: InfraCheck, viaRouter: boolean): InfraCheck {
  const out: InfraCheck = { type: c.type }
  if (c.id) out.id = c.id
  for (const f of FIELDS[c.type]) {
    if (f === 'forward_port' && !viaRouter) continue
    const v = c[f]
    if (v !== undefined && v !== null && v !== '') (out as Record<string, unknown>)[f] = v
  }
  if (c.type === 'http' && !c.https) delete out.tls_verify
  return out
}

/** IP privada (RFC 1918, ULA, CGNAT, enlace local): Horus puede no alcanzarla sin el router. */
export function isPrivateIp(ip: string): boolean {
  const v4 = ip.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/)
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])]
    return (
      a === 10 ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && b === 168) ||
      (a === 100 && b >= 64 && b <= 127) ||
      (a === 169 && b === 254) ||
      a === 127
    )
  }
  const low = ip.toLowerCase()
  return /^f[cd][0-9a-f]{2}:/.test(low) || low.startsWith('fe80:') || low === '::1'
}

/** Validación ligera de IP (la definitiva la hace el servidor). */
export function isIpAddress(ip: string): boolean {
  const v4 = ip.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/)
  if (v4) return v4.slice(1).every((n) => Number(n) <= 255)
  return /^[0-9a-f:]+$/i.test(ip) && ip.includes(':') && ip.split('::').length <= 2
}
