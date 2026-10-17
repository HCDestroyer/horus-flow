import { expect, test } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, seriousViolations } from './helpers'
import { layoutReport } from './layout-audit'

/**
 * Capacidad (`/t/:slug/capacity`, rol snmp): salud explicable por nodo, tabla de enlaces con
 * p95 y pronóstico, salida internacional, reparto local/internacional, detalle con el
 * gráfico de pronóstico, configuración de un enlace, script netwatch (la IP de la puerta de
 * enlace se completa en el navegador) y el widget «Salud de sitios / capacidad» en escala
 * normal y mural. `pnpm e2e --grep @capacity`.
 */
const T = '/t/fibra-norte'

test.describe('capacidad de enlaces @capacity', () => {
  test.describe.configure({ timeout: 120_000 })
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.findingEveryMs', '0'))
  })

  test('salud por nodo, enlaces, salida internacional y reparto', async ({ page }) => {
    await loginAsAdmin(page)
    await page.getByTestId('main-nav').getByRole('link', { name: 'Capacidad' }).click()
    await expect(page).toHaveURL(/\/capacity$/)
    await expect(page.getByRole('heading', { level: 1, name: 'Capacidad' })).toBeVisible()
    const sites = page.getByTestId('capacity-site')
    await expect(sites.first()).toBeVisible()
    // Peor salud primero y siempre con razón.
    const first = sites.first()
    await expect(first).not.toHaveAttribute('data-health', 'online')
    await expect(first.getByTestId('capacity-site-summary')).toContainText(
      /hora pico|pérdida|router/,
    )
    await expect(page.getByTestId('uplink-row').first()).toBeVisible()
    await expect(page.getByTestId('capacity-egress-recommendation')).toContainText(
      'salida internacional',
    )
    await expect(page.getByTestId('traffic-intl-share')).toContainText('%')
    expect(await seriousViolations(page)).toEqual([])

    // Detalle del enlace: recomendación, método y gráfico con tabla alternativa.
    await page.getByTestId('uplink-link').first().click()
    await expect(page).toHaveURL(/\/capacity\/uplinks\//)
    await expect(page.getByTestId('detail-recommendation')).toContainText(/upgrade|Sin acción/i)
    await expect(page.getByTestId('detail-method')).toContainText('Theil–Sen')
    await expect(page.getByTestId('detail-forecast').locator('canvas')).toBeVisible()
    await expect(
      page.getByTestId('detail-forecast').locator('table caption').first(),
    ).toContainText('Pico diario')
    // Historial de la capacidad contratada: cada día contra la capacidad vigente entonces.
    await expect(page.getByTestId('detail-capacity-history')).toContainText(
      'Historial de capacidad contratada',
    )
    expect(await seriousViolations(page)).toEqual([])
  })

  test('configura un enlace y genera el script netwatch sin enviar la IP', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(`${T}/capacity`)
    const row = page.getByTestId('uplink-row').first()
    await row.getByTestId('uplink-configure').click()
    const form = page.getByTestId('uplink-config')
    await form.getByTestId('uplink-label').fill('Proveedor E2E')
    await page.getByTestId('uplink-save').click()
    await expect(page.getByTestId('uplink-row').filter({ hasText: 'Proveedor E2E' })).toBeVisible()
    // Cambiar la capacidad pregunta desde cuándo rige (upgrade fechado en el pasado).
    await page
      .getByTestId('uplink-row')
      .filter({ hasText: 'Proveedor E2E' })
      .getByTestId('uplink-configure')
      .click()
    await expect(page.getByTestId('uplink-valid-from')).toHaveCount(0)
    await page.getByTestId('uplink-capacity').locator('input').fill('2500')
    await expect(page.getByTestId('uplink-valid-from')).toBeVisible()
    await page.keyboard.press('Escape')

    const requests: string[] = []
    page.on('request', (r) => requests.push(r.url()))
    await page.locator('[data-testid^="capacity-snippet-"]').first().click()
    const modal = page.getByTestId('netwatch-snippet')
    await expect(modal.getByTestId('netwatch-script')).toContainText('{GATEWAY_1}')
    await modal.locator('[data-testid^="netwatch-gateway-"]').first().fill('198.51.100.77')
    await expect(modal.getByTestId('netwatch-script')).toContainText('host=198.51.100.77')
    expect(requests.some((u) => u.includes('198.51.100.77'))).toBe(false)
  })

  test('lista local por prefijos: avisos y reparto local por prefijo', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(`${T}/capacity`)
    await page.getByTestId('capacity-settings-open').click()
    await page.getByTestId('settings-prefixes').fill('10.0.0.0/33')
    await page.getByTestId('settings-save').click()
    await expect(page.getByTestId('capacity-settings')).toContainText(
      'Prefijos no válidos: 10.0.0.0/33',
    )
    await page.getByTestId('settings-prefixes').fill('198.51.100.64/26\n100.64.12.0/24')
    await page.getByTestId('settings-save').click()
    await expect(page.getByText(/Revisa la lista de prefijos locales/).first()).toBeVisible()
    await expect(page.getByTestId('traffic-by-prefix')).toContainText('prefijos locales')
    await page.getByTestId('capacity-settings-open').click()
    await expect(page.getByTestId('settings-prefix-warnings')).toContainText('100.64.0.0/10')
  })

  test('el operador NOC ve la capacidad pero no configura', async ({ page }) => {
    await loginAsNoc(page)
    await page.goto(`${T}/capacity`)
    await expect(page.getByTestId('uplink-row').first()).toBeVisible()
    await expect(page.getByTestId('uplink-configure')).toHaveCount(0)
    await expect(page.getByTestId('capacity-settings-open')).toHaveCount(0)
  })

  test('widget «Salud de sitios / capacidad» en normal y mural', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(`${T}/dev/widgets`)
    const section = page.getByTestId('dev-widget-site_health')
    await section.scrollIntoViewIfNeeded()
    const w = section.locator('[data-testid="widget"]')
    await expect(w).toHaveAttribute('data-state', 'ready')
    await expect(w.getByTestId('site-health-summary')).toContainText('sanos')
    await expect(w.locator('li[data-health]').first()).not.toHaveAttribute('data-health', 'online')
  })
})

