/**
 * Mitad frontend del contrato de widget (C9, `presentation-manifest.schema.json`;
 * frontend.md §6.3). Cada `app/widgets/<type>/manifest.ts` exporta por defecto
 * `defineWidgetManifest({...})`.
 *
 * Se comprueba dos veces, ambas antes de ejecutar nada:
 * - **Tipos** (`pnpm typecheck`): `type` debe existir en el catálogo generado de
 *   `widget-types.json` y los tamaños por defecto deben estar en sus `sizes.allowed`.
 * - **Build** (`modules/widget-manifests.ts`): el manifiesto valida contra el esquema y el
 *   catálogo; si no, `nuxt generate` falla.
 *
 * Este archivo solo tiene imports de tipos: lo carga también el módulo de build.
 */
import type { WIDGET_CATALOG, WidgetTypeName } from '../../types/api/widget-catalog'

type CatalogEntry<T extends WidgetTypeName> = Extract<(typeof WIDGET_CATALOG)[number], { type: T }>
/** Tamaños permitidos por el catálogo para el tipo `T` (tipos literales). */
export type AllowedSize<T extends WidgetTypeName> = CatalogEntry<T>['sizes']['allowed'][number]

export interface GridSize {
  w: number
  h: number
}

export type WidgetCategory =
  'traffic' | 'customers' | 'security' | 'infrastructure' | 'platform' | 'layout'

export type WidgetPlaceholder =
  'kpi' | 'sparkline' | 'timeseries' | 'table' | 'list' | 'grid' | 'header' | 'bars'

export interface WidgetManifest<T extends WidgetTypeName = WidgetTypeName> {
  type: T
  sizes: {
    normal: { default: AllowedSize<T>; min: GridSize }
    wall: { default: AllowedSize<T>; min: GridSize }
  }
  /** Clave i18n; el texto empieza por verbo ("Muestra…"). */
  description: `widgets.${string}.description`
  category: WidgetCategory
  /** Overrides del formulario generado desde `config_schema` (I2). */
  configForm?: Record<string, unknown> | null
  /** Forma del esqueleto de carga. */
  placeholder: WidgetPlaceholder
  /** Variante mural (frontend.md §7.4): qué se oculta y qué se agranda. */
  wall: { hide: string[]; enlarge: string[] }
}

export function defineWidgetManifest<T extends WidgetTypeName>(manifest: WidgetManifest<T>) {
  return manifest
}
