import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/**
 * Switches de monitoreo (docs/vendors/mikrotik.md §13, huawei.md §11): lista por nodo, alta de un
 * switch SwOS con aviso de SNMP v2c, configuración para pegar (comunidad una sola vez),
 * verificación SNMP en vivo, ficha con puertos (elección, subida, tendencia, vecinos LLDP), top
 * sFlow como fuente secundaria y el badge de switch en la capacidad. `pnpm e2e --grep @switches`.
 */
const T = '/t/fibra-norte'

test.describe('switches @switches', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.switchVerifyMs', '1500'))
  })

  test('lista → alta SwOS (v2c) → configuración → SNMP verificado → elegir puertos', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(`${T}/switches`)
    const table = page.getByTestId('switches-table')
    await expect(table.locator('[data-switch="sw-core-1"]')).toBeVisible()
    await expect(table).toContainText('Huawei serie S (VRP)')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('add-switch').click()
    await expect(page.getByRole('dialog', { name: 'Agregar switch' })).toBeVisible()
    await page.getByTestId('switch-name').fill('sw-torre-sur')
    await page.getByTestId('switch-platform').getByText('MikroTik SwOS').click()
    await expect(page.getByTestId('switch-swos-warning')).toContainText('SNMP v1/v2c')
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('switch-submit').click()
    await expect(page).toHaveURL(/\/switches\/[^/?]+\?onboard=1$/)

    const wizard = page.getByTestId('switch-onboarding')
    await expect(wizard.getByTestId('switch-onboarding-v2c')).toBeVisible()
    await expect(wizard.getByTestId('switch-sflow')).toHaveCount(0)
    await page.getByTestId('switch-generate').click()
    await expect(wizard).toContainText('Indique la dirección de gestión')
    await page.getByTestId('switch-address').fill('10.20.0.40')
    await page.getByTestId('switch-generate').click()
    await expect(page.getByTestId('switch-config')).toContainText('Community = ')
    await expect(page.getByTestId('switch-once')).toContainText('una sola vez')
    await expect(page.getByTestId('switch-verified')).toContainText('10.20.0.40', {
      timeout: 15_000,
    })

    const ports = page.getByTestId('switch-ports')
    await expect(ports).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('switch-limited-mibs')).toBeVisible()
    await expect(page.locator('[data-port-row="SFP+1"]')).toBeVisible()
    await page.getByTestId('switch-ports-edit').click()
    await page.locator('[data-testid="port-uplink"][data-port="Port3"]').click()
    await page.getByTestId('switch-ports-save').click()
    await expect(page.getByTestId('switch-ports-summary')).toContainText('Elección guardada')
    await expect(
      page.locator('[data-port-row="Port3"]').getByTestId('port-uplink-badge'),
    ).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])
  })

  test('ficha de un switch Huawei: vecino LLDP, top sFlow secundario y capacidad', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(`${T}/switches`)
    await page.getByTestId('switches-table').locator('[data-switch="sw-core-1"]').click()
    await expect(page.getByTestId('switch-detail')).toBeVisible()
    await expect(page.getByTestId('switch-health')).toContainText('S5735-L48T4X-A1')
    await expect(
      page.locator('[data-port-row="XGigabitEthernet0/0/1"]').getByTestId('port-uplink-badge'),
    ).toBeVisible()
    await expect(page.getByTestId('switch-neighbors')).toContainText('rt-centro-01')
    await expect(page.getByTestId('switch-top-secondary')).toContainText('Nunca se suman')
    await expect(page.getByTestId('switch-top-list')).toContainText('10.20.1.9')
    expect(await seriousViolations(page)).toEqual([])

    await page.goto(`${T}/capacity`)
    await expect(page.getByTestId('uplink-switch').first()).toContainText('Switch')
  })
})
