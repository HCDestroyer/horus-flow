// Capturas de la landing para la revisión de diseño (docs/design-review.md). Solo con
// `pnpm screenshots`. Página completa, JPEG para no inflar el repositorio.
import { test } from '@playwright/test'
import { hydrated, scrollThrough } from './helpers'

const widths = [1440, 768, 390]
const schemes = ['light', 'dark'] as const
const pages = [
  { name: 'inicio', path: '/' },
  { name: 'comprar', path: '/comprar?plan=medium' },
]

for (const p of pages) {
  for (const width of widths) {
    for (const scheme of schemes) {
      test(`@screenshots ${p.name} ${width} ${scheme}`, async ({ page }) => {
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
        await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' })
        await page.goto(p.path)
        await hydrated(page)
        await scrollThrough(page)
        await page.screenshot({
          path: `docs/screenshots/${p.name}-${width}-${scheme === 'light' ? 'claro' : 'oscuro'}.jpg`,
          fullPage: true,
          type: 'jpeg',
          quality: 72,
        })
      })
    }
  }
}
