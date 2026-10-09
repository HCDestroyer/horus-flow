import { expect, test } from '@playwright/test'
import { loginAsAdmin, loginAsNoc } from './helpers'

/** E2E de I0-15: selector de ISP, token por ISP y refresh (docs/backlog/increment-0.md). */

test.describe('selector de ISP @tenant', () => {
  test('con un solo ISP no hay selector: solo el nombre en la cabecera', async ({ page }) => {
    await loginAsNoc(page)
    await expect(page.getByTestId('tenant-header')).toContainText('Fibra Norte')
    await expect(page.getByTestId('tenant-switcher')).toHaveCount(0)
  })

  test('con 3 ISP: cambia el prefijo, conserva la sección y recuerda el último', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto('/t/fibra-norte/security/findings')
    await expect(page.getByRole('heading', { level: 1, name: 'Hallazgos' })).toBeVisible()

    await page.getByTestId('tenant-switcher').click()
    const options = page.getByRole('menuitemcheckbox')
    await expect(options).toHaveText([/Fibra Norte/, /Red Andina/, /Valle Conecta/])
    await page.getByRole('menuitemcheckbox', { name: 'Valle Conecta' }).click()

    await expect(page).toHaveURL(/\/t\/valle-conecta\/security\/findings$/)
    await expect(page.getByTestId('tenant-switcher')).toContainText('Valle Conecta')
    await expect(page).toHaveTitle('Hallazgos · Valle Conecta · Horus Flow')
    expect(await page.evaluate(() => localStorage.getItem('horus.lastTenant'))).toBe(
      'valle-conecta',
    )

    // Al volver a entrar se abre el último ISP usado.
    await page.goto('/')
    await expect(page).toHaveURL(/\/t\/valle-conecta$/)
  })

  test('si la sección no existe en el ISP nuevo, abre su inicio', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/t/fibra-norte/admin/users')
    await page.getByTestId('tenant-switcher').click()
    // En Red Andina es security_analyst: sin "Usuarios y roles".
    await page.getByRole('menuitemcheckbox', { name: 'Red Andina' }).click()
    await expect(page).toHaveURL(/\/t\/red-andina$/)
    await expect(
      page.getByTestId('main-nav').getByRole('link', { name: 'Usuarios y roles' }),
    ).toHaveCount(0)
  })

  test('cambiar de ISP desde la búsqueda global (⌘K)', async ({ page }) => {
    await loginAsAdmin(page)
    await page.keyboard.press('ControlOrMeta+k')
    await page.getByRole('option', { name: 'Red Andina' }).click()
    await expect(page).toHaveURL(/\/t\/red-andina$/)
  })

  test('una URL de un ISP sin acceso muestra "No encontrado"', async ({ page }) => {
    await loginAsNoc(page)
    await page.goto('/t/red-andina/security/findings')
    await expect(page.getByTestId('error-page')).toContainText('No encontrado')
    await expect(page.getByTestId('error-page')).not.toContainText('permiso')
  })
})

test.describe('sesión y refresh @tenant', () => {
  test('un 401 con refresh válido se repite de forma transparente', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('horus.mock.tokenTtlMs', '1500'))
    await loginAsNoc(page)
    await page.waitForTimeout(2000)
    await page.getByTestId('main-nav').getByRole('link', { name: 'Dashboards' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Dashboards' })).toBeVisible()
    await expect(page.getByTestId('dashboard-list')).toContainText('NOC del ISP')
    await expect(page).toHaveURL(/\/t\/fibra-norte\/dashboards$/)
  })

  test('si el refresh falla, vuelve al login conservando la ruta', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('horus.mock.tokenTtlMs', '1500'))
    await loginAsNoc(page)
    // La "cookie" de refresh del mock desaparece: la sesión terminó en el servidor.
    await page.evaluate(() => sessionStorage.removeItem('horus.mock.session'))
    await page.waitForTimeout(2000)
    await page.getByTestId('main-nav').getByRole('link', { name: 'Dashboards' }).click()
    await expect(page).toHaveURL(/\/login\?redirect=\/t\/fibra-norte\/dashboards/)
    await expect(page.getByTestId('login-error')).toContainText('Tu sesión terminó')
  })
})
