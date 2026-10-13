import { expect, test } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, seriousViolations } from './helpers'
import { layoutReport } from './layout-audit'

/**
 * EP-08 · Ficha del router: estado SNMP en la cabecera, "Sondear ahora", pestañas Métricas e
 * Interfaces (gráficas, tabla, selección de interfaz) y el mural de un solo router.
 * `pnpm e2e --grep @router-detail`.
 */
const T = '/t/fibra-norte'
const NORTE = `${T}/routers/0192e333-0000-7000-8000-000000000102` // warning / cpu_high
const CENTRO = `${T}/routers/0192e333-0000-7000-8000-000000000101`
const RIBERA = '/t/valle-conecta/routers/0192e333-0000-7000-8000-000000000202' // offline

test.describe('ficha del router · salud @router-detail', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.findingEveryMs', '0'))
  })

  test('cabecera con estado, razón y muestra en vivo; "Sondear ahora" encola y se deshabilita', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(NORTE)
    const badge = page.getByTestId('router-status-badge')
    await expect(badge).toContainText('Aviso')
    await expect(badge.getByTestId('router-status-reason')).toContainText('CPU alta')
    await expect(page.getByTestId('router-live')).toContainText('CPU')
    await expect(page.getByTestId('router-live')).toContainText('°C')
    const poll = page.getByTestId('poll-now')
    await poll.click()
    await expect(page.getByText('Sondeo en cola').first()).toBeVisible()
    await expect(poll).toBeDisabled()
    // El sondeo concluye a los 2 s y llega por el canal en vivo ("En vivo · hace N s").
    await expect(page.getByTestId('router-live').getByTestId('freshness')).toContainText(
      'En vivo',
      {
        timeout: 10_000,
      },
    )
    expect(await seriousViolations(page)).toEqual([])
  })

  test('el NOC (sin devices.update) no ve "Sondear ahora"; el router caído dice por qué', async ({
    page,
    browser,
  }) => {
    const ctx = await browser.newContext()
    const noc = await ctx.newPage()
    await loginAsNoc(noc)
    await noc.goto(CENTRO)
    await expect(noc.getByTestId('router-status-badge')).toContainText('En línea')
    await expect(noc.getByTestId('router-live')).toContainText('CPU')
    await expect(noc.getByTestId('poll-now')).toHaveCount(0)
    await ctx.close()
    await loginAsAdmin(page)
    await page.goto(RIBERA)
    const badge = page.getByTestId('router-status-badge')
    await expect(badge).toContainText('Fuera de línea')
    await expect(badge.getByTestId('router-status-reason')).toContainText('Túnel caído')
  })

  test('pestaña Métricas: cinco gráficas con el rango en la URL', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(`${NORTE}/metrics`)
    const panel = page.getByTestId('router-metrics')
    for (const k of ['cpu', 'memory', 'temperature', 'latency', 'availability']) {
      await expect(panel.getByTestId(`router-chart-${k}`).locator('canvas')).toBeVisible()
    }
    await expect(panel.getByTestId('router-chart-cpu')).toContainText('Núcleo máximo')
    await page.getByTestId('router-range').click()
    await page.getByRole('option', { name: 'Últimas 6 h' }).click()
    await expect(page).toHaveURL(/range=6h/)
    await expect(panel.getByTestId('router-chart-cpu').locator('canvas')).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])
  })

  test('con la analítica caída, Métricas avisa de degradado y la ficha sigue', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.scenario', 'degraded'))
    await loginAsAdmin(page)
    await page.goto(`${NORTE}/metrics`)
    await expect(page.getByTestId('degraded-notice')).toBeVisible()
    await expect(page.getByTestId('router-status-badge')).toBeVisible()
  })

  test('pestaña Interfaces: tabla, uplink marcado por defecto y selección de hasta 4', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(`${NORTE}/interfaces`)
    const table = page.getByTestId('interfaces-table')
    await expect(table.locator('tbody tr')).toHaveCount(10)
    await expect(table).not.toContainText('pppoe')
    await expect(table.getByTestId('interface-uplink')).toHaveCount(1)
    await expect(table.locator('[data-oper="down"]')).toHaveCount(2)
    await expect(page.getByTestId('interfaces-selected')).toContainText('1 de 4')
    await expect(page.getByTestId('interfaces-chart')).toContainText('sfp-sfpplus1')
    await expect(page.getByTestId('interfaces-chart').locator('canvas')).toBeVisible()
    for (const name of ['ether2', 'ether3', 'sfp-sfpplus2']) {
      await page.locator(`[data-testid="interface-select"][data-interface="${name}"]`).click()
    }
    await expect(page.getByTestId('interfaces-selected')).toContainText('4 de 4')
    await page.locator('[data-testid="interface-select"][data-interface="ether4"]').click()
    await expect(page.getByText('Como mucho 4 interfaces a la vez').first()).toBeVisible()
    await expect(page.getByTestId('interfaces-selected')).toContainText('4 de 4')
    await expect(page.getByTestId('interfaces-chart')).toContainText('ether2')
    expect(await seriousViolations(page)).toEqual([])
  })

  test('mural de un router a 1920×1080: cabecera, medidores, gráfica e interfaces sin scroll', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await loginAsAdmin(page)
    await page.goto(`${NORTE}?scale=wall`)
    const wall = page.getByTestId('router-wall')
    await expect(wall).toBeVisible()
    await expect(page.locator('#app-sidebar')).toHaveCount(0)
    await expect(wall.getByRole('heading', { level: 1 })).toContainText('rt-norte')
    await expect(wall).toContainText('CPU alta')
    await expect(wall.locator('canvas')).toBeVisible()
    await expect(wall.getByTestId('router-wall-interfaces').locator('[data-fit-item]')).toHaveCount(
      8,
    )
    await page.waitForTimeout(800)
    const problems = await layoutReport(page, {
      noScroll: true,
      extra: ['[data-testid="router-wall"]'],
    })
    expect(problems).toEqual([])
    await page.keyboard.press('Escape')
    await expect(page).not.toHaveURL(/scale=wall/)
    await expect(page.getByTestId('router-header')).toBeVisible()
  })
})
