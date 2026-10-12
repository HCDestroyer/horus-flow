import { expect, test, type Page } from '@playwright/test'
import { layoutReport } from './layout-audit'
import { loginAsAdmin, PASSWORD, seriousViolations, TOTP } from './helpers'

/**
 * Hub WireGuard de la consola de plataforma (`platform.wireguard.manage`): editar el hub (con
 * re-autenticación y 412), añadir rangos con validación (solapes, CGNAT), quitar solo rangos
 * vacíos que no sean el último (cuarentena de 24 h), aviso de ocupación alta y diseño móvil.
 * `pnpm e2e --grep @wireguard`.
 */

const PAGE = '/platform/wireguard'

async function openHub(page: Page) {
  await page.goto(PAGE)
  const hub = page.getByTestId('wg-hub').first()
  await expect(hub.getByTestId('wg-pools')).toBeVisible()
  return hub
}

const poolRow = (page: Page, cidr: string) =>
  page.locator(`[data-testid="wg-pool"][data-pool="${cidr}"]`)

async function addPool(page: Page, cidr: string) {
  await page.getByTestId('wg-pool-add').click()
  await page.getByTestId('wg-pool-cidr').fill(cidr)
  await page.getByTestId('wg-pool-submit').click()
}

async function reauth(page: Page) {
  const dialog = page.getByRole('dialog', { name: 'Confirma tu identidad' })
  await expect(dialog).toContainText('hub WireGuard')
  await page.getByTestId('reauth-password').fill(PASSWORD)
  await page.getByTestId('reauth-code').fill(TOTP)
  await page.getByTestId('reauth-submit').click()
}

