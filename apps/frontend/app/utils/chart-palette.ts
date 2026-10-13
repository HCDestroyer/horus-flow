import type { BarSeriesOption, LineSeriesOption } from 'echarts/charts'
import type {
  GridComponentOption,
  LegendComponentOption,
  TooltipComponentOption,
} from 'echarts/components'
import type { ComposeOption } from 'echarts/core'

/**
 * Paleta de gráficos (frontend.md §13.2; skill dataviz › palette). Categórica en orden fijo,
 * nunca cíclica; los colores de estado (success/warning/error) nunca se usan como series.
 *
 * Validada con `dataviz/scripts/validate_palette.js` (5 primeras ranuras):
 * - claro sobre #ffffff: CVD adyacente ΔE ≥ 9,1, visión normal ≥ 19,6; aguamarina, amarillo y
 *   magenta < 3:1 de contraste ⇒ alivio obligatorio: leyenda/etiquetas visibles y tabla
 *   alternativa (cada gráfico la lleva).
 * - oscuro sobre #121214: CVD ≥ 8,4, visión normal ≥ 19,3, todas ≥ 3:1.
 */
export const CATEGORICAL = {
  light: ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4'],
  dark: ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181'],
} as const

/**
 * Tinta del gráfico por tema, con los mismos valores que los tokens de main.css (neutros
 * zinc; el canvas de ECharts necesita colores sRGB literales, no `var()` ni oklch).
 * Al cambiar el tema se re-tematiza sin recargar datos (§5.3).
 */
export const CHART_INK = {
  light: {
    text: '#3f3f46',
    muted: '#52525b',
    grid: '#e4e4e7',
    axis: '#a1a1aa',
    surface: '#ffffff',
  },
  dark: { text: '#e4e4e7', muted: '#a1a1aa', grid: '#232a33', axis: '#4b5563', surface: '#131820' },
} as const

export type ChartMode = keyof typeof CHART_INK

/** Líneas de umbral (aviso / crítico): los colores de estado, solo como referencia, nunca como serie. */
export const THRESHOLD_INK = {
  light: { warning: '#a16207', critical: '#dc2626' },
  dark: { warning: '#e9b949', critical: '#f25c69' },
} as const

/** Fila de `BarList` (top N). */
export interface BarItem {
  label: string
  value: number
  display: string
  /** Texto secundario (p. ej. subida). */
  detail?: string
  mono?: boolean
  muted?: boolean
}

/** Opción de ECharts con solo los módulos registrados en `EChart.vue`. */
export type ChartOption = ComposeOption<
  | LineSeriesOption
  | BarSeriesOption
  | GridComponentOption
  | LegendComponentOption
  | TooltipComponentOption
>

/** Serie temporal de `TimeSeriesChart` (puntos `[ISO, valor | null]`, null = hueco). */
export interface TimeSeries {
  name: string
  points: [string, number | null][]
}
