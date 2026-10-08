import type { DashboardWidget, WidgetData, WidgetDataMeta } from '~~/types/api'
import type { WidgetScale } from '~/utils/dashboard-layout'

/**
 * Props que `WidgetHost` pasa a cada `Widget.vue`. El host ya resolvió carga, error,
 * permiso, vacío y frescura: el widget solo pinta datos válidos.
 * Evento opcional `live(at: number)`: el widget recibió un dato en vivo (WS) a esa hora.
 */
export interface WidgetViewProps {
  widget: DashboardWidget
  /** `null` solo en tipos sin datos (`data_endpoint_kind: none`, p. ej. `noc_header`). */
  data: WidgetData['data'] | null
  meta: WidgetDataMeta | null
  scale: WidgetScale
  /** Filas de la grilla que ocupa (para decidir cuántas filas de tabla caben). */
  rows: number
}

/** Filas de tabla/lista que caben: ≤ 8 en mural (§7.4), algo más en escritorio. */
export function maxListRows(scale: WidgetScale, gridRows: number) {
  return scale === 'wall'
    ? Math.min(8, Math.max(2, gridRows * 2 - 2))
    : Math.max(3, gridRows * 2 + 1)
}
