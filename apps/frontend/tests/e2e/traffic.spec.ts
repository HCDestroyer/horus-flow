import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/** I1-17 · Tráfico: tops y series. `pnpm e2e --grep @traffic`. */

const PAGE = '/t/fibra-norte/traffic'

test.describe('tráfico @traffic', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('cambiar rango y nodo recalcula todo y queda en la URL (sin IPs)', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(PAGE)
    const widgets = page.locator('[data-testid="widget"]')
    await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(5)
    await expect(page.getByTestId('traffic-coverage')).toContainText('atribuidos a clientes')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('traffic-range').click()
    await page.getByRole('option', { name: 'Últimos 7 días' }).click()
    await expect(page).toHaveURL(/range=7d/)
    await page.getByTestId('traffic-site').click()
    await page.getByRole('option', { name: 'Nodo Norte' }).click()
    await expect(page).toHaveURL(/site=/)
    await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(5)
    await expect(widgets.first()).toContainText('7d')
    expect(new URL(page.url()).search).not.toMatch(/\d+\.\d+\.\d+\.\d+/)
    // Al recargar, el estado sale de la URL.
    await page.reload()
    await expect(page.getByTestId('traffic-range')).toContainText('Últimos 7 días')
  })

  test('meta.partial muestra "Datos incompletos en este rango"', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(`${PAGE}?range=30d`)
    await expect(page.getByTestId('traffic-partial')).toContainText(
      'Datos incompletos en este rango',
    )
  })

  test('analítica no disponible: aviso de degradado y el resto de la app funciona', async ({
    page,
  }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.scenario', 'degraded'))
    await loginAsAdmin(page)
    await page.goto(PAGE)
    await expect(page.getByTestId('degraded-notice')).toContainText('Analítica no disponible')
    await expect(
      page.locator('[data-testid="widget"][data-state="degraded"]').first(),
    ).toBeVisible()
    // La navegación y otras secciones siguen funcionando.
    await page.getByTestId('main-nav').getByRole('link', { name: 'Hallazgos' }).click()
    await expect(page.getByTestId('findings-table')).toBeVisible()
  })
})
