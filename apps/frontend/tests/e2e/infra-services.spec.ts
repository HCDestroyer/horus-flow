import { expect, test } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, seriousViolations } from './helpers'

/**
 * Servicios de infraestructura (`/t/:slug/infra-services`, rol snmp): lista con estados
 * (problemas primero), detalle con historial, alta con el constructor de chequeos, script de
 * router para un servidor privado y aparición por permiso. `pnpm e2e --grep @infra`.
 */
const T = '/t/fibra-norte'

test.describe('servicios de infraestructura @infra', () => {
  test.describe.configure({ timeout: 90_000 })
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.findingEveryMs', '0'))
  })

  test('lista con estados, problemas primero y detalle con historial', async ({ page }) => {
    await loginAsAdmin(page)
    await page
      .getByTestId('main-nav')
      .getByRole('link', { name: 'Servicios de infraestructura' })
      .click()
    await expect(page).toHaveURL(/\/infra-services$/)
    await expect(
      page.getByRole('heading', { level: 1, name: 'Servicios de infraestructura' }),
    ).toBeVisible()
    const summary = page.getByTestId('infra-summary')
    await expect(summary.locator('[data-summary="down"]')).toContainText('1')
    await expect(summary.locator('[data-summary="degraded"]')).toContainText('1')

    const rows = page.getByTestId('infra-row')
    await expect(rows).toHaveCount(4)
    const states = await rows.evaluateAll((els) => els.map((e) => e.getAttribute('data-state')))
    expect(states).toEqual(['down', 'degraded', 'unknown', 'up'])
    const dns = page.locator('[data-testid="infra-row"][data-service="DNS recursivo 1"]')
    await expect(dns.getByTestId('infra-state')).toContainText('Caído')
    await expect(dns.getByTestId('infra-reason')).toContainText('Sin tráfico')
    await expect(
      page
        .locator('[data-testid="infra-row"][data-service="IPTV cabecera"]')
        .getByTestId('infra-latency'),
    ).toContainText('ms')
    await expect(
      page
        .locator('[data-testid="infra-row"][data-service="RADIUS PPPoE"]')
        .getByTestId('infra-private'),
    ).toContainText('IP privada')
    expect(await seriousViolations(page)).toEqual([])

    // Filtro de estado en la URL.
    await page.goto(`${T}/infra-services?state=problems`)
    await expect(page.getByTestId('infra-row')).toHaveCount(2)

    // Detalle con historial reciente.
    await page.goto(`${T}/infra-services`)
    await page.getByRole('button', { name: 'Ver detalle de Portal web de clientes' }).click()
    const drawer = page.getByTestId('infra-drawer')
    await expect(drawer.getByTestId('infra-drawer-state')).toContainText('Degradado')
    await expect(
      drawer.locator('[data-testid="infra-drawer-check"][data-check-type="ddos_inbound"]'),
    ).toContainText('pps')
    await expect(drawer.getByTestId('infra-history').first().locator('li')).toHaveCount(20)
  })

  test('alta con chequeos y script de router para un servidor privado', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(`${T}/infra-services`)
    await page.getByTestId('add-infra').click()
    await page.getByTestId('infra-name').fill('Caché de vídeo')
    await page.getByTestId('infra-target').fill('192.168.50.10')
    await expect(page.getByTestId('infra-private-hint')).toBeVisible()
    await page.getByTestId('infra-via-router').click()
    await page.getByTestId('infra-router').click()
    await page.getByRole('option').first().click()
    // El ping inicial se cambia por un puerto TCP con reenvío.
    const first = page.getByTestId('infra-check').first()
    await first.getByTestId('infra-check-type').click()
    await page.getByRole('option', { name: 'Puerto TCP' }).click()
    await first.getByTestId('infra-check-port').fill('8443')
    await first.getByTestId('infra-check-forward-port').fill('18443')
    await page.getByTestId('infra-submit').click()

    const row = page.locator('[data-testid="infra-row"][data-service="Caché de vídeo"]')
    await expect(row).toBeVisible()
    await expect(row.getByTestId('infra-state')).toContainText('Sin datos')
    await expect(row.getByTestId('infra-private')).toContainText('Por el router')

    await row.getByTestId('infra-snippet-open').click()
    const dialog = page.getByTestId('infra-snippet')
    await expect(dialog.getByTestId('infra-snippet-script')).toContainText('dst-port=18443')
    await expect(dialog.getByTestId('infra-snippet-script')).toContainText(
      'to-addresses=192.168.50.10 to-ports=8443',
    )
    await expect(dialog.getByTestId('infra-snippet-horus')).toContainText('no hay que cambiar nada')
    expect(await seriousViolations(page)).toEqual([])
  })

  test('el NOC ve los servicios pero no los gestiona', async ({ page }) => {
    await loginAsNoc(page)
    await page.goto(`${T}/infra-services`)
    await expect(page.getByTestId('infra-row')).toHaveCount(4)
    await expect(page.getByTestId('add-infra')).toHaveCount(0)
    await expect(page.getByTestId('infra-snippet-open')).toHaveCount(0)
  })
})
