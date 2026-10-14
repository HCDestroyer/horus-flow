// Paleta neón derivada de los tokens de la landing (apps/landing/app/assets/css/main.css).
//
// - El acento lapislázuli (#1d3fa8 / #93b1ff) se vuelve neón azul-cian: es el color de los
//   datos que circulan y de Horus.
// - El rojo neón SOLO significa "Infectado" (color.md › "Avoid using the same color to mean
//   different things"): nada más en las composiciones usa ese tono.
// - El fondo es el --ui-bg oscuro de la landing algo más profundo, para que el glow respire.
export const NEON = {
  bg: '#060a14',
  bgPanel: '#0d1220', // --ui-bg (oscuro)
  bgRaised: '#151c2e', // --surface (oscuro)
  border: '#263049', // --ui-border (oscuro)
  borderStrong: '#3a4766', // --ui-border-accented (oscuro)

  dot: '#2f3a55', // --dot (oscuro)
  dotStrong: '#4d5b7d', // --dot-strong (oscuro)

  lapis: '#93b1ff', // --ui-primary (oscuro), núcleo de los trazos
  lapisDeep: '#4a6de0', // lapis-500, halo
  cyan: '#5fd4ff', // extremo cian del acento, paquetes de datos
  cyanCore: '#d6f4ff', // núcleo casi blanco de los paquetes

  infected: '#ff4d5e', // halo rojo neón: solo "Infectado"
  infectedCore: '#ff8b7e', // --infected (oscuro), núcleo

  text: '#e8ecf4', // --ui-text (oscuro)
  textMuted: '#a4aec4', // --ui-text-muted (oscuro)
} as const

export const FONT_SANS =
  "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', 'Noto Sans', 'Liberation Sans', Arial, sans-serif"
export const FONT_MONO =
  "ui-monospace, 'SF Mono', SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace"
