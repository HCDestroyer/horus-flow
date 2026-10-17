import type { RouterEffectiveStatus, RouterStatus } from '~~/types/api'

/**
 * Salud de routers (EP-08, api.md §2.6): los 8 estados efectivos con su presentación
 * (color + icono; el texto lo da i18n `routerStatus.*`), umbrales de los medidores, orden
 * "problemas primero" y utilidades compartidas por la vista NOC, la ficha y el widget.
 * `stale` = "no sabemos" (nunca se pinta como caído); `unknown` = nunca observado.
 */

export const ROUTER_STATUSES = [
  'online',
  'warning',
  'critical',
  'degraded',
  'offline',
  'stale',
  'unknown',
  'maintenance',
] as const

/** Problemas primero (snmp.yaml): critical, offline, degraded, warning, stale, unknown. */
export const ROUTER_STATUS_ORDER: Record<RouterEffectiveStatus, number> = {
  critical: 0,
  offline: 1,
  degraded: 2,
  warning: 3,
  stale: 4,
  unknown: 5,
  maintenance: 6,
  online: 7,
}

export interface StatusVisual {
  /** Clase de texto semántica (nunca color crudo). */
  cls: string
  /** Clase de fondo para puntos y barras. */
  bg: string
  icon: string
  /** Halo solo en lo crítico/caído (frontend.md §13.1). */
  halo: boolean
}

export const ROUTER_STATUS_VISUAL: Record<RouterEffectiveStatus, StatusVisual> = {
  online: { cls: 'text-success', bg: 'bg-success', icon: 'i-lucide-circle-check', halo: false },
  warning: { cls: 'text-warning', bg: 'bg-warning', icon: 'i-lucide-triangle-alert', halo: false },
  critical: { cls: 'text-error', bg: 'bg-error', icon: 'i-lucide-octagon-alert', halo: true },
  degraded: { cls: 'text-warning', bg: 'bg-warning', icon: 'i-lucide-signal-low', halo: false },
  offline: { cls: 'text-error', bg: 'bg-error', icon: 'i-lucide-wifi-off', halo: true },
  stale: {
    cls: 'text-muted',
    bg: 'bg-(--ui-border-accented)',
    icon: 'i-lucide-clock-alert',
    halo: false,
  },
  unknown: {
    cls: 'text-muted',
    bg: 'bg-(--ui-border-accented)',
    icon: 'i-lucide-circle-help',
    halo: false,
  },
  maintenance: { cls: 'text-info', bg: 'bg-info', icon: 'i-lucide-wrench', halo: false },
}

export function routerStatusVisual(status: string): StatusVisual {
  return ROUTER_STATUS_VISUAL[status as RouterEffectiveStatus] ?? ROUTER_STATUS_VISUAL.unknown
}

/** Umbrales de los medidores (los mismos que el sondeador, events.md §8.4). */
export const CPU_THRESHOLDS = { warning: 80, critical: 95 }
export const MEMORY_THRESHOLDS = { warning: 80, critical: 90 }
export const TEMP_THRESHOLDS = { warning: 70, critical: 85 }

export type MeterLevel = 'ok' | 'warning' | 'critical'

export function meterLevel(
  value: number | null | undefined,
  thresholds: { warning: number; critical: number },
): MeterLevel {
  if (value === null || value === undefined) return 'ok'
  if (value >= thresholds.critical) return 'critical'
  if (value >= thresholds.warning) return 'warning'
  return 'ok'
}

export function sortRouterStatuses<T extends { status: RouterEffectiveStatus; name: string }>(
  list: readonly T[],
) {
  return [...list].sort(
    (a, b) =>
      ROUTER_STATUS_ORDER[a.status] - ROUTER_STATUS_ORDER[b.status] || a.name.localeCompare(b.name),
  )
}

export function isRouterProblem(status: RouterEffectiveStatus) {
  return status !== 'online' && status !== 'maintenance'
}

/** Resumen por estado de la barra de la vista NOC: `nodata` = stale + unknown (+ maintenance). */
export type StatusBucket = 'online' | 'warning' | 'critical' | 'degraded' | 'offline' | 'nodata'
export const STATUS_BUCKETS: StatusBucket[] = [
  'online',
  'warning',
  'critical',
  'degraded',
  'offline',
  'nodata',
]

export function bucketOf(status: RouterEffectiveStatus): StatusBucket {
  if (status === 'stale' || status === 'unknown' || status === 'maintenance') return 'nodata'
  return status
}

export function summarizeStatuses(list: readonly { status: RouterEffectiveStatus }[]) {
  const out: Record<StatusBucket, number> = {
    online: 0,
    warning: 0,
    critical: 0,
    degraded: 0,
    offline: 0,
    nodata: 0,
  }
  for (const r of list) out[bucketOf(r.status)]++
  return out
}

/** "12 d 4 h" · "3 h 12 min" · "48 min". */
export function formatUptime(seconds: number | null | undefined) {
  if (seconds === null || seconds === undefined) return '—'
  const d = Math.floor(seconds / 86_400)
  const h = Math.floor((seconds % 86_400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d} d ${h} h`
  if (h > 0) return `${h} h ${m} min`
  return `${m} min`
}

export function formatCelsius(value: number | null | undefined, digits = 0) {
  if (value === null || value === undefined) return '—'
  return `${formatNumber(value, digits)} °C`
}

export function formatMs(value: number | null | undefined, digits = 1) {
  if (value === null || value === undefined) return '—'
  return `${formatNumber(value, digits)} ms`
}

/** 0–100 → "52 %" (los medidores reciben porcentajes, no razones). */
export function formatPct(value: number | null | undefined, digits = 0) {
  if (value === null || value === undefined) return '—'
  return formatPercent(value / 100, digits)
}

/**
 * Aplica la proyección de un evento `routers.status` (horus.snmp.router.state_changed) a la
 * fila del router sin pedir toda la lista: solo las claves que trae el evento.
 */
export function patchRouterStatus(row: RouterStatus, data: Record<string, unknown>): RouterStatus {
  const keys: (keyof RouterStatus)[] = [
    'status',
    'observed_state',
    'reason',
    'since',
    'last_observed_at',
    'signals',
    'device',
    'uplink',
    'interfaces_up',
    'interfaces_total',
    'thresholds',
    'last_flow_at',
  ]
  const next = { ...row }
  for (const k of keys) {
    if (k in data) (next as Record<string, unknown>)[k] = data[k]
  }
  return next
}
