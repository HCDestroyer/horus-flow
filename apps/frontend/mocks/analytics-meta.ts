import { iso } from './random'
import { problem } from './http'

/**
 * Rango, paso y `meta` de las series de la analítica simulada (`/analytics/*`), compartidos por
 * las series de tráfico, de clientes y de routers (SNMP e ICMP).
 */

/** Rango relativo → ms. */
export function rangeMs(range: string | null | undefined) {
  const map: Record<string, number> = {
    '15m': 0.25,
    '1h': 1,
    '6h': 6,
    '24h': 24,
    '7d': 168,
    '30d': 720,
    '90d': 2160,
  }
  return (map[range ?? '24h'] ?? 24) * 3_600_000
}

/** Paso (s) con el que se sirve un rango: ≤ ~500 puntos por serie, 60 s en la última hora. */
export function stepFor(span: number) {
  if (span <= 3_600_000) return 60
  if (span <= 6 * 3_600_000) return 300
  if (span <= 86_400_000) return 900
  return 3600
}

export function analyticsMeta(
  now: Date,
  range: string | null | undefined,
  step: number | null,
  source = 'flows',
) {
  const to = now.getTime()
  const span = rangeMs(range)
  // 30 días o más: una parte del rango cae antes de que el router empezara a exportar.
  const partial = span >= 30 * 86_400_000
  return {
    from: iso(to - span),
    to: iso(to),
    step,
    coverage: partial ? 0.71 : 0.97,
    partial,
    generated_at: now.toISOString(),
    source,
  }
}

/** `503 ANALYTICS_UNAVAILABLE` en el escenario degradado (ClickHouse caído). */
export function analyticsDown(degraded: boolean) {
  return degraded
    ? problem(503, 'ANALYTICS_UNAVAILABLE', 'Analítica no disponible', { 'Retry-After': '30' })
    : null
}
