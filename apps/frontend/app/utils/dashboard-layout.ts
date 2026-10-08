import type { InjectionKey, Ref } from 'vue'
import type { Dashboard, DashboardWidget } from '~~/types/api'

/**
 * Grilla de 12 columnas del dashboard (frontend.md §5.2, §6.4) y contexto compartido por
 * sus widgets.
 */

export type WidgetScale = 'normal' | 'wall'

/**
 * Modo de la grilla según el ancho disponible del CONTENEDOR (no del viewport: la barra
 * lateral puede ocupar 64–350 px). Equivalencias con §5.2 con la barra expandida:
 * - `designed` (≥ 1100 px ≈ viewport ≥ 1440): posiciones tal como se diseñaron.
 * - `medium` (≥ 860 px ≈ 1024–1439): los widgets de 3 columnas pasan a 4; flujo denso.
 * - `narrow` (≥ 560 px ≈ 768–1023): widgets a 6 o 12 columnas.
 * - `single` (< 560 px): una columna en orden de lectura (izquierda→derecha, arriba→abajo).
 */
export type GridMode = 'designed' | 'medium' | 'narrow' | 'single'

export function gridModeFor(width: number): GridMode {
  if (width >= 1100) return 'designed'
  if (width >= 860) return 'medium'
  if (width >= 560) return 'narrow'
  return 'single'
}

/** Orden de lectura: arriba→abajo, izquierda→derecha (también el orden en el DOM). */
export function readingOrder<T extends Pick<DashboardWidget, 'position'>>(widgets: readonly T[]) {
  return [...widgets].sort((a, b) => a.position.y - b.position.y || a.position.x - b.position.x)
}

export interface PlacedWidget {
  widget: DashboardWidget
  style: Record<string, string>
}

export function placeWidgets(widgets: readonly DashboardWidget[], mode: GridMode): PlacedWidget[] {
  return readingOrder(widgets).map((widget) => {
    const { x, y, w, h } = widget.position
    let style: Record<string, string>
    switch (mode) {
      case 'designed':
        style = { gridColumn: `${x + 1} / span ${w}`, gridRow: `${y + 1} / span ${h}` }
        break
      case 'medium':
        style = { gridColumn: `span ${w === 3 ? 4 : w}`, gridRow: `span ${h}` }
        break
      case 'narrow':
        style = { gridColumn: `span ${w <= 6 ? 6 : 12}`, gridRow: `span ${h}` }
        break
      default:
        style = { gridColumn: '1 / -1', gridRow: `span ${h}` }
    }
    return { widget, style }
  })
}

/** Filas que ocupa el layout diseñado (para repartir la altura en mural). */
export function totalRows(widgets: readonly DashboardWidget[]) {
  return widgets.reduce((max, w) => Math.max(max, w.position.y + w.position.h), 0)
}

export interface DashboardContext {
  dashboard: Dashboard
  tenantName: Ref<string>
  scale: Ref<WidgetScale>
  /** Instante (ms) del dato más reciente recibido por cualquier widget. */
  lastUpdate: Ref<number | null>
  report: (at: number) => void
  /** Hay una fuente de tiempo real activa. */
  live: Ref<boolean>
}

export const DASHBOARD_CONTEXT: InjectionKey<DashboardContext> = Symbol('horus.dashboard')
