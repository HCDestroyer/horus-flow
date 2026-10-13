import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// acorn llega como dependencia de las herramientas (pnpm lo deja en .pnpm/node_modules).
const require = createRequire(import.meta.url)
const acorn = require(
  require.resolve('acorn', { paths: [join(process.cwd(), 'node_modules/.pnpm/node_modules')] }),
) as { parse: (src: string, opts: { ecmaVersion: number }) => unknown }

const root = join(process.cwd(), 'public')
/** Código sin comentarios (los comentarios explican qué se evita y lo nombran). */
const code = (src: string) => src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')

describe('pantalla NOC ligera (TVs antiguas)', () => {
  it('kiosk-lite.js es ES5 puro: sin módulos, flechas, const/let ni comas finales en llamadas', () => {
    const src = readFileSync(join(root, 'kiosk-lite.js'), 'utf8')
    expect(() => acorn.parse(src, { ecmaVersion: 5 })).not.toThrow()
    expect(code(src)).not.toMatch(/\bfetch\(|\bPromise\b|=>/)
  })

  it('kiosk-lite.css no usa CSS moderno que ignoran los navegadores de TV antiguos', () => {
    const css = readFileSync(join(root, 'kiosk-lite.css'), 'utf8')
    expect(code(css)).not.toMatch(/@layer|oklch|color-mix|:has\(|@container|\binset:/)
  })

  it('el script de la pantalla de arranque (spa-loading-template) también es ES5', () => {
    const html = readFileSync(join(process.cwd(), 'app/spa-loading-template.html'), 'utf8')
    const script = html.match(/<script>([\s\S]*?)<\/script>/)?.[1] ?? ''
    expect(script).toContain('kiosk-lite.html')
    expect(() => acorn.parse(script, { ecmaVersion: 5 })).not.toThrow()
  })

  it('la página carga el script clásico, no un módulo', () => {
    const html = readFileSync(join(root, 'kiosk-lite.html'), 'utf8')
    expect(html).toContain('<script src="/kiosk-lite.js"></script>')
    expect(html).not.toContain('type="module"')
  })
})