test.describe('hub WireGuard @wireguard', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('hub con MTU, keepalive, rangos y servicios reservados; el único rango no se quita', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    const hub = await openHub(page)
    await expect(page.getByTestId('wg-intro')).toContainText('a partir de ahora')
    await expect(hub.getByTestId('wg-mtu')).toHaveText('1420')
    await expect(hub.getByTestId('wg-keepalive')).toHaveText('25 s')
    const table = hub.getByTestId('wg-pools')
    for (const header of ['Rango (CIDR)', 'Ocupación', 'Alta']) {
      await expect(table.getByRole('columnheader', { name: header })).toBeVisible()
    }
    const seed = poolRow(page, '10.255.0.0/16')
    await expect(seed.getByTestId('wg-pool-occupancy')).toHaveText(/\d+ de 65[.\s\u202f]534 \(/)
    await expect(seed.getByTestId('wg-pool-remove')).toBeDisabled()
    await expect(seed.getByTestId('wg-pool-blocked')).toHaveText(
      'Es el único rango: no se puede quitar.',
    )
    await expect(hub.getByTestId('wg-services')).toContainText('10.254.0.0/24')
    await expect(hub.getByTestId('wg-services')).toContainText('Reservado')
    await expect(hub.getByTestId('wg-occupancy-alert')).toHaveCount(0)
    expect(await seriousViolations(page)).toEqual([])
  })

  test('editar el hub con re-autenticación y aviso al cambiar el endpoint', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.reauthRequired', '1'))
    await loginAsAdmin(page)
    const hub = await openHub(page)
    await hub.getByTestId('wg-hub-edit').click()
    const modal = page.getByRole('dialog', { name: 'Editar hub-principal' })
    await expect(modal).toContainText('Solo para los routers que conectes a partir de ahora')
    await expect(modal.getByTestId('wg-hub-port')).toContainText('51820/udp')
    await expect(modal.getByTestId('wg-hub-port')).toContainText('install.sh --wg-port')
    await expect(modal.getByTestId('wg-hub-endpoint-warning')).toHaveCount(0)
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('wg-hub-endpoint').fill('https://vpn.fibranorte.example')
    await page.getByTestId('wg-hub-submit').click()
    await expect(modal).toContainText('sin https:// ni puerto')

    await page.getByTestId('wg-hub-endpoint').fill('vpn.fibranorte.example')
    await expect(modal.getByTestId('wg-hub-endpoint-warning')).toContainText(
      'Los routers existentes seguirán usando el endpoint anterior (203.0.113.10)',
    )
    await page.getByTestId('wg-hub-mtu').fill('1380')
    await page.getByTestId('wg-hub-mtu').blur()
    await page.getByTestId('wg-hub-submit').click()
    await reauth(page)
    await expect(page.getByText('Hub hub-principal actualizado', { exact: true })).toBeVisible()
    await expect(modal).toBeHidden()
    await expect(hub.getByTestId('wg-endpoint')).toHaveText('vpn.fibranorte.example:51820')
    await expect(hub.getByTestId('wg-mtu')).toHaveText('1380')

    // Segunda edición sobre la versión nueva (sin 412) y sin volver a pedir la identidad.
    await hub.getByTestId('wg-hub-edit').click()
    await expect(page.getByTestId('wg-hub-endpoint')).toHaveValue('vpn.fibranorte.example')
    await page.getByTestId('wg-hub-keepalive').fill('0')
    await page.getByTestId('wg-hub-keepalive').blur()
    await page.getByTestId('wg-hub-submit').click()
    await expect(hub.getByTestId('wg-keepalive')).toHaveText('Desactivado')

    // Persistido en la sesión simulada.
    await page.reload()
    await expect(page.getByTestId('wg-mtu')).toHaveText('1380')
  })

  test('412: otra persona cambió el hub → se cargan los valores actuales', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.wgHubConflict', '1'))
    await loginAsAdmin(page)
    const hub = await openHub(page)
    await hub.getByTestId('wg-hub-edit').click()
    await page.getByTestId('wg-hub-mtu').fill('1400')
    await page.getByTestId('wg-hub-mtu').blur()
    await page.getByTestId('wg-hub-submit').click()
    await expect(page.getByTestId('wg-hub-conflict')).toContainText('Otra persona cambió el hub')
    await expect(page.getByTestId('wg-hub-keepalive')).toHaveValue('30')
    await expect(page.getByTestId('wg-hub-mtu')).toHaveValue('1420')
    await page.getByTestId('wg-hub-mtu').fill('1400')
    await page.getByTestId('wg-hub-mtu').blur()
    await page.getByTestId('wg-hub-submit').click()
    await expect(page.getByText('Hub hub-principal actualizado', { exact: true })).toBeVisible()
    await expect(hub.getByTestId('wg-mtu')).toHaveText('1400')
    await expect(hub.getByTestId('wg-keepalive')).toHaveText('30 s')
  })

  test('añadir un rango: sugerencia libre, solapes y CGNAT rechazados', async ({ page }) => {
    await loginAsAdmin(page)
    await openHub(page)
    await page.getByTestId('wg-pool-add').click()
    const modal = page.getByRole('dialog', { name: 'Añadir rango a hub-principal' })
    await expect(modal.getByTestId('wg-pool-explain')).toContainText('nunca se mueven')
    await expect(modal.getByTestId('wg-pool-explain')).toContainText('redes internas del ISP')
    await expect(page.getByTestId('wg-pool-cidr')).toHaveAttribute('placeholder', '10.253.0.0/16')
    expect(await seriousViolations(page)).toEqual([])

    const cidr = page.getByTestId('wg-pool-cidr')
    const submit = page.getByTestId('wg-pool-submit')
    await cidr.fill('10.255.128.0/17')
    await submit.click()
    await expect(modal).toContainText('Se solapa con el rango 10.255.0.0/16 del hub.')
    await cidr.fill('10.254.0.0/16')
    await submit.click()
    await expect(modal).toContainText('Se solapa con el rango de servicios 10.254.0.0/24')
    await cidr.fill('100.64.0.0/16')
    await submit.click()
    await expect(modal).toContainText('100.64.0.0/10 es el espacio CGNAT')
    await cidr.fill('10.253.4.0/16')
    await submit.click()
    await expect(modal).toContainText('Escribe la dirección de red del rango: 10.253.0.0/16.')
    await cidr.fill('10.0.0.0/8')
    await submit.click()
    await expect(modal).toContainText('entre /16 y /28')

    await page.getByTestId('wg-pool-suggestion').click()
    await expect(cidr).toHaveValue('10.253.0.0/16')
    await submit.click()
    await expect(page.getByText('Rango 10.253.0.0/16 añadido', { exact: true })).toBeVisible()
    await expect(modal).toBeHidden()

    const added = poolRow(page, '10.253.0.0/16')
    await expect(added.getByTestId('wg-pool-occupancy')).toHaveText(/^0 de 65[.\s\u202f]534 \(0/)
    await expect(added.getByTestId('wg-pool-remove')).toBeEnabled()
    // El rango con direcciones ya no es el último, pero sigue sin poder quitarse.
    const seed = poolRow(page, '10.255.0.0/16')
    await expect(seed.getByTestId('wg-pool-remove')).toBeDisabled()
    await expect(seed.getByTestId('wg-pool-blocked')).toHaveText('Tiene direcciones asignadas.')
    await expect(page.getByTestId('wg-occupancy')).toHaveText(/de 131[.\s\u202f]068 direcciones/)
  })

  test('quitar un rango vacío con re-autenticación', async ({ page }) => {
    await loginAsAdmin(page)
    await openHub(page)
    await addPool(page, '10.200.0.0/16')
    await expect(poolRow(page, '10.200.0.0/16')).toBeVisible()

    await page.evaluate(() => window.localStorage.setItem('horus.mock.reauthRequired', '1'))
    await poolRow(page, '10.200.0.0/16').getByTestId('wg-pool-remove').click()
    await expect(
      page.getByRole('dialog', { name: '¿Quitar el rango 10.200.0.0/16?' }),
    ).toBeVisible()
    await page.getByTestId('wg-pool-remove-confirm').click()
    await reauth(page)
    await expect(page.getByText('Rango 10.200.0.0/16 quitado', { exact: true })).toBeVisible()
    await expect(poolRow(page, '10.200.0.0/16')).toHaveCount(0)
    await expect(poolRow(page, '10.255.0.0/16').getByTestId('wg-pool-remove')).toBeDisabled()
  })

  test('rango en cuarentena: 409 WIREGUARD_POOL_IN_USE explica las 24 h', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.wgQuarantine', '1'))
    await loginAsAdmin(page)
    await openHub(page)
    await addPool(page, '10.253.0.0/16')
    await poolRow(page, '10.253.0.0/16').getByTestId('wg-pool-remove').click()
    await page.getByTestId('wg-pool-remove-confirm').click()
    const error = page.getByTestId('wg-pool-remove-error')
    await expect(error).toContainText('Este rango aún está en uso')
    await expect(error).toContainText('24 h en cuarentena')
    await expect(poolRow(page, '10.253.0.0/16')).toBeVisible()
  })

  test('ocupación por encima del 80 %: aviso que lleva a añadir un rango', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.wgUsage', '0.85'))
    await loginAsAdmin(page)
    const hub = await openHub(page)
    const alert = hub.getByTestId('wg-occupancy-alert')
    await expect(alert).toContainText('Ocupación alta: 85')
    await expect(alert).toContainText('Añade un rango antes de que se agoten')
    expect(await seriousViolations(page)).toEqual([])
    await alert.getByRole('button', { name: 'Añadir rango' }).click()
    await expect(page.getByRole('dialog', { name: 'Añadir rango a hub-principal' })).toBeVisible()
  })

  test('móvil: sin desbordamiento, rangos apilados y modales usables', async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 800 })
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.wgUsage', '0.85'))
    await loginAsAdmin(page)
    await openHub(page)
    await addPool(page, '10.253.0.0/16')
    await expect(poolRow(page, '10.253.0.0/16')).toBeVisible()
    const problems = await layoutReport(page, {
      extra: ['[data-testid="app-navbar"]', '#main-content'],
    })
    expect(problems).toEqual([])
    await page.getByTestId('wg-hub-edit').click()
    await expect(page.getByTestId('wg-hub-submit')).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
  })
})
