// Rendimiento: la hoja de estilos principal (≈ 12 KB comprimida) va dentro del HTML en vez de
// como <link>: el primer pintado no espera a otra petición (Lighthouse móvil ≥ 90). El HTML se
// sirve comprimido (plugins/compression.ts). Si no encuentra el archivo, deja el <link>.
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'

const LINK_RE = /<link rel="stylesheet" href="\/_nuxt\/(entry\.[\w-]+\.css)"[^>]*>/g
const cache = new Map<string, string | null>()

function publicDir(): string {
  // .output/server/index.mjs → .output/public
  return join(dirname(process.argv[1] ?? ''), '..', 'public')
}

function readCss(file: string): string | null {
  if (!cache.has(file)) {
    try {
      cache.set(file, readFileSync(join(publicDir(), '_nuxt', file), 'utf8'))
    } catch {
      cache.set(file, null)
    }
  }
  return cache.get(file) ?? null
}

export default defineNitroPlugin((nitro) => {
  if (import.meta.dev) return
  nitro.hooks.hook('render:html', (html) => {
    html.head = html.head.map((chunk) =>
      chunk.replace(LINK_RE, (tag, file: string) => {
        const css = readCss(file)
        return css ? `<style>${css.replaceAll('</style', '<\\/style')}</style>` : tag
      }),
    )
  })
})
