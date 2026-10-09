/**
 * Formato de cifras (frontend.md §13.4): bits/s para tasas, bytes para volumen, SI decimal,
 * español. Siempre devuelve valor y unidad por separado para poder dar a la unidad la mitad
 * de tamaño en los KPI (§7.4).
 */

export interface Formatted {
  value: string
  unit: string
}

const LOCALE = 'es-ES'

function fixed(value: number, digits: number) {
  return new Intl.NumberFormat(LOCALE, {
    maximumFractionDigits: digits,
    minimumFractionDigits: digits,
    useGrouping: 'min2',
  }).format(value)
}

function siScale(value: number, units: string[]): Formatted {
  let i = 0
  let v = Math.abs(value)
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  const digits = v >= 100 || i === 0 ? 0 : v >= 10 ? 1 : 2
  return { value: fixed(Math.sign(value) * v, digits), unit: units[i]! }
}

export function formatBps(bps: number | null | undefined): Formatted {
  if (bps === null || bps === undefined) return { value: '—', unit: '' }
  return siScale(bps, ['bit/s', 'kbit/s', 'Mbit/s', 'Gbit/s', 'Tbit/s'])
}

export function formatBytes(bytes: number | null | undefined): Formatted {
  if (bytes === null || bytes === undefined) return { value: '—', unit: '' }
  return siScale(bytes, ['B', 'kB', 'MB', 'GB', 'TB', 'PB'])
}

export function formatNumber(n: number | null | undefined, digits = 0): string {
  if (n === null || n === undefined) return '—'
  return fixed(n, digits)
}

export function formatPercent(ratio: number | null | undefined, digits = 0): string {
  if (ratio === null || ratio === undefined) return '—'
  return `${fixed(ratio * 100, digits)} %`
}

/** Une valor y unidad para texto plano (lectores de pantalla, tablas). */
export function joinUnit(f: Formatted) {
  return f.unit ? `${f.value} ${f.unit}` : f.value
}

/** Variación relativa (`0.12` → "+12 %"). */
export function formatChange(current: number | null, previous: number | null): string | null {
  if (current === null || previous === null || previous === 0) return null
  const pct = Math.round(((current - previous) / previous) * 100)
  const sign = pct > 0 ? '+' : pct < 0 ? '−' : '±'
  return `${sign}${fixed(Math.abs(pct), 0)} %`
}
