import AxeBuilder from '@axe-core/playwright'
import { expect, test } from '@playwright/test'
import { hydrated, scrollThrough } from './helpers'

const pages = ['/', '/en', '/comprar', '/en/buy', '/legal/privacidad']

for (const scheme of ['light', 'dark'] as const) {
  for (const path of pages) {
    test(`axe sin violaciones: ${path} (${scheme})`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' })
      await page.goto(path)
      await hydrated(page)
      await scrollThrough(page)
      const results = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice'])
        .analyze()
      const summary = results.violations.map((v) => ({
        id: v.id,
        impact: v.impact,
        nodes: v.nodes.slice(0, 3).map((n) => n.target.join(' ')),
      }))
      expect(summary).toEqual([])
    })
  }
}

test('teclado: el enlace para saltar al contenido es el primer foco', async ({ page }) => {
  await page.goto('/')
  await hydrated(page)
  await page.keyboard.press('Tab')
  const skip = page.getByRole('link', { name: 'Saltar al contenido' })
  await expect(skip).toBeFocused()
  await expect(skip).toBeInViewport()
})

test('objetivos táctiles de al menos 44 px en móvil', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  for (const path of ['/', '/comprar']) {
    await page.goto(path)
    await hydrated(page)
    const small = await page.evaluate(() => {
      const out: string[] = []
      for (const el of document.querySelectorAll<HTMLElement>(
        'header a, header button, main a, main button, main input:not([tabindex="-1"]):not(.sr-only), main textarea, footer a',
      )) {
        let r = el.getBoundingClientRect()
        if (r.width === 0 || r.height === 0) continue
        // Enlaces dentro de un texto (párrafo o etiqueta) quedan exentos (WCAG 2.5.8, "inline").
        if (el.tagName === 'A' && el.closest('p, label')) continue
        // Una casilla cuenta con su etiqueta: pulsar la etiqueta también la marca.
        const label =
          el.closest('[data-slot="item"]') ??
          (el.id ? document.querySelector(`label[for="${CSS.escape(el.id)}"]`) : null)
        if (label) {
          const l = label.getBoundingClientRect()
          const top = Math.min(r.top, l.top)
          const left = Math.min(r.left, l.left)
          r = new DOMRect(
            left,
            top,
            Math.max(r.right, l.right) - left,
            Math.max(r.bottom, l.bottom) - top,
          )
        }
        if (Math.round(r.height) < 44 || Math.round(r.width) < 44) {
          out.push(
            `${el.tagName} "${el.textContent?.trim()}" ${Math.round(r.width)}×${Math.round(r.height)}`,
          )
        }
      }
      return out
    })
    expect(small, path).toEqual([])
  }
})

test('sin desbordamiento horizontal a 320 px', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 700 })
  for (const path of ['/', '/comprar']) {
    await page.goto(path)
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow, path).toBeLessThanOrEqual(0)
  }
})

// El HTML del servidor también debe pasar axe: las secciones se hidratan en diferido y
// Lighthouse audita antes de que eso ocurra.
test.describe('sin los scripts de la app (HTML del servidor)', () => {
  for (const path of ['/', '/comprar']) {
    test(`axe sin violaciones: ${path}`, async ({ page }) => {
      // Sin los scripts de la app (axe sí necesita JavaScript en la página).
      await page.route(/\/_nuxt\/.*\.js$/, (r) => r.abort())
      await page.goto(path)
      const results = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'])
        .analyze()
      expect(results.violations.map((v) => `${v.id}: ${v.nodes[0]?.target.join(' ')}`)).toEqual([])
    })
  }
})
