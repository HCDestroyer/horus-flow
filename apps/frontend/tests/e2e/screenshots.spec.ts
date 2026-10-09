import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { ADMIN, fillCredentials, loginAsAdmin, loginAsNoc, setTheme } from './helpers'

/**
 * Capturas para revisión humana (DoD de I0-14: "capturas claro/oscuro adjuntas").
 * `pnpm screenshots` → docs/screenshots/*.png. No forman parte del humo @i0.
 */
const dir = resolve(import.meta.dirname, '../../docs/screenshots')
mkdirSync(dir, { recursive: true })

test.describe('capturas @screenshots', () => {
  test.use({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 })

  for (const theme of ['light', 'dark'] as const) {
    test(`login y layout (${theme})`, async ({ page }) => {
      await setTheme(page, theme)
      await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' })

      await page.goto('/login')
      await expect(page.getByTestId('login-card')).toBeVisible()
      await page.screenshot({ path: `${dir}/login-${theme}.png` })

      await fillCredentials(page, ADMIN)
      await expect(page.getByLabel('Código de verificación')).toBeVisible()
      await page.screenshot({ path: `${dir}/login-totp-${theme}.png` })

      await page.goto('/login')
      await loginAsAdmin(page)
      await expect(page.getByTestId('system-status')).toContainText('Funciona')
      await page.screenshot({ path: `${dir}/layout-resumen-${theme}.png` })
    })
  }

  test('layout móvil (320 px, claro)', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 720 })
    await setTheme(page, 'light')
    await page.emulateMedia({ colorScheme: 'light', reducedMotion: 'reduce' })
    await loginAsAdmin(page)
    await expect(page.getByTestId('system-status')).toContainText('Funciona')
    await page.screenshot({ path: `${dir}/layout-resumen-320-light.png`, fullPage: true })
  })
})

/** Capturas de I0-15 (selector de ISP) e I0-16 (dashboards y widgets). */
test.describe('capturas de ISP y dashboards @screenshots', () => {
  const NOC = '/t/fibra-norte/dashboards/0192f000-0000-7000-8000-00000000d001'
  const SECURITY = '/t/fibra-norte/dashboards/0192f000-0000-7000-8000-00000000d002'

  async function settle(page: Page, count: number) {
    await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(count)
    // Un dato en vivo (traffic.summary) y los gráficos ya pintados.
    await page.waitForTimeout(6000)
  }

  test('selector de ISP abierto (claro)', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await setTheme(page, 'light')
    await page.emulateMedia({ colorScheme: 'light', reducedMotion: 'reduce' })
    await loginAsAdmin(page)
    await expect(page.getByTestId('system-status')).toContainText('Funciona')
    await page.getByTestId('tenant-switcher').click()
    await expect(page.getByRole('menuitemcheckbox')).toHaveCount(3)
    await page.screenshot({ path: `${dir}/tenant-switcher-open-light.png` })
  })

  test('NOC del ISP en vista mural, oscuro, 1920×1080', async ({ page }) => {
    await page.setViewportSize({ width: 1920, height: 1080 })
    await setTheme(page, 'dark')
    await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' })
    await loginAsAdmin(page)
    await page.goto(`${NOC}?scale=wall`)
    await settle(page, 9)
    await page.screenshot({ path: `${dir}/dashboard-noc-wall-dark-1920.png` })
  })

  test('NOC del ISP en la app, oscuro, 1920×1080', async ({ page }) => {
    await page.setViewportSize({ width: 1920, height: 1080 })
    await setTheme(page, 'dark')
    await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' })
    await loginAsAdmin(page)
    await page.goto(NOC)
    await settle(page, 9)
    await page.screenshot({ path: `${dir}/dashboard-noc-dark-1920.png` })
  })

  test('Seguridad en la app, claro, 1920×1080', async ({ page }) => {
    await page.setViewportSize({ width: 1920, height: 1080 })
    await setTheme(page, 'light')
    await page.emulateMedia({ colorScheme: 'light', reducedMotion: 'reduce' })
    await loginAsAdmin(page)
    await page.goto(SECURITY)
    await settle(page, 7)
    await page.screenshot({ path: `${dir}/dashboard-security-light-1920.png` })
  })

  test('estados por widget (error, sin permiso, degradado), claro', async ({ page }) => {
    await page.setViewportSize({ width: 1920, height: 1080 })
    await setTheme(page, 'light')
    await page.emulateMedia({ colorScheme: 'light', reducedMotion: 'reduce' })
    await page.addInitScript(() => localStorage.setItem('horus.mock.scenario', 'degraded'))
    await loginAsNoc(page)
    await page.goto(NOC)
    await expect(page.locator('[data-widget-id="w-top-customers"]')).toHaveAttribute(
      'data-state',
      'forbidden',
    )
    await page.waitForTimeout(3000)
    // El panel hace scroll propio: se baja hasta los widgets degradado y sin permiso.
    await page.evaluate(() =>
      document
        .querySelector('[data-widget-id="w-top-customers"]')
        ?.scrollIntoView({ block: 'end' }),
    )
    await page.waitForTimeout(300)
    await page.screenshot({ path: `${dir}/dashboard-noc-states-light.png` })
  })

  for (const scale of ['normal', 'wall'] as const) {
    test(`galería de widgets, escala ${scale}`, async ({ page }) => {
      await page.setViewportSize({ width: 1920, height: 1080 })
      const theme = scale === 'wall' ? 'dark' : 'light'
      await setTheme(page, theme)
      await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' })
      await loginAsAdmin(page)
      await page.goto(`/t/fibra-norte/dev/widgets${scale === 'wall' ? '?scale=wall' : ''}`)
      await expect(page.getByTestId('dev-widget-watched_ports')).toBeVisible()
      await page.waitForTimeout(5000)
      await page.screenshot({ path: `${dir}/widgets-gallery-${scale}.png`, fullPage: true })
    })
  }
})
