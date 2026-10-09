/**
 * Validación de manifiestos de presentación (C9) contra
 * `packages/schemas/dashboard/v0/presentation-manifest.schema.json` y el catálogo
 * `widget-types.json`. La usan el módulo de build (`modules/widget-manifests.ts`) y los
 * tests. Imports relativos: se carga fuera de Nuxt (jiti) en el build.
 */
import * as z from 'zod'
import { WIDGET_CATALOG } from '../../types/api/widget-catalog'

const size = z.strictObject({ w: z.int().min(1).max(12), h: z.int().min(1).max(12) })
const sizeSet = z.strictObject({ default: size, min: size })

/** Espejo de presentation-manifest.schema.json (additionalProperties: false). */
export const manifestSchema = z.strictObject({
  type: z.string().regex(/^[a-z][a-z0-9_]*$/),
  sizes: z.strictObject({ normal: sizeSet, wall: sizeSet }),
  description: z.string().regex(/^widgets\.[a-z0-9_]+\.description$/),
  category: z.enum(['traffic', 'customers', 'security', 'infrastructure', 'platform', 'layout']),
  configForm: z.record(z.string(), z.unknown()).nullable().optional(),
  placeholder: z.enum([
    'kpi',
    'sparkline',
    'timeseries',
    'table',
    'list',
    'grid',
    'header',
    'bars',
  ]),
  wall: z.strictObject({ hide: z.array(z.string()), enlarge: z.array(z.string()) }),
})

export interface CatalogLike {
  type: string
  sizes: { allowed: readonly { w: number; h: number }[] }
}

/**
 * Errores de un manifiesto (vacío = válido). `folder` es el nombre de su carpeta, que
 * debe coincidir con `type` (el registro se genera por convención de carpeta).
 */
export function validateManifest(
  manifest: unknown,
  folder: string,
  catalog: readonly CatalogLike[] = WIDGET_CATALOG,
): string[] {
  const parsed = manifestSchema.safeParse(manifest)
  if (!parsed.success) {
    return parsed.error.issues.map(
      (i) => `${folder}: ${i.path.join('.') || '(raíz)'}: ${i.message}`,
    )
  }
  const m = parsed.data
  const errors: string[] = []
  if (m.type !== folder) errors.push(`${folder}: type "${m.type}" no coincide con la carpeta`)
  const entry = catalog.find((t) => t.type === m.type)
  if (!entry) {
    errors.push(`${folder}: el tipo "${m.type}" no existe en widget-types.json`)
    return errors
  }
  const allowed = (s: { w: number; h: number }) =>
    entry.sizes.allowed.some((a) => a.w === s.w && a.h === s.h)
  for (const scale of ['normal', 'wall'] as const) {
    const set = m.sizes[scale]
    if (!allowed(set.default)) {
      errors.push(
        `${folder}: sizes.${scale}.default ${set.default.w}×${set.default.h} no está en sizes.allowed del catálogo`,
      )
    }
    if (set.min.w > set.default.w || set.min.h > set.default.h) {
      errors.push(`${folder}: sizes.${scale}.min es mayor que default`)
    }
  }
  if (m.description !== `widgets.${m.type}.description`) {
    errors.push(`${folder}: description debe ser widgets.${m.type}.description`)
  }
  return errors
}
