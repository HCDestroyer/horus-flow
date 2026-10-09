/** Utilidades deterministas de la API simulada (semilla = ISP, minuto…). */

/** FNV-1a de 32 bits. */
export function hash(text: string) {
  let h = 2166136261
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

/** PRNG determinista (mulberry32). */
export function rng(seed: number) {
  let a = seed
  return () => {
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export const iso = (ms: number) => new Date(ms).toISOString()

/** UUIDv7 sintético y estable: `kind` (8 hex) + número. */
export function mockUuid(kind: string, n: number | string) {
  const tail = String(n)
    .replace(/[^0-9a-f]/gi, '')
    .padStart(12, '0')
    .slice(-12)
  return `${kind.padEnd(8, '0').slice(0, 8)}-0000-7000-8000-${tail}`
}
