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

/**
 * Cierra los huecos del layout diseñado: las plantillas del contrato dejan celdas vacías
 * (NOC: columna 12 de la fila 2 y fila 9 bajo "Tráfico 24 h"; Seguridad: varias). Cada
 * widget, en orden de lectura, se estira hacia la derecha, luego hacia abajo y luego hacia
 * la izquierda mientras toda la franja que ganaría esté vacía. Nunca se mueve ni se encoge
 * un widget, ni crece más allá de las filas del layout.
 */
export function fillGaps<T extends Pick<DashboardWidget, 'id' | 'position'>>(
  widgets: readonly T[],
  columns = 12,
): T[] {
  const rows = totalRows(widgets)
  const grid: (string | null)[][] = Array.from({ length: rows }, () =>
    Array<string | null>(columns).fill(null),
  )
  const out = readingOrder(widgets).map((w) => ({ ...w, position: { ...w.position } }))
  for (const { id, position: p } of out) {
    for (let r = p.y; r < p.y + p.h; r++) {
      for (let c = p.x; c < p.x + p.w; c++) if (grid[r]) grid[r]![c] = id
    }
  }
  const free = (r0: number, r1: number, c0: number, c1: number) => {
    if (r0 < 0 || c0 < 0 || r1 > rows || c1 > columns) return false
    for (let r = r0; r < r1; r++)
      for (let c = c0; c < c1; c++) if (grid[r]![c] !== null) return false
    return true
  }
  const claim = (id: string, r0: number, r1: number, c0: number, c1: number) => {
    for (let r = r0; r < r1; r++) for (let c = c0; c < c1; c++) grid[r]![c] = id
  }
  for (const { id, position: p } of out) {
    while (free(p.y, p.y + p.h, p.x + p.w, p.x + p.w + 1)) {
      claim(id, p.y, p.y + p.h, p.x + p.w, p.x + p.w + 1)
      p.w++
    }
    while (free(p.y + p.h, p.y + p.h + 1, p.x, p.x + p.w)) {
      claim(id, p.y + p.h, p.y + p.h + 1, p.x, p.x + p.w)
      p.h++
    }
    while (free(p.y, p.y + p.h, p.x - 1, p.x)) {
      claim(id, p.y, p.y + p.h, p.x - 1, p.x)
      p.x--
      p.w++
    }
  }
  return out
}

export function placeWidgets(widgets: readonly DashboardWidget[], mode: GridMode): PlacedWidget[] {
  const list = mode === 'designed' ? fillGaps(widgets) : readingOrder(widgets)
  return list.map((widget) => {
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
