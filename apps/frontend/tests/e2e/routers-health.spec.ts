import { expect, test, type Page } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, seriousViolations } from './helpers'
import { layoutReport } from './layout-audit'

/**
 * EP-08 · Salud de routers (vista NOC, `/t/:slug/routers`): resumen por estado, filtros en
 * la URL, tarjetas con razón, aviso con el sondeador apagado, modo mural sin desbordamiento
 * y aparición por permiso. `pnpm e2e --grep @routers`.
 */
const T = '/t/fibra-norte'

async function enableI2(page: Page) {
  await page.addInitScript(() => {
    window.localStorage.setItem('horus.mock.navIncrement', 'I2')
    window.localStorage.setItem('horus.mock.findingEveryMs', '0')
  })
}

test.describe('salud de routers @routers', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await enableI2(page)
  })

  test('resumen por estado, tarjetas problemas primero y razón "CPU alta"', async ({ page }) => {
    await loginAsAdmin(page)
    await page.getByTestId('main-nav').getByRole('link', { name: 'Salud de routers' }).click()
    await expect(page).toHaveURL(/\/routers$/)
    await expect(page.getByRole('heading', { level: 1, name: 'Salud de routers' })).toBeVisible()
    const summary = page.getByTestId('routers-summary')
    await expect(summary.locator('[data-summary="online"]')).toContainText('En línea')
    await expect(summary.locator('[data-summary="warning"]')).toContainText('1')
    await expect(page.getByTestId('poller-ok')).toContainText('routers en sondeo')

    const cards = page.getByTestId('router-card')
    await expect(cards).toHaveCount(6)
    // Problemas primero: el de aviso antes que los en línea; el pendiente de alta, sin observar.
    const statuses = await cards.evaluateAll((els) =>
      els.map((e) => e.getAttribute('data-router-status')),
    )
    expect(statuses.indexOf('warning')).toBeLessThan(statuses.indexOf('online'))
    expect(statuses).toContain('stale')
    expect(statuses).toContain('unknown')
    const warning = page.locator('[data-testid="router-card"][data-router-status="warning"]')
    await expect(warning.getByTestId('router-status-reason')).toContainText('CPU alta')
    await expect(warning.locator('[data-meter-level="warning"]')).toHaveCount(1)
    await expect(warning.locator('[data-signal="snmp"]')).toContainText('ok')
    // Sparkline perezoso: con la tarjeta en pantalla se pide la serie del uplink.
    await expect(warning.getByTestId('router-sparkline')).toBeVisible({ timeout: 10_000 })
    expect(await seriousViolations(page)).toEqual([])

    // Click en la tarjeta → ficha del router con su estado en la cabecera.
    await warning.click()
    await expect(page).toHaveURL(/\/routers\/0192e333-/)
    await expect(page.getByTestId('router-status-badge')).toContainText('Aviso')
  })

  test('los filtros de nodo y estado viven en la URL', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(`${T}/routers?status=problems`)
    const cards = page.getByTestId('router-card')
    await expect(cards).toHaveCount(3)
    for (const s of await cards.evaluateAll((els) =>
      els.map((e) => e.getAttribute('data-router-status')),
    )) {
      expect(s).not.toBe('online')
    }
    await page.getByTestId('routers-status').click()
    await page.getByRole('option', { name: 'Aviso', exact: true }).click()
    await expect(page).toHaveURL(/status=warning/)
    await expect(cards).toHaveCount(1)
    await page.getByTestId('routers-site').click()
    await page.getByRole('option', { name: 'Nodo Centro' }).click()
    await expect(page).toHaveURL(/site=0192e111-0000-7000-8000-000000000101/)
    await expect(page.getByText('Ningún router coincide con estos filtros.')).toBeVisible()
    await page.goto(`${T}/routers?site=0192e111-0000-7000-8000-000000000101`)
    await expect(cards).toHaveCount(1)
    await expect(cards.first()).toContainText('rt-centro')
  })

  test('con el sondeo SNMP desactivado (snmp-off) todo es "último conocido" y se avisa', async ({
    page,
  }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.scenario', 'snmp-off'))
    await loginAsAdmin(page)
    await page.goto(`${T}/routers`)
    await expect(page.getByTestId('poller-notice')).toContainText('HORUS_SNMP_ENABLED')
    const cards = page.getByTestId('router-card')
    await expect(cards).toHaveCount(6)
    const statuses = await cards.evaluateAll((els) =>
      els.map((e) => e.getAttribute('data-router-status')),
    )
    for (const s of statuses) expect(['stale', 'unknown']).toContain(s)
    // Nadie queda "fuera de línea" por falta de monitoreo.
    await expect(
      page.locator('[data-testid="router-card"][data-router-status="offline"]'),
    ).toHaveCount(0)
    await expect(
      page.getByTestId('routers-summary').locator('[data-summary="nodata"]'),
    ).toContainText('6')
  })

  test('visible con la compilación por defecto para quien tiene devices.read (el permiso lo cubre el test unitario)', async ({
    browser,
  }) => {
    // Sin override de incremento (build por defecto): el NOC (devices.read, sin devices.update) la ve y entra.
    const plain = await browser.newContext()
    const p1 = await plain.newPage()
    await loginAsNoc(p1)
    const link = p1.getByTestId('main-nav').getByRole('link', { name: 'Salud de routers' })
    await expect(link).toBeVisible()
    await link.click()
    await expect(p1.getByRole('heading', { level: 1, name: 'Salud de routers' })).toBeVisible()
    await expect(p1.getByTestId('router-card').first()).toBeVisible()
    await plain.close()
  })
})

test.describe('salud de routers · mural @routers @layout', () => {
  for (const [width, height] of [
    [1920, 1080],
    [3840, 2160],
  ] as const) {
    test(`mural ${width}×${height} sin desbordamiento`, async ({ page }) => {
      await page.setViewportSize({ width, height })
      await page.emulateMedia({ reducedMotion: 'reduce' })
      await enableI2(page)
      await loginAsAdmin(page)
      await page.goto(`${T}/routers?scale=wall`)
      await expect(page.getByTestId('routers-wall')).toBeVisible()
      await expect(page.locator('#app-sidebar')).toHaveCount(0)
      await expect(page.getByTestId('router-card')).toHaveCount(6)
      await expect(page.getByTestId('router-sparkline').first()).toBeVisible({ timeout: 10_000 })
      await page.waitForTimeout(600)
      const problems = await layoutReport(page, {
        noScroll: true,
        extra: ['[data-testid="routers-wall"]'],
      })
      expect(problems).toEqual([])
      const minText = await page.evaluate(() => {
        const root = document.querySelector('[data-testid="routers-wall"]')!
        const texts = [...root.querySelectorAll<HTMLElement>('*')].filter(
          (el) =>
            [...el.childNodes].some((n) => n.nodeType === 3 && n.textContent!.trim()) &&
            el.offsetParent !== null &&
            !el.closest('.sr-only') &&
            !el.closest('[data-testid="exit-wall"]') &&
            getComputedStyle(el).display !== 'none',
        )
        return Math.min(...texts.map((el) => parseFloat(getComputedStyle(el).fontSize)))
      })
      expect(minText).toBeGreaterThanOrEqual(20 * (height / 1080) - 0.5)
      // Halo solo en lo crítico o caído.
      await expect(page.locator('[data-testid="router-card"].halo-critical')).toHaveCount(0)
      await page.keyboard.press('Escape')
      await expect(page).not.toHaveURL(/scale=wall/)
      await expect(page.getByRole('heading', { level: 1, name: 'Salud de routers' })).toBeVisible()
    })
  }
})
