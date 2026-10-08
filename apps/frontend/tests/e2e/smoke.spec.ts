import { expect, test } from '@playwright/test'
import {
  ADMIN,
  fillCredentials,
  loginAsAdmin,
  loginAsNoc,
  seriousViolations,
  setTheme,
  TOTP,
} from './helpers'

/** E2E de humo de I0-14 (criterios de docs/backlog/increment-0.md). */

test.describe('login @i0', () => {
  test('sin sesión, cualquier ruta lleva al login', async ({ page }) => {
    await page.goto('/t/fibra-norte')
    await expect(page).toHaveURL(/\/login\?redirect=/)
    await expect(page.getByRole('heading', { name: 'Iniciar sesión' })).toBeVisible()
  })

  test('credenciales válidas sin 2FA llevan al Resumen con el layout', async ({ page }) => {
    await loginAsNoc(page)
    await expect(page.getByRole('heading', { level: 1, name: 'Resumen' })).toBeVisible()
    await expect(page.getByTestId('tenant-header')).toContainText('Fibra Norte')
    const nav = page.getByTestId('main-nav')
    await expect(nav.getByRole('link', { name: 'Nodos y routers' })).toBeVisible()
    await expect(page.getByTestId('user-menu')).toContainText('Luis Prieto')
    await expect(page.getByTestId('system-status')).toContainText('Todo funciona con normalidad')
    await expect(page).toHaveTitle('Resumen · Fibra Norte · Horus Flow')
  })

  test('credenciales inválidas: error en contexto sin revelar si el usuario existe', async ({
    page,
  }) => {
    await page.goto('/login')
    await fillCredentials(page, ADMIN, 'contraseña-incorrecta')
    const error = page.getByTestId('login-error')
    await expect(error).toBeVisible()
    const existing = await error.textContent()

    await fillCredentials(page, 'nadie@ejemplo.example', 'contraseña-incorrecta')
    await expect(error).toHaveText(existing ?? '')
    await expect(error).toContainText('Correo o contraseña incorrectos')
    await expect(page).toHaveURL(/\/login/)
  })

  test('con 2FA pide el código; uno erróneo da error y el correcto entra', async ({ page }) => {
    await page.goto('/login')
    await fillCredentials(page, ADMIN)
    await expect(page.getByRole('heading', { name: 'Verificación en dos pasos' })).toBeVisible()
    await page.getByLabel('Código de verificación').fill('000000')
    await page.getByTestId('mfa-submit').click()
    await expect(page.getByTestId('login-error')).toContainText('El código no es correcto')
    await page.getByLabel('Código de verificación').fill(TOTP)
    await page.getByTestId('mfa-submit').click()
    await expect(page).toHaveURL(/\/t\/fibra-norte$/)
    await expect(page.getByRole('heading', { level: 1, name: 'Resumen' })).toBeVisible()
  })

  test('cerrar sesión vuelve al login', async ({ page }) => {
    await loginAsNoc(page)
    await page.getByTestId('user-menu').click()
    await page.getByRole('menuitem', { name: 'Cerrar sesión' }).click()
    await expect(page).toHaveURL(/\/login$/)
    await page.goto('/t/fibra-norte')
    await expect(page).toHaveURL(/\/login/)
  })
})

test.describe('layout @i0', () => {
  test('sin un permiso, la sección se oculta (no se deshabilita)', async ({ page }) => {
    await loginAsNoc(page)
    const nav = page.getByTestId('main-nav')
    await expect(nav.getByRole('link', { name: 'Hallazgos' })).toBeVisible()
    // El rol noc no tiene customers.read, kiosks.manage ni permisos de plataforma.
    await expect(nav.getByRole('link', { name: 'Clientes' })).toHaveCount(0)
    await expect(nav.getByRole('link', { name: 'Pantallas NOC' })).toHaveCount(0)
    await expect(nav.getByText('Plataforma')).toHaveCount(0)
    // La URL directa no muestra la sección.
    await page.goto('/t/fibra-norte/clients')
    await expect(page.getByTestId('error-page')).toContainText('No tienes acceso')
  })

  test('la administradora ve Clientes y la consola de plataforma', async ({ page }) => {
    await loginAsAdmin(page)
    const nav = page.getByTestId('main-nav')
    await expect(nav.getByRole('link', { name: 'Clientes' })).toBeVisible()
    await expect(nav.getByRole('link', { name: 'Estado del sistema' })).toBeVisible()
    await nav.getByRole('link', { name: 'Clientes' }).click()
    await expect(page).toHaveURL(/\/t\/fibra-norte\/clients$/)
    await expect(page.getByRole('heading', { level: 1, name: 'Clientes' })).toBeVisible()
  })

  test('un ISP ajeno muestra "No encontrado"', async ({ page }) => {
    await loginAsNoc(page)
    await page.goto('/t/valle-conecta')
    await expect(page.getByTestId('error-page')).toContainText('No encontrado')
  })

  test('el error del estado del sistema se muestra en contexto con Reintentar', async ({
    page,
  }) => {
    await page.addInitScript(() => localStorage.setItem('horus.mock.scenario', 'system-error'))
    await loginAsNoc(page)
    const card = page.getByTestId('system-status')
    await expect(card.getByRole('alert')).toContainText('No pudimos consultar el estado')
    await expect(card.getByRole('button', { name: 'Reintentar' })).toBeVisible()
    // El resto del layout sigue funcionando.
    await expect(page.getByTestId('main-nav')).toBeVisible()
  })

  test('a 320 px no hay scroll horizontal de página', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 640 })
    await loginAsNoc(page)
    await expect(page.getByRole('heading', { level: 1, name: 'Resumen' })).toBeVisible()
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow).toBeLessThanOrEqual(0)
  })
})

test.describe('tema y accesibilidad @i0', () => {
  for (const theme of ['system', 'light', 'dark'] as const) {
    test(`tema ${theme}: tokens aplicados y sin violaciones serias de axe`, async ({ page }) => {
      await setTheme(page, theme)
      await page.goto('/login')
      const expected = theme === 'dark' ? 'dark' : 'light' // "system" sigue al navegador (claro)
      await expect(page.locator('html')).toHaveClass(new RegExp(`\\b${expected}\\b`))
      expect(await seriousViolations(page)).toEqual([])

      await loginAsNoc(page)
      await expect(page.getByTestId('system-status')).toContainText('Funciona')
      const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor)
      if (expected === 'dark') expect(bg).not.toBe('rgb(255, 255, 255)')
      else expect(bg).toBe('rgb(255, 255, 255)')
      expect(await seriousViolations(page)).toEqual([])
    })
  }

  test('el menú de usuario cambia el tema en caliente', async ({ page }) => {
    await setTheme(page, 'light')
    await loginAsNoc(page)
    await page.getByTestId('user-menu').click()
    await page.getByRole('menuitemcheckbox', { name: 'Oscuro' }).click()
    await expect(page.locator('html')).toHaveClass(/\bdark\b/)
    await page.getByRole('menuitemcheckbox', { name: 'Claro' }).click()
    await expect(page.locator('html')).toHaveClass(/\blight\b/)
  })
})
