import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { ADMIN, fillCredentials, loginAsAdmin, loginAsNoc, setTheme } from './helpers'
import { NAV_SECTIONS, sectionHref } from '../../app/utils/navigation'

/**
 * Recorrido completo de la UI para revisión humana: una captura por pantalla y sección.
 * `pnpm exec playwright test --grep @tour` → docs/screenshots/tour/*.png. No forma parte del humo.
 */
const dir = resolve(import.meta.dirname, '../../docs/screenshots/tour')
mkdirSync(dir, { recursive: true })

const SLUG = 'fibra-norte'
const NOC_DASHBOARD = `/t/${SLUG}/dashboards/0192f000-0000-7000-8000-00000000d001`
const SECURITY_DASHBOARD = `/t/${SLUG}/dashboards/0192f000-0000-7000-8000-00000000d002`

async function prepare(page: Page, theme: 'light' | 'dark') {
  await setTheme(page, theme)
  await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' })
}

async function shot(page: Page, name: string, wait = 1200) {
  await page.waitForLoadState('load')
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${dir}/${name}.png` })
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
    for (const section of NAV_SECTIONS) {
      if (section.id === 'overview') continue
      await page.goto(sectionHref(section, SLUG))
      await shot(page, `${String(n).padStart(2, '0')}-${section.id}`)
      n++
    }

    await page.goto(NOC_DASHBOARD)
    await expect(page.locator('[data-testid="widget"]').first()).toBeVisible()
    await shot(page, `${n++}-dashboard-noc`, 6000)
    await page.goto(`${NOC_DASHBOARD}?scale=wall`)
    await shot(page, `${n++}-dashboard-noc-mural`, 6000)
    await page.goto(SECURITY_DASHBOARD)
    await expect(page.locator('[data-testid="widget"]').first()).toBeVisible()
    await shot(page, `${n++}-dashboard-seguridad`, 6000)
    await page.goto(`/t/${SLUG}/dev/widgets`)
    await shot(page, `${n++}-galeria-widgets`, 6000)
    await page.goto('/account')
    await shot(page, `${n++}-cuenta`)
    await page.goto('/t/isp-que-no-existe')
    await shot(page, `${n++}-isp-no-encontrado`)
  })

  test('vista del rol NOC y tema claro', async ({ page }) => {
    await prepare(page, 'light')
    await loginAsNoc(page)
    await shot(page, '40-noc-resumen-claro')
    await page.goto(SECURITY_DASHBOARD)
    await shot(page, '41-noc-dashboard-seguridad-claro', 6000)
    await page.goto(`/t/${SLUG}/clients`)
    await shot(page, '42-noc-sin-permiso-clientes')
  })

  test('móvil (390 px)', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await prepare(page, 'dark')
    await loginAsAdmin(page)
    await shot(page, '50-movil-resumen')
    await page.goto(NOC_DASHBOARD)
    await shot(page, '51-movil-dashboard-noc', 6000)
  })
})
