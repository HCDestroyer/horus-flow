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
    // «¿Horus está en la misma red de gestión que el switch?» → por defecto no (vía router).
    await expect(wizard.getByTestId('switch-mode-via-note')).toBeVisible()
    await wizard.getByTestId('switch-mode').getByText('Sí — modo directo').click()
    await expect(wizard.getByTestId('switch-mode-direct-note')).toBeVisible()
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

  test('alta vía router del sitio: camino, configuración del router, ficha y lista del router', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(`${T}/switches`)
    await page.getByTestId('add-switch').click()
    await page.getByTestId('switch-name').fill('sw-via-norte')
    await page.getByTestId('switch-platform').getByText('Huawei serie S (VRP)').click()
    await page.getByTestId('switch-submit').click()
    await expect(page).toHaveURL(/\/switches\/[^/?]+\?onboard=1$/)

    const wizard = page.getByTestId('switch-onboarding')
    await expect(wizard.getByTestId('switch-mode-via-note')).toBeVisible()
    await page.getByTestId('switch-address').fill('192.168.88.2')
    await page.getByTestId('switch-generate').click()
    await expect(wizard).toContainText('Elija el router del sitio')
    await page.getByTestId('switch-via-router').click()
    await page.getByRole('option').first().click()
    await page.getByTestId('switch-generate').click()
    const path = page.getByTestId('switch-via-path').first()
    await expect(path).toContainText('Vía router')
    await expect(path.getByTestId('switch-via-protection')).toBeVisible()
    await expect(path.getByTestId('switch-via-horus')).toContainText('10.255.200.')
    await expect(page.getByTestId('switch-router-config')).toContainText(
      'netmap to-addresses=192.168.88.2',
    )
    await expect(page.getByTestId('switch-config')).toContainText('10.255.0.1')
    expect(await seriousViolations(page)).toEqual([])

    // La ficha muestra el camino y enlaza al router, que lista el switch.
    await expect(page.getByTestId('switch-verified')).toBeVisible({ timeout: 15_000 })
    const fichaPath = page.getByTestId('switch-detail-path')
    await expect(fichaPath).toContainText('192.168.88.2')
    await fichaPath.getByRole('link').first().click()
    await expect(page).toHaveURL(/\/routers\//)
    const card = page.getByTestId('router-via-switches')
    await expect(card.locator('[data-via-switch="sw-via-norte"]')).toContainText('192.168.88.2')
    await card.getByTestId('router-subnet-input').fill('192.168.88.0/24')
    await card.getByTestId('router-subnet-add').click()
    await expect(card.locator('[data-subnet="192.168.88.0/24"]')).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])
  })

  test('switch vía router con el túnel del router caído: inalcanzable, no fuera de línea', async ({
    page,
  }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.viaTunnelDown', '1'))
    await loginAsAdmin(page)
    await page.goto(`${T}/switches`)
    await page.getByTestId('add-switch').click()
    await page.getByTestId('switch-name').fill('sw-via-sur')
    await page.getByTestId('switch-platform').getByText('MikroTik RouterOS').click()
    await page.getByTestId('switch-submit').click()
    await page.getByTestId('switch-address').fill('192.168.90.2')
    await page.getByTestId('switch-via-router').click()
    await page.getByRole('option').first().click()
    await page.getByTestId('switch-generate').click()
    await expect(page.getByTestId('switch-verified')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('switch-status')).toContainText('Inalcanzable', {
      timeout: 15_000,
    })
    await expect(page.getByTestId('switch-status')).toContainText(
      'Túnel del router del sitio caído',
    )
    await expect(page.getByTestId('switch-via-tunnel-down')).toBeVisible()
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
