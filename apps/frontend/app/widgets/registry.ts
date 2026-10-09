import { defineAsyncComponent, type Component } from 'vue'
import type { WidgetData } from '~~/types/api'
import { WIDGET_CATALOG } from '~~/types/api/widget-catalog'
import type { WidgetManifest } from './define'

/**
 * Registro de tipos de widget (frontend.md §6.3, §16): se genera por convención de carpeta,
 * `app/widgets/<type>/{manifest.ts, Widget.vue}`. Los manifiestos se cargan al inicio (son
 * pequeños) y los componentes bajo demanda (ECharts va en esos chunks, no en el inicial).
 * Las carpetas `_*` son plantillas (no se registran). Un widget no importa otro widget.
 */

interface ManifestModule {
  default: WidgetManifest
  /** Opcional: cuándo los datos cuentan como "vacío" (si no, la regla genérica). */
  isEmpty?: (data: WidgetData['data']) => boolean
}

const manifests = import.meta.glob<ManifestModule>('./[a-z]*/manifest.ts', { eager: true })
const components = import.meta.glob<Component>('./[a-z]*/Widget.vue', { import: 'default' })

export interface RegisteredWidget {
  manifest: WidgetManifest
  catalog: (typeof WIDGET_CATALOG)[number]
  component: Component
  isEmpty?: ManifestModule['isEmpty']
}

const registry = new Map<string, RegisteredWidget>()
for (const [path, mod] of Object.entries(manifests)) {
  const folder = path.split('/')[1]!
  const loader = components[`./${folder}/Widget.vue`]
  const catalog = WIDGET_CATALOG.find((t) => t.type === mod.default.type)
  // El build ya garantiza ambos (modules/widget-manifests.ts); aquí solo se ignoran.
  if (!loader || !catalog) continue
  registry.set(mod.default.type, {
    manifest: mod.default,
    catalog,
    component: defineAsyncComponent(loader),
    isEmpty: mod.isEmpty,
  })
}

/** Renderizador de un tipo, o `undefined` → "Widget no disponible en esta versión". */
export function widgetFor(type: string): RegisteredWidget | undefined {
  return registry.get(type)
}

export function registeredTypes() {
  return [...registry.keys()].sort()
}

/** Regla genérica de "vacío" por tipo de datos (frontend.md §9.2). */
export function isEmptyData(data: WidgetData['data']): boolean {
  if (data.kind === 'table') return data.rows.length === 0
  if (data.kind === 'series') {
    return data.series.every((s) => s.points.every(([, v]) => v === null))
  }
  const leaves = (v: unknown): unknown[] =>
    v && typeof v === 'object' ? Object.values(v).flatMap(leaves) : [v]
  return leaves(data.values).every((v) => v === null || v === 0 || v === '')
}
