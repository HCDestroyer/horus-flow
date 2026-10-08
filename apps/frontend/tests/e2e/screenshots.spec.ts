import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, test } from '@playwright/test'
import { ADMIN, fillCredentials, loginAsAdmin, setTheme } from './helpers'

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
