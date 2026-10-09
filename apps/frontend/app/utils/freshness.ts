/**
 * Frescura de un dato (frontend.md §7.6, §10.2): fresco (≤ 2·T), atrasado (2–5·T, atenuado +
 * reloj), obsoleto (> 5·T, "Sin datos recientes"; nunca presentado como actual ni como cero).
 * T = intervalo esperado de actualización del widget.
 */
export type FreshnessLevel = 'fresh' | 'late' | 'stale'

export function freshnessLevel(ageSeconds: number, expectedSeconds: number): FreshnessLevel {
  if (ageSeconds > 5 * expectedSeconds) return 'stale'
  if (ageSeconds > 2 * expectedSeconds) return 'late'
  return 'fresh'
}

/** Clave i18n y parámetros de "hace N s / min / h / días". */
export function agoParts(ageSeconds: number): { key: string; n: number } {
  const s = Math.max(0, Math.round(ageSeconds))
  if (s < 60) return { key: 'time.agoSeconds', n: s }
  if (s < 3600) return { key: 'time.agoMinutes', n: Math.floor(s / 60) }
  if (s < 2 * 86_400) return { key: 'time.agoHours', n: Math.floor(s / 3600) }
  return { key: 'time.agoDays', n: Math.floor(s / 86_400) }
}

/** Instante del dato: generado en servidor menos su edad declarada (`meta.freshness_seconds`). */
export function dataTime(meta: { generated_at: string; freshness_seconds?: number | null }) {
  return new Date(meta.generated_at).getTime() - (meta.freshness_seconds ?? 0) * 1000
}
