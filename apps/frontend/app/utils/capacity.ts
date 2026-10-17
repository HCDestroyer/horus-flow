import type {
  CapacityForecast,
  CapacityForecastLevel,
  CapacityUplink,
  HealthStatus,
  UplinkDaily,
  UplinkKind,
} from '~~/types/api'
import type { TimeSeries } from '~/utils/chart-palette'

/**
 * Capacidad de enlaces y salud de nodos (rol snmp, NOC): presentación de los estados de
 * salud y de pronóstico (icono + texto + color; nunca solo color), orden de los enlaces,
 * capacidad en el formulario (Mb/s ↔ bits/s), series del gráfico de pronóstico y el
 * completado de los marcadores del script netwatch (la IP de la puerta de enlace se escribe
 * en el navegador y nunca viaja a la API).
 */

export const HEALTH_STATUSES = [
  'offline',
  'critical',
  'degraded',
  'warning',
  'unknown',
  'online',
] as const satisfies HealthStatus[]

export const UPLINK_KINDS = [
  'international_transit',
  'local_ix',
  'cache',
  'other',
] as const satisfies UplinkKind[]

type Color = 'success' | 'warning' | 'error' | 'neutral' | 'info'

/** Estado de salud de un nodo = vocabulario de equipos (estados.md, frontend.md §13.2). */
export const HEALTH_META: Record<HealthStatus, { color: Color; icon: string; halo: boolean }> = {
  online: { color: 'success', icon: 'i-lucide-circle-check', halo: false },
  warning: { color: 'warning', icon: 'i-lucide-triangle-alert', halo: false },
  degraded: { color: 'warning', icon: 'i-lucide-signal-low', halo: false },
  critical: { color: 'error', icon: 'i-lucide-octagon-alert', halo: true },
  offline: { color: 'error', icon: 'i-lucide-wifi-off', halo: true },
  unknown: { color: 'neutral', icon: 'i-lucide-circle-help', halo: false },
}

export function healthMeta(status: string) {
  return HEALTH_META[status as HealthStatus] ?? HEALTH_META.unknown
}

const HEALTH_ORDER: Record<HealthStatus, number> = {
  offline: 0,
  critical: 1,
  degraded: 2,
  warning: 3,
  unknown: 4,
  online: 5,
}

/** Peor salud primero y después por nombre. */
export function sortByHealth<T extends { status: string; name: string }>(rows: T[]): T[] {
  return [...rows].sort(
    (a, b) =>
      (HEALTH_ORDER[a.status as HealthStatus] ?? 4) -
        (HEALTH_ORDER[b.status as HealthStatus] ?? 4) || a.name.localeCompare(b.name, 'es'),
  )
}

/**
 * Nivel del pronóstico: urgente = acción ya (error), planificación = conviene mirarlo
 * (warning), sin acción = success; sin datos o sin capacidad informan (neutral).
 */
export const FORECAST_META: Record<CapacityForecastLevel, { color: Color; icon: string }> = {
  urgent: { color: 'error', icon: 'i-lucide-octagon-alert' },
  planning: { color: 'warning', icon: 'i-lucide-calendar-clock' },
  ok: { color: 'success', icon: 'i-lucide-circle-check' },
  insufficient_data: { color: 'neutral', icon: 'i-lucide-hourglass' },
  no_capacity: { color: 'neutral', icon: 'i-lucide-gauge' },
}

export function forecastMeta(level: string) {
  return FORECAST_META[level as CapacityForecastLevel] ?? FORECAST_META.insufficient_data
}

/** Nivel de una utilización frente a los umbrales de saturación. */
export function utilLevel(
  util: number | null | undefined,
  warn: number,
  crit: number,
): 'ok' | 'warning' | 'critical' {
  if (util == null) return 'ok'
  if (util >= crit) return 'critical'
  if (util >= warn) return 'warning'
  return 'ok'
}

/** Orden de la tabla de enlaces: peor utilización p95 primero, sin dato al final. */
export function sortUplinks(rows: CapacityUplink[]): CapacityUplink[] {
  const key = (u: CapacityUplink) => u.window?.util_p95 ?? u.utilization ?? -1
  return [...rows].sort((a, b) => key(b) - key(a) || a.label.localeCompare(b.label, 'es'))
}

/** Unidades del campo de capacidad contratada del formulario. */
export const CAPACITY_UNITS = { 'Mbit/s': 1e6, 'Gbit/s': 1e9 } as const
export type CapacityUnit = keyof typeof CAPACITY_UNITS

/** bits/s → valor y unidad del formulario (Gbit/s si es múltiplo exacto de 1 Gbit/s). */
export function capacityToForm(bps: number | null | undefined): {
  value: number | undefined
  unit: CapacityUnit
} {
  if (bps == null || bps <= 0) return { value: undefined, unit: 'Mbit/s' }
  if (bps >= 1e9 && bps % 1e9 === 0) return { value: bps / 1e9, unit: 'Gbit/s' }
  return { value: Math.round(bps / 1e6), unit: 'Mbit/s' }
}

/** Valor y unidad del formulario → bits/s (null si vacío). */
export function capacityFromForm(value: number | null | undefined, unit: CapacityUnit) {
  if (value == null || Number.isNaN(value) || value <= 0) return null
  return Math.round(value * CAPACITY_UNITS[unit])
}

