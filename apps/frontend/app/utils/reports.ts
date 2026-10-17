import type { ReportFormat, ReportRun, ReportStatus } from '~~/types/api'

/**
 * Reportes del ISP (rol reporting, I3): estado de una ejecución, periodo en la zona horaria del
 * ISP (`dd/mm/aaaa HH:mm`, frontend.md §13.4) y conversión de un rango de días elegido en la
 * consola a instantes UTC en esa zona (segura con cambios de horario).
 */

/** Estado → color semántico + icono (nunca solo color; horus-identity, estados.md). */
export const REPORT_STATUS_META: Record<
  ReportStatus,
  { color: 'success' | 'neutral' | 'error'; icon: string; spinning?: boolean }
> = {
  completed: { color: 'success', icon: 'i-lucide-circle-check' },
  queued: { color: 'neutral', icon: 'i-lucide-loader-circle', spinning: true },
  running: { color: 'neutral', icon: 'i-lucide-loader-circle', spinning: true },
  failed: { color: 'error', icon: 'i-lucide-circle-x' },
}

export const REPORT_FORMATS: ReportFormat[] = ['pdf', 'csv']

/** ¿Sigue generándose? (la página se refresca mientras haya alguna así). */
export function isReportPending(r: Pick<ReportRun, 'status'>) {
  return r.status === 'queued' || r.status === 'running'
}

/** Fecha y hora `dd/mm/aaaa HH:mm` en la zona `tz` (la del ISP). */
export function formatInZone(iso: string | Date, tz: string) {
  const d = typeof iso === 'string' ? new Date(iso) : iso
  const parts = new Intl.DateTimeFormat('es-ES', {
    timeZone: tz,
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(d)
  const get = (t: Intl.DateTimeFormatPartTypes) => parts.find((p) => p.type === t)?.value ?? ''
  return `${get('day')}/${get('month')}/${get('year')} ${get('hour')}:${get('minute')}`
}

/** Periodo `[from, to)` mostrado con el último minuto incluido ("05/10/2026 00:00 – 11/10/2026 23:59"). */
export function formatReportPeriod(r: Pick<ReportRun, 'period_from' | 'period_to' | 'timezone'>) {
  const to = new Date(new Date(r.period_to).getTime() - 60_000)
  return `${formatInZone(r.period_from, r.timezone)} – ${formatInZone(to, r.timezone)}`
}

/** "2026-W41" → { year: 2026, week: 41 }. */
export function parseIsoWeek(label: string) {
  const m = /^(\d{4})-W(\d{2})$/.exec(label)
  return m ? { year: Number(m[1]), week: Number(m[2]) } : null
}

/** Desplazamiento (ms) de la zona `tz` respecto a UTC en el instante `at`. */
function zoneOffset(at: number, tz: string) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: tz,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(new Date(at))
  const n = (t: Intl.DateTimeFormatPartTypes) => Number(parts.find((p) => p.type === t)?.value)
  const asUtc = Date.UTC(n('year'), n('month') - 1, n('day'), n('hour'), n('minute'), n('second'))
  return asUtc - Math.floor(at / 1000) * 1000
}

/**
 * Medianoche local del día `yyyy-mm-dd` en `tz`, como instante UTC. Dos pasadas: el
 * desplazamiento se recalcula en el instante resultante (cambios de horario).
 */
export function zonedMidnight(day: string, tz: string) {
  const [y, m, d] = day.split('-').map(Number)
  const guess = Date.UTC(y!, m! - 1, d!)
  let at = guess - zoneOffset(guess, tz)
  at = guess - zoneOffset(at, tz)
  return new Date(at)
}

/** Suma días de calendario a `yyyy-mm-dd`. */
export function addCalendarDays(day: string, n: number) {
  const [y, m, d] = day.split('-').map(Number)
  return new Date(Date.UTC(y!, m! - 1, d! + n)).toISOString().slice(0, 10)
}

/** Hoy (`yyyy-mm-dd`) en la zona `tz`. */
export function todayInZone(tz: string, now = new Date()) {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: tz,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(now)
  return parts
}

/**
 * Rango de días incluidos `[desde, hasta]` → `{from, to}` en UTC (`to` = medianoche del día
 * siguiente a `hasta`), o null si no es válido (orden o más de `maxDays`).
 */
export function dayRangeToPeriod(fromDay: string, toDay: string, tz: string, maxDays = 31) {
  if (!fromDay || !toDay || fromDay > toDay) return null
  const from = zonedMidnight(fromDay, tz)
  const to = zonedMidnight(addCalendarDays(toDay, 1), tz)
  const days = Math.round((to.getTime() - from.getTime()) / 86_400_000)
  if (days > maxDays) return null
  return { from: from.toISOString(), to: to.toISOString() }
}

/** Nombre de archivo de `Content-Disposition` (o uno por defecto). */
export function filenameFrom(header: string | null, fallback: string) {
  const m = header ? /filename="?([^";]+)"?/i.exec(header) : null
  return m?.[1] ?? fallback
}
