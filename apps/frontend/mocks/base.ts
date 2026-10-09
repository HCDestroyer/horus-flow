import type { BotnetSignal, FindingFeedRow } from '~/widgets/shapes'
import findingExample from '~~/types/api/contract/examples/finding-outbound-scanning.api.json'
import type { MockTenant } from './data'
import { hash, iso, rng } from './random'
import { isClosedByUser, mockState } from './state'

/**
 * Base única de la API simulada: identidad de los clientes (IP, alias, nodo) y hallazgos
 * abiertos. De aquí salen los widgets, la lista y la ficha de clientes, los hallazgos y el
 * resumen de seguridad, para que ninguna pantalla contradiga a otra (README › Decisiones).
 */

export function tenantScale(tenant: MockTenant) {
  return 1 + (hash(tenant.tenant_id) % 7) / 4 // 1–2.5
}

/** Etiqueta IPv6 por ISP: los prefijos delegados salen de `2001:db8:<tag>00::/40`. */
export function v6Tag(tenant: MockTenant) {
  return ({ 'fibra-norte': '4a', 'valle-conecta': '5b', 'red-andina': '6c' } as const)[
    tenant.tenant_slug as 'fibra-norte'
  ]
}

/** Prefijo IPv6 de clientes del ISP (D22: cada prefijo delegado es un cliente). */
export function v6CustomerPrefix(tenant: MockTenant) {
  return `2001:db8:${v6Tag(tenant)}00::/40`
}

/** Largo del prefijo delegado a cada abonado IPv6 (`ipv6_client_len`). */
export const V6_CLIENT_LEN = 56

/** Clientes IPv6 (D22): un prefijo delegado por abonado, independiente de su IPv4. */
export const V6_INDICES = [40, 41, 42, 43, 44, 45]
/** Clientes inactivos del tramo con identidad (los demás inactivos salen cada 37). */
export const INACTIVE_INDICES = [46, 47, 48, 49]

/** Dirección canónica del cliente `i` (IPv6 truncada a su prefijo delegado). */
export function customerAddress(tenant: MockTenant, i: number) {
  if (i === 0) return findingExample.customer.address.replace('10.20', tenant.prefix)
  if (V6_INDICES.includes(i)) {
    const third = `${v6Tag(tenant)}${i.toString(16).padStart(2, '0')}`
    return `2001:db8:${third}:${((i * 7) % 256).toString(16)}00::`
  }
  if (i < 50) return `${tenant.prefix}.${i % 3}.${10 + ((i * 37) % 240)}`
  return `${tenant.prefix}.${3 + Math.floor((i - 50) / 240)}.${10 + ((i - 50) % 240)}`
}

export function isV6Customer(i: number) {
  return V6_INDICES.includes(i)
}

const BASE_ALIASES: Record<number, string> = {
  2: 'Ferretería El Puente',
  5: 'Panadería Sol',
  8: 'Clínica Dental Norte',
  17: 'Hotel Mirador',
}

/** Alias base (antes de cambios del usuario): comercios con alias manual. */
export function baseAlias(i: number) {
  return BASE_ALIASES[i] ?? null
}

export function isBaseCommercial(i: number) {
  return i in BASE_ALIASES
}

/** Nodo del cliente `i`: los nodos con tráfico del ISP, en orden. */
export function customerSite(tenant: MockTenant, i: number) {
  return tenant.sites[i % tenant.sites.length]!
}

export const CATEGORIES = [
  'Streaming de video',
  'Redes sociales',
  'Videojuegos',
  'Actualizaciones de software',
  'Navegación web',
  'Mensajería',
  'Nube y almacenamiento',
  'Videollamadas',
  'Música',
  'Comercio electrónico',
]

export function maskIp(ip: string) {
  if (ip.includes(':')) {
    const groups = ip.split(':')
    return `${groups.slice(0, 3).join(':')}:••••::`
  }
  const parts = ip.split('.')
  return `${parts.slice(0, 3).join('.')}.•••`
}

/** Plantillas de resumen de hallazgos (el primero, del ejemplo del contrato). */
export const FINDING_TEMPLATES: Pick<FindingFeedRow, 'kind' | 'severity' | 'summary'>[] = [
  {
    kind: findingExample.kind,
    severity: findingExample.severity as FindingFeedRow['severity'],
    summary: findingExample.summary.text,
  },
  {
    kind: 'botnet_c2_communication',
    severity: 'critical',
    summary: 'Conexiones a un servidor de control de botnet conocido (Feodo Tracker)',
  },
  {
    kind: 'spam_smtp_outbound',
    severity: 'medium',
    summary: 'SMTP saliente directo a 86 servidores en 1 h',
  },
  {
    kind: 'beaconing',
    severity: 'medium',
    summary: 'Conexiones periódicas cada 60 s al mismo destino',
  },
  {
    kind: 'outbound_scanning',
    severity: 'high',
    summary: 'Escaneo del puerto 445 a 610 destinos en 10 min',
  },
  {
    kind: 'ddos_participation',
    severity: 'high',
    summary: 'Ráfaga de 48 000 pps UDP/123 hacia 2 destinos',
  },
  {
    kind: 'reputation_hit',
    severity: 'low',
    summary: 'Contacto con una IP listada en Spamhaus DROP',
  },
  {
    kind: 'outbound_scanning',
    severity: 'medium',
    summary: 'Escaneo del puerto 7547 a 320 destinos en 15 min',
  },
  {
    kind: 'beaconing',
    severity: 'low',
    summary: 'Conexiones periódicas cada 300 s a un dominio nuevo',
  },
  {
    kind: 'spam_smtp_outbound',
    severity: 'low',
    summary: 'SMTP saliente directo a 14 servidores en 1 h',
  },
]