/** Pico diario de un día (el mayor de los dos sentidos). */
export function dailyPeak(d: Pick<UplinkDaily, 'rx_p95_bps' | 'tx_p95_bps'>) {
  return Math.max(d.rx_p95_bps, d.tx_p95_bps)
}

/**
 * Series del gráfico de pronóstico: picos diarios observados (p95 de entrada y de salida),
 * la proyección y su banda (baja/alta). Los puntos de una serie fuera de su tramo son
 * huecos (`null`), nunca cero.
 */
export function forecastSeries(
  daily: UplinkDaily[],
  forecast: CapacityForecast | null | undefined,
  labels: { rx: string; tx: string; projection: string; low: string; high: string },
): { history: TimeSeries[]; projection: TimeSeries[] } {
  const history: TimeSeries[] = [
    { name: labels.rx, points: daily.map((d) => [d.day, d.samples ? d.rx_p95_bps : null]) },
    { name: labels.tx, points: daily.map((d) => [d.day, d.samples ? d.tx_p95_bps : null]) },
  ]
  const proj = forecast?.projection ?? []
  const projection: TimeSeries[] = proj.length
    ? [
        { name: labels.projection, points: proj.map((p) => [p.day, p.bps]) },
        { name: labels.low, points: proj.map((p) => [p.day, p.low_bps]) },
        { name: labels.high, points: proj.map((p) => [p.day, p.high_bps]) },
      ]
    : []
  return { history, projection }
}

/** Proporción (0..1) de cada clase de tráfico sobre lo clasificado; null sin datos. */
export function splitShares(s: {
  local_bytes: number
  international_bytes: number
}): { local: number; international: number } | null {
  const known = s.local_bytes + s.international_bytes
  if (!known) return null
  return { local: s.local_bytes / known, international: s.international_bytes / known }
}

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

export function isIPv4(s: string) {
  return IPV4.test(s.trim())
}

/**
 * Sustituye en el script netwatch cada marcador `{GATEWAY_n}` por la IP que escribió el
 * usuario. Solo IPv4 válidas; un marcador sin IP válida se deja tal cual (y se avisa).
 */
export function fillNetwatchScript(script: string, gateways: Record<string, string>) {
  let out = script
  const missing: string[] = []
  for (const key of new Set(script.match(/\{GATEWAY_\d+\}/g) ?? [])) {
    const ip = (gateways[key] ?? '').trim()
    if (isIPv4(ip)) out = out.split(key).join(ip)
    else missing.push(key)
  }
  return { script: out, missing }
}

/** Lista de ASN escrita a mano ("15169, 2906 36040") → números únicos ordenados. */
export function parseAsnList(text: string): { asns: number[]; invalid: string[] } {
  const parts = text
    .split(/[\s,;]+/)
    .map((p) => p.trim().replace(/^AS/i, ''))
    .filter(Boolean)
  const asns: number[] = []
  const invalid: string[] = []
  for (const p of parts) {
    const n = Number(p)
    if (Number.isInteger(n) && n >= 1 && n <= 4294967295) asns.push(n)
    else invalid.push(p)
  }
  return { asns: [...new Set(asns)].sort((a, b) => a - b), invalid }
}

const V4_CIDR = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}\/(\d|[12]\d|3[0-2])$/
const V6_CIDR = /^[0-9a-f:]+(\.\d+\.\d+\.\d+\.\d+)?\/(\d|[1-9]\d|1[01]\d|12[0-8])$/i

/**
 * Lista de prefijos escrita a mano (uno por línea o separados por comas) → prefijos únicos
 * en el orden escrito e inválidos. La normalización (red, orden) la hace la API.
 */
export function parsePrefixList(text: string): { prefixes: string[]; invalid: string[] } {
  const parts = text
    .split(/[\s,;]+/)
    .map((p) => p.trim())
    .filter(Boolean)
  const prefixes: string[] = []
  const invalid: string[] = []
  for (const p of parts) {
    if (V4_CIDR.test(p) || (p.includes(':') && V6_CIDR.test(p))) {
      if (!prefixes.includes(p.toLowerCase())) prefixes.push(p.toLowerCase())
    } else invalid.push(p)
  }
  return { prefixes, invalid }
}

/** mm/aaaa (UTC: las fechas de pronóstico son días, sin hora). */
export function formatMonth(iso: string | null | undefined) {
  if (!iso) return '—'
  const d = new Date(iso)
  return `${String(d.getUTCMonth() + 1).padStart(2, '0')}/${d.getUTCFullYear()}`
}

/** dd/mm/aaaa (UTC). */
export function formatDay(iso: string | null | undefined) {
  if (!iso) return '—'
  const d = new Date(iso)
  return `${String(d.getUTCDate()).padStart(2, '0')}/${String(d.getUTCMonth() + 1).padStart(2, '0')}/${d.getUTCFullYear()}`
}

/** Crecimiento con signo: "+4,2 %", "−3,0 %", "+28 %". */
export function formatSigned(r: number | null | undefined) {
  if (r == null) return '—'
  const p = r * 100
  const sign = p < 0 ? '−' : '+'
  const a = Math.abs(p)
  const txt = a < 10 ? a.toFixed(1).replace('.', ',') : String(Math.round(a))
  return `${sign}${txt} %`
}