test.describe('capacidad sin desbordamiento @layout @capacity', () => {
  for (const width of [320, 390, 1440]) {
    test(`páginas de capacidad a ${width} px`, async ({ page }) => {
      test.setTimeout(120_000)
      await page.setViewportSize({ width, height: 900 })
      await page.emulateMedia({ reducedMotion: 'reduce' })
      await page.addInitScript(() => window.localStorage.setItem('horus.mock.findingEveryMs', '0'))
      await loginAsAdmin(page)
      for (const path of [`${T}/capacity`, `${T}/capacity/egress`]) {
        await page.goto(path)
        await expect(page.locator('#main-content')).toBeVisible()
        await page.waitForTimeout(1200)
        const problems = await layoutReport(page, {
          extra: ['[data-testid="app-navbar"]', '#main-content'],
        })
        expect(problems, `${path} a ${width}px`).toEqual([])
      }
      await page.goto(`${T}/capacity`)
      await page.getByTestId('uplink-link').first().click()
      await expect(page.getByTestId('detail-forecast')).toBeVisible()
      await page.waitForTimeout(1200)
      const problems = await layoutReport(page, {
        extra: ['[data-testid="app-navbar"]', '#main-content'],
      })
      expect(problems, `detalle a ${width}px`).toEqual([])
    })
  }

  for (const [width, height] of [
    [1920, 1080],
    [3840, 2160],
  ] as const) {
    test(`widget site_health mural ${width}×${height}`, async ({ page }) => {
      await page.setViewportSize({ width, height })
      await page.emulateMedia({ reducedMotion: 'reduce' })
      await loginAsAdmin(page)
      await page.goto(`${T}/dev/widgets?scale=wall`)
      const section = page.getByTestId('dev-widget-site_health')
      await section.scrollIntoViewIfNeeded()
      const w = section.locator('[data-testid="widget"]')
      await expect(w).toHaveAttribute('data-state', 'ready')
      await page.waitForTimeout(500)
      const visible = await w.locator('[data-fit-item]').evaluateAll((els) =>
        els
          .filter((e) => getComputedStyle(e).visibility !== 'hidden')
          .map((e) => ({
            h: e.getBoundingClientRect().height,
            sw: e.scrollWidth,
            cw: e.clientWidth,
          })),
      )
      expect(visible.length).toBeGreaterThan(0)
      for (const v of visible) expect(v.sw).toBeLessThanOrEqual(v.cw + 1)
    })
  }
})
