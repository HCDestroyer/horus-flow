import { existsSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { defineNuxtModule, useLogger } from 'nuxt/kit'
import { validateManifest } from '../app/widgets/validate'

/**
 * Valida los manifiestos de presentación de widgets (C9) al construir (I0-16, criterio 1):
 * un manifiesto inválido, de un tipo que no existe en `widget-types.json`, con tamaños fuera
 * del catálogo o sin `Widget.vue` hace fallar `nuxt dev`/`nuxt generate`, no la ejecución.
 * Las carpetas que empiezan por `_` (p. ej. `_example`) son plantillas y no se registran.
 */
export default defineNuxtModule({
  meta: { name: 'horus-widget-manifests' },
  async setup(_options, nuxt) {
    const logger = useLogger('widgets')
    const dir = join(nuxt.options.srcDir, 'widgets')
    const folders = readdirSync(dir, { withFileTypes: true })
      .filter((d) => d.isDirectory() && /^[a-z]/.test(d.name))
      .map((d) => d.name)
      .sort()

    const errors: string[] = []
    for (const folder of folders) {
      const manifestPath = join(dir, folder, 'manifest.ts')
      if (!existsSync(manifestPath)) {
        errors.push(`${folder}: falta manifest.ts`)
        continue
      }
      if (!existsSync(join(dir, folder, 'Widget.vue'))) errors.push(`${folder}: falta Widget.vue`)
      const mod = (await import(pathToFileURL(manifestPath).href)) as { default?: unknown }
      errors.push(...validateManifest(mod.default, folder))
    }

    if (errors.length > 0) {
      throw new Error(`Manifiestos de widget inválidos (C9):\n  - ${errors.join('\n  - ')}`)
    }
    logger.info(`${folders.length} widgets validados contra widget-types.json`)
  },
})
