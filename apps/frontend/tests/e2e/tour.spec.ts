import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { ADMIN, fillCredentials, loginAsAdmin, loginAsNoc, setTheme } from './helpers'
import { layoutReport } from './layout-audit'
import {
  DEFAULT_NAV_INCREMENT,
  isIncrementVisible,
  NAV_SECTIONS,
  sectionHref,
} from '../../app/utils/navigation'

/**
 * Recorrido completo de la UI para revisión humana: una captura por pantalla y sección.
 * `pnpm exec playwright test --grep @tour` → docs/screenshots/tour/*.png. No forma parte del humo.
 * Cada captura de dashboard pasa además la auditoría "sin desbordamiento" (ver @layout).
 */
const dir = resolve(import.meta.dirname, '../../docs/screenshots/tour')
mkdirSync(dir, { recursive: true })

const SLUG = 'fibra-norte'
const NOC_DASHBOARD = `/t/${SLUG}/dashboards/0192f000-0000-7000-8000-00000000d001`
const SECURITY_DASHBOARD = `/t/${SLUG}/dashboards/0192f000-0000-7000-8000-00000000d002`

/** Secciones del incremento entregado (las futuras no existen en la UI: dan 404). */
const SECTIONS = NAV_SECTIONS.filter(
  (s) => s.id !== 'overview' && isIncrementVisible(s.increment, DEFAULT_NAV_INCREMENT),
)
const FUTURE = NAV_SECTIONS.find((s) => !isIncrementVisible(s.increment, DEFAULT_NAV_INCREMENT))!

async function prepare(page: Page, theme: 'light' | 'dark') {
  await setTheme(page, theme)
  await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' })
}

async function shot(page: Page, name: string, wait = 1200) {
  await page.waitForLoadState('load')
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${dir}/${name}.png` })
}

/** Dashboard listo (todos sus widgets con datos o sin datos propios) y sin desbordamientos. */
async function dashboardShot(page: Page, name: string, widgets: number, wall = false) {
  await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(widgets)
  await shot(page, name, 3000)
  const extra = wall ? [] : ['[data-testid="app-navbar"]']
  expect(await layoutReport(page, { extra, noScroll: wall }), name).toEqual([])
}

test.describe('recorrido completo @tour', () => {
  test.describe.configure({ mode: 'serial', timeout: 300_000 })
  test.use({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 })

  test('acceso', async ({ page }) => {
    await prepare(page, 'dark')
    await page.goto('/login')
    await expect(page.getByTestId('login-card')).toBeVisible()
    await shot(page, '01-login')
    await fillCredentials(page, ADMIN)
    await expect(page.getByLabel('Código de verificación')).toBeVisible()
    await shot(page, '02-login-totp')
  })

  test('secciones del ISP y de plataforma (administradora)', async ({ page }) => {
    await prepare(page, 'dark')
    await loginAsAdmin(page)
    await shot(page, '03-resumen')

    let n = 4
    const next = () => String(n++).padStart(2, '0')
    for (const section of SECTIONS) {
      await page.goto(sectionHref(section, SLUG))
      await shot(page, `${next()}-${section.id}`)
    }
    // Una sección de un incremento posterior: fuera del menú y su URL da "No encontrado".
    await page.goto(sectionHref(FUTURE, SLUG))
    await expect(page.getByTestId('error-page')).toContainText('No encontrado')
    await shot(page, `${next()}-seccion-futura-${FUTURE.id}-no-encontrada`)

    await page.goto(NOC_DASHBOARD)
    await dashboardShot(page, `${next()}-dashboard-noc`, 9)
    await page.goto(`${NOC_DASHBOARD}?scale=wall`)
    await dashboardShot(page, `${next()}-dashboard-noc-mural`, 9, true)
    await page.goto(SECURITY_DASHBOARD)
    await dashboardShot(page, `${next()}-dashboard-seguridad`, 7)
    await page.goto(`/t/${SLUG}/dev/widgets`)
    await shot(page, `${next()}-galeria-widgets`, 6000)
    await page.goto('/account')
    await shot(page, `${next()}-cuenta`)
    await page.goto('/t/isp-que-no-existe')
    await shot(page, `${next()}-isp-no-encontrado`)
  })

  test('vista mural a varias resoluciones', async ({ page }) => {
    await prepare(page, 'dark')
    await loginAsAdmin(page)
    const sizes = [
      [1280, 720],
      [1920, 1080],
      [3840, 2160],
    ] as const
    let n = 30
    for (const [width, height] of sizes) {
      await page.setViewportSize({ width, height })
      await page.goto(`${NOC_DASHBOARD}?scale=wall`)
      await dashboardShot(page, `${n++}-mural-noc-${width}x${height}`, 9, true)
      await page.goto(`${SECURITY_DASHBOARD}?scale=wall`)
      await dashboardShot(page, `${n++}-mural-seguridad-${width}x${height}`, 7, true)
    }
  })

  test('vista del rol NOC y tema claro', async ({ page }) => {
    await prepare(page, 'light')
    await loginAsNoc(page)
    await shot(page, '40-noc-resumen-claro')
    await page.goto(SECURITY_DASHBOARD)
    await dashboardShot(page, '41-noc-dashboard-seguridad-claro', 7)
    await page.goto(`/t/${SLUG}/clients`)
    await shot(page, '42-noc-sin-permiso-clientes')
  })

  test('móvil (390 y 320 px)', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await prepare(page, 'dark')
    await loginAsAdmin(page)
    await shot(page, '50-movil-resumen')
    await page.goto(NOC_DASHBOARD)
    await dashboardShot(page, '51-movil-dashboard-noc', 9)
    await page.locator('[data-widget-id="w-exporters"]').scrollIntoViewIfNeeded()
    await shot(page, '55-movil-dashboard-noc-exportadores', 500)
    await page.goto(SECURITY_DASHBOARD)
    await dashboardShot(page, '52-movil-dashboard-seguridad', 7)
    await page.setViewportSize({ width: 320, height: 720 })
    await page.goto(NOC_DASHBOARD)
    await dashboardShot(page, '53-movil-320-dashboard-noc', 9)
    await page.goto(SECURITY_DASHBOARD)
    await dashboardShot(page, '54-movil-320-dashboard-seguridad', 7)
  })
})
