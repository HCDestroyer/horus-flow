import { expect, test, type Page } from '@playwright/test'
import { loginAsAdmin } from './helpers'
import { layoutReport } from './layout-audit'

/**
 * Sin desbordamiento (I0-16): en vista mural a varias resoluciones, en escritorio y en
 * móvil (320–430 px), ningún texto de un widget ni del encabezado se corta, se sale de su
 * contenedor o se solapa con otro. `pnpm exec playwright test --grep @layout`.
 */

const DASHBOARDS = {
  noc: { path: '/t/fibra-norte/dashboards/0192f000-0000-7000-8000-00000000d001', widgets: 9 },
  seguridad: { path: '/t/fibra-norte/dashboards/0192f000-0000-7000-8000-00000000d002', widgets: 7 },
}

const WALL = [
  [1280, 720],
  [1440, 900],
  [1920, 1080],
  [2560, 1440],
  [3840, 2160],
  // Proporciones menos habituales: 4:3 y ultrapanorámica.
  [1024, 768],
  [2560, 1080],
] as const

const MOBILE = [320, 360, 390, 430] as const

async function ready(page: Page, count: number) {
  await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(count)
  // Deja actuar a los observadores de ajuste (filas, elementos secundarios) y a ECharts.
  await page.waitForTimeout(400)
}

test.describe('sin desbordamiento @layout', () => {
  for (const [width, height] of WALL) {
    test(`vista mural ${width}×${height}`, async ({ page }) => {
      await page.setViewportSize({ width, height })
      await page.emulateMedia({ reducedMotion: 'reduce' })
      await loginAsAdmin(page)
      for (const [name, d] of Object.entries(DASHBOARDS)) {
        await page.goto(`${d.path}?scale=wall`)
        await ready(page, d.widgets)
        const problems = await layoutReport(page, { noScroll: true })
        expect(problems, `${name} mural ${width}×${height}`).toEqual([])
      }
    })
  }

  for (const width of MOBILE) {
    test(`móvil ${width} px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 })
      await page.emulateMedia({ reducedMotion: 'reduce' })
      await loginAsAdmin(page)
      for (const [name, d] of Object.entries(DASHBOARDS)) {
        await page.goto(d.path)
        await ready(page, d.widgets)
        const problems = await layoutReport(page, { extra: ['[data-testid="app-navbar"]'] })
        expect(problems, `${name} móvil ${width}px`).toEqual([])
      }
    })
  }

  for (const width of [1024, 1440, 1920]) {
    test(`escritorio ${width} px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await loginAsAdmin(page)
      for (const [name, d] of Object.entries(DASHBOARDS)) {
        await page.goto(d.path)
        await ready(page, d.widgets)
        const problems = await layoutReport(page, { extra: ['[data-testid="app-navbar"]'] })
        expect(problems, `${name} escritorio ${width}px`).toEqual([])
      }
    })
  }
})
