/**
 * Scoring residencial/comercial simulado (I2, detection.yaml `GET /customers/{id}/scoring`,
 * devices.yaml `suggested_*`): determinista por índice de cliente. Espejo de la forma del
 * modelo `commercial-v1` (services/detection/internal/domain/scoring.go): seis características
 * normalizadas con su peso y aporte, razones con los números reales y la histéresis de 7 días.
 */
import type { CustomerScoring, Reason, ScoringFeature } from '~~/types/api'
import { isBaseCommercial } from './base'

const WEIGHTS = {
  inbound_services: 0.3,
  upload_ratio_high: 0.2,
  upload_vs_residential_median: 0.2,
  business_hours_pattern: 0.15,
  always_on: 0.1,
  many_remote_asns: 0.05,
} as const

export type MockScoringLevel = 'residential' | 'suspected' | 'commercial'

export interface MockScoring {
  score: number
  level: MockScoringLevel
  dynamic: boolean
  /** Ya publicado (supera la histéresis): devices guarda la sugerencia. */
  published: boolean
}

/**
 * Nivel simulado: el cliente 11 (tipo detectado) y uno de cada nueve son comerciales; otro de
 * cada nueve, residencial con indicios; el resto, residencial. `null` = sin scoring (inactivo).
 */
export function mockScoring(i: number, inactive: boolean): MockScoring | null {
  if (inactive) return null
  if (i === 11) return { score: 87, level: 'commercial', dynamic: false, published: true }
  if (isBaseCommercial(i))
    return { score: 90 + (i % 8), level: 'commercial', dynamic: false, published: true }
  if (i % 9 === 4) {
    return {
      score: 81 + ((i * 7) % 15),
      level: 'commercial',
      dynamic: false,
      published: i % 18 !== 13,
    }
  }
  if (i % 9 === 7)
    return { score: 52 + ((i * 5) % 22), level: 'suspected', dynamic: i % 4 === 3, published: true }
  return { score: 6 + ((i * 11) % 34), level: 'residential', dynamic: false, published: false }
}

function es(v: number, dec = 1) {
  return v.toFixed(dec).replace('.', ',')
}

function features(i: number, s: MockScoring): ScoringFeature[] {
  const f = s.score / 100
  const ports = s.level === 'residential' ? 0 : 1 + (i % 4)
  const peers = s.level === 'residential' ? 0 : 40 + ((i * 37) % 260)
  const ratio = 0.05 + f * 0.7
  const mult = 0.6 + f * 6
  const share = 0.3 + f * 0.45
  const on = 0.55 + f * 0.43
  const asns = 20 + Math.round(f * 320)
  const ramp = (v: number, lo: number, hi: number) => Math.max(0, Math.min(1, (v - lo) / (hi - lo)))
  const rows: [keyof typeof WEIGHTS, number, string, number, string][] = [
    [
      'inbound_services',
      ports,
      'ports',
      ports ? 0.5 * ramp(ports, 0, 3) + 0.5 * ramp(peers, 0, 100) : 0,
      `${ports} ${ports === 1 ? 'puerto de servicio' : 'puertos de servicio'} con conexiones entrantes de ${peers} IPs distintas`,
    ],
    [
      'upload_ratio_high',
      ratio,
      'ratio',
      ramp(ratio, 0.15, 0.6),
      `Ratio subida/bajada ${es(ratio, 2)} (residencial típico < 0,15)`,
    ],
    [
      'upload_vs_residential_median',
      mult,
      'x',
      ramp(mult, 1, 5),
      `Subida ${es(mult)}× la mediana residencial (${es(4 * mult)} GB en la ventana)`,
    ],
    [
      'business_hours_pattern',
      share,
      'share',
      ramp(share, 0.45, 0.75),
      `${Math.round(share * 100)} % del tráfico en horario laboral (L–S 08:00–19:00, hora local)`,
    ],
    [
      'always_on',
      on,
      'share',
      ramp(on, 0.6, 0.95),
      `Actividad en el ${Math.round(on * 100)} % de las horas de la ventana (${Math.round(on * 168)} de 168 h)`,
    ],
    [
      'many_remote_asns',
      asns,
      'asns',
      ramp(asns, 50, 300),
      `Tráfico saliente hacia ${asns} ASN distintos`,
    ],
  ]
  return rows.map(([code, value, unit, normalized, detail]) => ({
    code,
    value: Math.round(value * 100) / 100,
    unit,
    normalized: Math.round(normalized * 100) / 100,
    weight: WEIGHTS[code],
    contribution: Math.round(WEIGHTS[code] * normalized * 100) / 100,
    available: true,
    detail,
  }))
}