/** Señales de botnet (traffic-model.md §8) que aporta cada tipo de hallazgo. */
export const SIGNALS_BY_KIND: Record<string, BotnetSignal[]> = {
  outbound_scanning: ['scanning', 'fan_out', 'watched_ports'],
  botnet_c2_communication: ['c2_contact'],
  beaconing: ['beaconing'],
  spam_smtp_outbound: ['smtp', 'fan_out'],
  ddos_participation: ['ddos', 'sustained_upload'],
  reputation_hit: [],
}

export const SEVERITY_ORDER = ['info', 'low', 'medium', 'high', 'critical']

/** Hallazgo de la base simulada: lo que ve el feed más lo que necesitan los agregados. */
export interface MockFinding extends FindingFeedRow {
  /** Índice del cliente en el pool sintético del ISP (agrupa por cliente). */
  customer: number
  opened_at: string
  /** Posición en la base (determina evidencia, ocurrencias y estado inicial). */
  seq: number
}

const FINDING_ID_PREFIX = findingExample.id.slice(0, -2)

export function findingId(i: number) {
  return `${FINDING_ID_PREFIX}${String(i).padStart(2, '0')}`
}

/** Pool de clientes con hallazgos: los 14 primeros (los diez más recientes, distintos). */
const POOL = 14

/**
 * Hallazgos activos (abiertos o reconocidos) del ISP: **una sola base** de la que salen el
 * resumen (`findings_summary`), el feed (`findings_feed`), la tendencia (`findings_trend`),
 * la seguridad por nodo (`security_by_node`), las señales de botnet (`botnet_signals`), la
 * lista de hallazgos y el estado de seguridad de cada cliente, para que nunca se contradigan.
 * Determinista por ISP; fechas relativas a `now`. Un cliente con un hallazgo de C2 es
 * "Infectado" (D18) en todos sus hallazgos; el resto, "Sospechoso". Los que el usuario
 * resolvió o marcó como falso positivo salen de la base; los que llegan "en vivo" entran.
 */
export function openFindings(tenant: MockTenant, now: Date): MockFinding[] {
  const r = rng(hash(tenant.tenant_id + 'findings-base'))
  const t = now.getTime()
  const total = 30 + (hash(tenant.tenant_id) % 12)
  const base = Array.from({ length: total }, (_, i) => {
    const template =
      i < FINDING_TEMPLATES.length
        ? FINDING_TEMPLATES[i]!
        : FINDING_TEMPLATES[Math.floor(r() * FINDING_TEMPLATES.length)]!
    // Los diez más recientes son de clientes distintos; los demás repiten clientes.
    const customer = i < 10 ? i : Math.floor(r() * POOL)
    const lastSeen = t - (i * 7 + 1) * 60_000 - Math.round(r() * 50_000)
    // Abiertos en los últimos 30 días; dos tercios en la última semana.
    const age = (r() < 0.66 ? r() * 7 : 7 + r() * 23) * 86_400_000
    return { template, customer, lastSeen, opened: Math.min(lastSeen, t - age), seq: i }
  })
  // Hallazgos nuevos llegados por el tiempo real simulado (más recientes que la base).
  const live = (mockState.liveFindings.get(tenant.tenant_id) ?? [])
    .filter((at) => at <= t)
    .map((at, k) => ({
      template: FINDING_TEMPLATES[(k * 3 + 4) % FINDING_TEMPLATES.length]!,
      customer: 20 + (k % 6) * 3,
      lastSeen: at,
      opened: at,
      seq: 90 + k,
    }))
  const all = [...base, ...live].filter((f) => !isClosedByUser(findingId(f.seq)))
  const infected = new Set(
    all.filter((f) => f.template.kind === 'botnet_c2_communication').map((f) => f.customer),
  )
  return all.map(({ template, customer, lastSeen, opened, seq }) => ({
    id: findingId(seq),
    seq,
    ...template,
    customer,
    customer_ip: customerAddress(tenant, customer),
    alias: baseAlias(customer),
    site: customerSite(tenant, customer).name,
    security_state: infected.has(customer) ? 'infected' : 'suspected',
    confidence:
      template.kind === 'botnet_c2_communication'
        ? 0.94
        : Math.round((0.55 + rng(hash(tenant.tenant_id + seq))() * 0.4) * 100) / 100,
    opened_at: iso(opened),
    last_seen_at: iso(lastSeen),
  }))
}
