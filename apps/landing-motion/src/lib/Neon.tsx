// Glow por capas: cada elemento luminoso se pinta dos veces, una desenfocada (halo) y otra
// nítida encima (núcleo). El significado y la forma los lleva el trazo nítido; el halo es un
// degradado suave que el códec comprime bien y que, si se emborrona, no se pierde nada.
import type { ReactNode } from 'react'
import { AbsoluteFill } from 'remotion'
import { NEON } from './theme'

/** Filtros de desenfoque compartidos (una sola definición por composición). */
export function NeonDefs() {
  return (
    <defs>
      {[3, 6, 12, 24].map((s) => (
        <filter
          key={s}
          id={`blur${s}`}
          x="-50%"
          y="-50%"
          width="200%"
          height="200%"
          colorInterpolationFilters="sRGB"
        >
          <feGaussianBlur stdDeviation={s} />
        </filter>
      ))}
      <radialGradient id="vignette" cx="50%" cy="45%" r="75%">
        <stop offset="0%" stopColor={NEON.bgPanel} />
        <stop offset="100%" stopColor={NEON.bg} />
      </radialGradient>
    </defs>
  )
}

/**
 * Halo + núcleo. `blur` en unidades del viewBox (3, 6, 12 o 24); `halo` es la opacidad del
 * halo. Si `wide`, añade un segundo halo más ancho y tenue (para los acentos principales).
 */
export function Glow({
  children,
  blur = 6,
  halo = 0.9,
  wide = false,
  opacity = 1,
}: {
  children: ReactNode
  blur?: 3 | 6 | 12
  halo?: number
  wide?: boolean
  opacity?: number
}) {
  if (opacity <= 0.001) return null
  return (
    <g opacity={opacity}>
      {wide ? (
        <g filter={`url(#blur${blur * 2})`} opacity={halo * 0.6}>
          {children}
        </g>
      ) : null}
      <g filter={`url(#blur${blur})`} opacity={halo}>
        {children}
      </g>
      <g>{children}</g>
    </g>
  )
}

/** Lienzo SVG a pantalla completa con fondo y viñeta. */
export function Stage({
  width,
  height,
  children,
}: {
  width: number
  height: number
  children: ReactNode
}) {
  return (
    <AbsoluteFill style={{ backgroundColor: NEON.bg }}>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        height="100%"
        style={{ display: 'block' }}
      >
        <NeonDefs />
        <rect width={width} height={height} fill="url(#vignette)" />
        {children}
      </svg>
    </AbsoluteFill>
  )
}