/** Razones (top 5 por aporte) y `dynamic_pool` si aplica. */
export function mockReasons(i: number, s: MockScoring): Reason[] {
  const out: Reason[] = features(i, s)
    .filter((f) => f.contribution >= 0.02)
    .sort((a, b) => b.contribution - a.contribution)
    .slice(0, 5)
    .map((f) => ({
      code: f.code,
      detail: f.detail,
      weight: f.contribution,
      data: { value: f.value, normalized: f.normalized },
    }))
  if (s.dynamic) {
    out.push({
      code: 'dynamic_pool',
      detail: 'La IP es de un pool dinámico (puede cambiar de cliente): confianza comercial ×0,60',
      weight: null,
      data: { factor: 0.6 },
    })
  }
  return out
}

/** Respuesta de `GET /customers/{id}/scoring` (latest). */
export function mockScoringDetail(
  customerId: string,
  i: number,
  s: MockScoring,
  kind: string,
  locked: boolean,
  now: Date,
): CustomerScoring {
  const day = 86_400_000
  const to = new Date(Math.floor(now.getTime() / 3_600_000) * 3_600_000)
  const commercial = s.score / 100
  const pending = !s.published && s.level !== 'residential'
  return {
    customer_id: customerId,
    model_version: 'commercial-v1',
    computed_at: new Date(to.getTime() - 3 * 3_600_000).toISOString(),
    window_from: new Date(to.getTime() - 7 * day).toISOString(),
    window_to: to.toISOString(),
    level: s.level,
    suggested_kind: s.level === 'commercial' ? 'commercial' : 'residential',
    commercial_score: s.score,
    raw_score: s.dynamic ? Math.min(100, Math.round(s.score / 0.6)) : s.score,
    confidence: s.level === 'commercial' ? commercial : Math.round((1 - commercial) * 100) / 100,
    scores: { residential: Math.round((1 - commercial) * 100) / 100, commercial },
    commercial_use_suspected: s.level === 'suspected',
    dynamic_pool: s.dynamic,
    customer_kind: kind,
    kind_locked: locked,
    published_level: s.published ? s.level : 'residential',
    published_at: s.published ? new Date(to.getTime() - (2 + (i % 5)) * day).toISOString() : null,
    pending_level: pending ? s.level : null,
    pending_since: pending ? new Date(to.getTime() - (2 + (i % 4)) * day).toISOString() : null,
    features: features(i, s),
    reasons: mockReasons(i, s),
    thresholds: { commercial: 80, suspected: 50, hysteresis_days: 7 },
  }
}

/** Serie diaria (90 días) que converge en la puntuación actual. */
export function mockScoringSeries(i: number, s: MockScoring, now: Date, days = 90) {
  const today = Math.floor(now.getTime() / 86_400_000) * 86_400_000
  return Array.from({ length: Math.min(days, 90) }, (_, k) => {
    const back = Math.min(days, 90) - 1 - k
    const drift = Math.round(((i * 13 + back * 7) % 11) - 5 - back * 0.15)
    const score = Math.max(0, Math.min(100, s.score + drift))
    const level: MockScoringLevel =
      score >= 80 ? 'commercial' : score >= 50 ? 'suspected' : 'residential'
    return {
      day: new Date(today - back * 86_400_000).toISOString().slice(0, 10),
      commercial_score: score,
      confidence: level === 'commercial' ? score / 100 : Math.round(100 - score) / 100,
      level,
      suggested_kind: level === 'commercial' ? 'commercial' : 'residential',
      kind: level === 'commercial' && s.published ? 'commercial' : 'residential',
    }
  })
}
