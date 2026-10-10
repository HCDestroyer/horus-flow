// Utilidades de movimiento para loops perfectos: todo lo que se mueve depende de `frame` de
// forma periódica (módulo la duración) o vuelve a su estado inicial antes del último frame, así
// que el frame N enlaza con el 0 sin salto.
import { Easing, interpolate, spring } from 'remotion'

export type Pt = readonly [number, number]

/** Fase en [0, 1) de un ciclo de `period` frames. */
export function phase(frame: number, period: number, offset = 0): number {
  const p = (frame + offset) % period
  return (p < 0 ? p + period : p) / period
}

/** Oscilación suave en [0, 1] con `cycles` vueltas enteras por loop (periódica en el loop). */
export function breathe(frame: number, loop: number, cycles: number, offset = 0): number {
  return 0.5 - 0.5 * Math.cos(2 * Math.PI * (frame / loop) * cycles + offset)
}

/**
 * Envolvente de entrada y salida: 0 → 1 entre `a` y `b`, 1 hasta `c` y 1 → 0 hasta `d`.
 * Con curva estándar (ease in-out) en ambos flancos.
 */
export function envelope(frame: number, a: number, b: number, c: number, d: number): number {
  return interpolate(frame, [a, b, c, d], [0, 1, 1, 0], {
    extrapolateLeft: 'clamp',
    extrapolateRight: 'clamp',
    easing: Easing.bezier(0.4, 0, 0.2, 1),
  })
}

/** Rampa 0 → 1 entre `a` y `b` (clamp). */
export function ramp(frame: number, a: number, b: number, ease = Easing.bezier(0.4, 0, 0.2, 1)) {
  return interpolate(frame, [a, b], [0, 1], {
    extrapolateLeft: 'clamp',
    extrapolateRight: 'clamp',
    easing: ease,
  })
}

/**
 * Muelle que arranca en `start` (0 antes). Amortiguación alta: movimiento con peso, sin rebote
 * llamativo (apple-design: springs críticamente amortiguados para UI informativa).
 */
export function springAt(
  frame: number,
  fps: number,
  start: number,
  opts: { damping?: number; stiffness?: number; mass?: number } = {},
): number {
  return spring({
    frame: frame - start,
    fps,
    config: { damping: opts.damping ?? 18, stiffness: opts.stiffness ?? 140, mass: opts.mass ?? 1 },
  })
}

/** `interpolate` con clamp en ambos extremos. */
export function interpolateClamp(frame: number, input: number[], output: number[]): number {
  return interpolate(frame, input, output, { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' })
}

/** Longitudes acumuladas de una polilínea. */
function lengths(points: readonly Pt[]): number[] {
  const acc = [0]
  for (let i = 1; i < points.length; i++) {
    const [x0, y0] = points[i - 1]!
    const [x1, y1] = points[i]!
    acc.push(acc[i - 1]! + Math.hypot(x1 - x0, y1 - y0))
  }
  return acc
}

/** Punto a la fracción `t` (por longitud) de una polilínea. */
export function along(points: readonly Pt[], t: number): Pt {
  const acc = lengths(points)
  const total = acc[acc.length - 1]!
  const d = Math.min(Math.max(t, 0), 1) * total
  for (let i = 1; i < points.length; i++) {
    if (d <= acc[i]!) {
      const seg = acc[i]! - acc[i - 1]!
      const k = seg === 0 ? 0 : (d - acc[i - 1]!) / seg
      const [x0, y0] = points[i - 1]!
      const [x1, y1] = points[i]!
      return [x0 + (x1 - x0) * k, y0 + (y1 - y0) * k]
    }
  }
  return points[points.length - 1]!
}

/** Fracción de la longitud total en la que empieza el vértice `i`. */
export function vertexT(points: readonly Pt[], i: number): number {
  const acc = lengths(points)
  return acc[i]! / acc[acc.length - 1]!
}

export function polyPath(points: readonly Pt[]): string {
  return points.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x} ${y}`).join(' ')
}

/** Hash determinista en [0, 1) (misma salida en cada render: nada de Math.random). */
export function hash01(i: number): number {
  return (((i * 2654435761) >>> 0) % 10007) / 10007
}

/** Mezcla lineal de dos colores #rrggbb. */
export function mix(a: string, b: string, t: number): string {
  const pa = parseInt(a.slice(1), 16)
  const pb = parseInt(b.slice(1), 16)
  const k = Math.min(Math.max(t, 0), 1)
  const ch = (s: number) => {
    const ca = (pa >> s) & 255
    const cb = (pb >> s) & 255
    return Math.round(ca + (cb - ca) * k)
  }
  return `#${((ch(16) << 16) | (ch(8) << 8) | ch(0)).toString(16).padStart(6, '0')}`
}
