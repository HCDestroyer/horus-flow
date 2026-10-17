import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/**
 * Huawei VRP en modo directo (docs/vendors/huawei.md): alta con fabricante Huawei, aviso de que no
 * hay túnel WireGuard, configuración VRP con la dirección de gestión y verificación SNMPv3 en vivo.
 * `pnpm e2e --grep @huawei`.
 */

const T = '/t/fibra-norte'
const CENTRO = `${T}/nodes/0192e111-0000-7000-8000-000000000101`

test.describe('router Huawei en modo directo @huawei', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('alta Huawei → configuración VRP sin túnel → SNMP verificado', async ({ page }) => {
    await page.addInitScript(() =>
      window.localStorage.setItem('horus.mock.onboardingStepMs', '2500'),
    )
    await loginAsAdmin(page)
    await page.goto(CENTRO)
    await page.getByTestId('add-router').click()
    await expect(page.getByRole('dialog', { name: 'Agregar router' })).toBeVisible()
    await page.getByTestId('router-vendor').click()
    await page.getByRole('option', { name: 'Huawei (VRP)' }).click()
    await expect(page.getByTestId('router-direct-warning')).toContainText('sin túnel WireGuard')
    await expect(page.getByTestId('router-version')).toHaveCount(0)
    await page.getByTestId('router-name').fill('ne40e-centro')
    await page.getByTestId('router-model').fill('NE40E-X8A')
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('router-submit').click()
    await expect(page.getByTestId('router-created')).toBeVisible()
    await expect(
      page.getByRole('dialog', { name: 'Router ne40e-centro registrado' }),
    ).toContainText('configuración VRP')
    await page.getByTestId('router-continue').click()
    await expect(page).toHaveURL(/\/routers\/[^/]+\/connection$/)

    const wizard = page.getByTestId('direct-onboarding')
    await expect(wizard.getByTestId('direct-warning')).toContainText(
      'Huawei VRP no admite WireGuard',
    )
    await expect(page.getByTestId('router-direct-badge')).toContainText('Modo directo')
    await expect(page.getByTestId('router-vendor-line')).toContainText('Huawei (VRP)')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('direct-generate').click()
    await expect(wizard).toContainText('Indica la dirección de gestión')
    await page.getByTestId('direct-address').fill('198.51.100.20')
    await page.getByTestId('direct-generate').click()
    const config = page.getByTestId('direct-config')
    await expect(config).toContainText('ip netstream export source 198.51.100.20')
    await expect(config).toContainText('snmp-agent usm-user v3')
    await expect(page.getByTestId('direct-once')).toContainText('Solo se muestra una vez')
    await expect(page.getByTestId('direct-notes')).toContainText('PENDIENTE DE VALIDACIÓN')

    await expect(page.getByTestId('direct-verified')).toContainText('198.51.100.20', {
      timeout: 15_000,
    })
    await expect(page.locator('[data-check="flow"]')).toHaveAttribute('data-done', 'true', {
      timeout: 20_000,
    })
  })
})
