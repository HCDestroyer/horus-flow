import type { Customer, Reason } from '~~/types/api'

/**
 * Scoring residencial/comercial (I2, docs/api.md §2.9, frontend.md §8.1): utilidades puras de
 * presentación compartidas por la lista de "posibles comerciales" y la ficha del cliente.
 */

/** Orden de presentación de las características del modelo commercial-v1. */
export const SCORING_FEATURES = [
  'inbound_services',
  'upload_ratio_high',
  'upload_vs_residential_median',
  'business_hours_pattern',
  'always_on',
  'many_remote_asns',
] as const

/** Nivel de la sugerencia de un cliente de la lista: comercial, sospechoso o ninguno. */
export type CandidateLevel = 'commercial' | 'suspected' | 'none'

export function candidateLevel(
  c: Pick<Customer, 'suggested_kind' | 'commercial_use_suspected'>,
): CandidateLevel {
  if (c.suggested_kind === 'commercial') return 'commercial'
  if (c.commercial_use_suspected) return 'suspected'
  return 'none'
}

/**
 * Puntuación comercial 0–100 de un cliente: `commercial_score` del último scoring o, si falta,
 * la confianza de una sugerencia comercial. `null` si no hay scoring.
 */
export function commercialScore(
  c: Pick<Customer, 'commercial_score' | 'suggested_kind' | 'suggested_confidence'>,
): number | null {
  if (typeof c.commercial_score === 'number') return c.commercial_score
  if (c.suggested_kind === 'commercial' && typeof c.suggested_confidence === 'number') {
    return c.suggested_confidence
  }
  return null
}

/** Ordena los posibles comerciales: primero los de puntuación más alta (estable). */
export function sortCandidates<T extends Customer>(list: T[]): T[] {
  return list
    .map((c, i) => ({ c, i, s: commercialScore(c) ?? -1 }))
    .sort((a, b) => b.s - a.s || a.i - b.i)
    .map((x) => x.c)
}

/** Las n razones con más peso (las de peso nulo, como `dynamic_pool`, al final). */
export function topReasons(reasons: Reason[] | null | undefined, n = 3): Reason[] {
  return [...(reasons ?? [])]
    .map((r, i) => ({ r, i, w: typeof r.weight === 'number' ? r.weight : -1 }))
    .sort((a, b) => b.w - a.w || a.i - b.i)
    .slice(0, n)
    .map((x) => x.r)
}

/** Color semántico de una puntuación frente a los umbrales del ISP (nunca solo color). */
export function scoreColor(
  score: number | null,
  thresholds: { commercial: number; suspected: number } = { commercial: 80, suspected: 50 },
): 'info' | 'warning' | 'neutral' {
  if (score === null) return 'neutral'
  if (score >= thresholds.commercial) return 'info'
  if (score >= thresholds.suspected) return 'warning'
  return 'neutral'
}

/** Motivo de `set-kind` al confirmar o descartar desde la lista (3–500 caracteres). */
export function decisionReason(
  decision: 'confirm' | 'discard',
  c: Pick<Customer, 'suggested_reasons' | 'commercial_score' | 'suggested_confidence'>,
  labels: { confirm: string; discard: string },
): string {
  const codes = topReasons(c.suggested_reasons, 3).map((r) => r.code)
  const score = commercialScore({ ...c, suggested_kind: 'commercial' })
  const base = decision === 'confirm' ? labels.confirm : labels.discard
  const detail = [score !== null ? `${score}/100` : null, codes.length ? codes.join(', ') : null]
    .filter(Boolean)
    .join(' · ')
  return (detail ? `${base} (${detail})` : base).slice(0, 500)
}
