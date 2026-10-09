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

/** Pantallas de I1 (I1-16…I1-21, I1-31): sin cortes ni solapes en escritorio y en móvil. */
const I1_SCREENS = [
  '/t/fibra-norte/clients',
  '/t/fibra-norte/clients/0193c000-0000-7000-8000-000000100001',
  '/t/fibra-norte/clients/0193c000-0000-7000-8000-000000100040',
  '/t/fibra-norte/traffic',
  '/t/fibra-norte/security/findings',
  '/t/fibra-norte/security/findings/0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f00',
  '/t/fibra-norte/nodes',
  '/t/fibra-norte/nodes/0192e111-0000-7000-8000-000000000105',
  '/t/fibra-norte/routers/0192e333-0000-7000-8000-000000000106/connection',
  '/t/fibra-norte/routers/0192e333-0000-7000-8000-000000000101/prefixes',
  '/t/fibra-norte/admin/kiosks',
  '/t/fibra-norte/admin/notifications',
  '/platform/isps',
  '/platform/wireguard',
  '/platform/storage',
  '/platform/system',
  '/platform/reputation',
]

test.describe('pantallas de I1 sin desbordamiento @layout', () => {
  for (const width of [320, 390, 1440]) {
    test(`pantallas de I1 a ${width} px`, async ({ page }) => {
      test.setTimeout(180_000)
      await page.setViewportSize({ width, height: 900 })
      await page.emulateMedia({ reducedMotion: 'reduce' })
      await page.addInitScript(() => window.localStorage.setItem('horus.mock.findingEveryMs', '0'))
      await loginAsAdmin(page)
      for (const path of I1_SCREENS) {
        await page.goto(path)
        await page.waitForLoadState('load')
        await expect(page.locator('#main-content')).toBeVisible()
        await page.waitForTimeout(1500)
        const problems = await layoutReport(page, {
          extra: ['[data-testid="app-navbar"]', '#main-content'],
        })
        expect(problems, `${path} a ${width}px`).toEqual([])
      }
    })
  }
})
