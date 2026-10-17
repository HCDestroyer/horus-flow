import { expect, test } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, seriousViolations } from './helpers'

/**
 * Reportes (`/t/:slug/reports`, rol reporting, I3): lista con estados, descarga autenticada,
 * «Generar ahora» (en cola → listo) y programación semanal. `pnpm e2e --grep @reports`.
 */
const T = '/t/fibra-norte'

test.describe('reportes @reports', () => {
  test.describe.configure({ timeout: 90_000 })
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.findingEveryMs', '0'))
  })

  test('lista, descarga, genera a petición y guarda la programación', async ({ page }) => {
    await loginAsAdmin(page)
    await page.getByTestId('main-nav').getByRole('link', { name: 'Reportes' }).click()
    await expect(page).toHaveURL(/\/reports$/)
    await expect(page.getByRole('heading', { level: 1, name: 'Reportes' })).toBeVisible()

    const rows = page.getByTestId('report-row')
    await expect(rows.first()).toBeVisible()
    const failed = page.locator('[data-testid="report-row"][data-status="failed"]')
    await expect(failed.getByTestId('report-status')).toContainText('Error')
    await expect(failed.getByTestId('report-error')).toContainText('No se pudo generar')
    const done = page.locator('[data-testid="report-row"][data-status="completed"]').first()
    await expect(done.getByTestId('report-status')).toContainText('Listo')
    await expect(done).toContainText(/Semana \d+ de 2026/)

    const download = page.waitForEvent('download')
    await done.getByTestId('report-download-pdf').click()
    expect((await download).suggestedFilename()).toMatch(/^horus-seguridad-.+\.pdf$/)

    await page.getByTestId('report-generate').click()
    await page.getByTestId('generate-submit').click()
    await expect(page.locator('[data-testid="report-row"][data-status="queued"]')).toHaveCount(1)
    await expect(page.locator('[data-testid="report-row"][data-status="completed"]')).toHaveCount(
      6,
      { timeout: 20_000 },
    )

    const schedule = page.getByTestId('report-schedule')
    await expect(schedule.getByTestId('report-next-run')).toContainText('/2026')
    await schedule.getByTestId('schedule-recipients').fill('noc@fibranorte.example')
    await schedule.getByTestId('schedule-save').click()
    await expect(page.getByText('Programación guardada.', { exact: true })).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])
  })

  test('el operador NOC ve y descarga pero no genera ni programa', async ({ page }) => {
    await loginAsNoc(page)
    await page.goto(`${T}/reports`)
    await expect(page.getByRole('heading', { level: 1, name: 'Reportes' })).toBeVisible()
    await expect(page.getByTestId('report-row').first()).toBeVisible()
    await expect(page.getByTestId('report-generate')).toHaveCount(0)
    await expect(page.getByTestId('schedule-save')).toHaveCount(0)
    await expect(page.getByTestId('schedule-enabled')).toBeDisabled()
  })
})
