import { expect, test, type Page } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, seriousViolations } from './helpers'

/** E2E de I0-16: marco de widgets, plantillas y estados por widget. */

const NOC = '/t/fibra-norte/dashboards/0192f000-0000-7000-8000-00000000d001'
const SECURITY = '/t/fibra-norte/dashboards/0192f000-0000-7000-8000-00000000d002'

const widget = (page: Page, id: string) => page.locator(`[data-widget-id="${id}"]`)

async function scenario(page: Page, name: string) {
  await page.addInitScript((value) => localStorage.setItem('horus.mock.scenario', value), name)
}

test.describe('dashboards @widgets', () => {
  test.use({ viewport: { width: 1600, height: 1000 } })

  test('la plantilla "NOC del ISP" renderiza cada widget en su posición de la grilla', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto('/t/fibra-norte/dashboards')
    await page.getByRole('link', { name: /NOC del ISP/ }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'NOC del ISP' })).toBeVisible()
    const widgets = page.getByTestId('widget')
    await expect(widgets).toHaveCount(9)
    await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(9)
    await expect(page.getByTestId('dashboard-grid')).toHaveAttribute('data-grid-mode', 'designed')
    // top_categories: x=8, y=5, w=4, h=4 (C9 `position`).
    const style = await widget(page, 'w-top-categories').evaluate((el) => ({
      col: getComputedStyle(el).gridColumnStart,
      span: getComputedStyle(el).gridColumnEnd,
      row: getComputedStyle(el).gridRowStart,
    }))
    expect(style).toEqual({ col: '9', span: 'span 4', row: '6' })
    await expect(page.getByTestId('noc-header-tenant')).toHaveText('Fibra Norte')
    expect(await seriousViolations(page)).toEqual([])
  })

  test('la plantilla "Seguridad" muestra "Infectado" con su confianza (D18)', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(SECURITY)
    await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(7)
    const feed = widget(page, 'w-findings-feed')
    await expect(feed.locator('[data-security-state="infected"]').first()).toContainText(
      /Infectado · confianza \d+/,
    )
    expect(await seriousViolations(page)).toEqual([])
  })

  test('sin el permiso del tipo, solo ese widget dice "No tienes acceso"', async ({ page }) => {
    await loginAsNoc(page)
    await page.goto(NOC)
    await expect(widget(page, 'w-top-customers')).toHaveAttribute('data-state', 'forbidden')
    await expect(widget(page, 'w-top-customers')).toContainText('No tienes acceso a este widget')
    await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(8)
    // Sin customers.read, las IP del feed llegan enmascaradas.
    await expect(widget(page, 'w-findings-feed')).toContainText('•••')
  })

  test('si la fuente de un widget falla, solo él muestra el error con "Reintentar"', async ({
    page,
  }) => {
    await scenario(page, 'widget-error')
    await loginAsAdmin(page)
    await page.goto(NOC)
    const broken = widget(page, 'w-traffic-24h')
    await expect(broken).toHaveAttribute('data-state', 'error')
    await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(8)
    await broken.getByRole('button', { name: 'Reintentar' }).click()
    await expect(broken).toHaveAttribute('data-state', 'ready')
  })

  test('con la analítica caída, los widgets de tráfico se ven degradados y el resto sigue', async ({
    page,
  }) => {
    await scenario(page, 'degraded')
    await loginAsAdmin(page)
    await page.goto(NOC)
    await expect(widget(page, 'w-traffic-24h')).toHaveAttribute('data-state', 'degraded')
    await expect(widget(page, 'w-traffic-24h')).toContainText('Analítica no disponible')
    await expect(widget(page, 'w-exporters')).toHaveAttribute('data-state', 'ready')
    await expect(widget(page, 'w-exporters')).toContainText('Silencioso')
  })

  test('sin datos, cada widget muestra su estado vacío', async ({ page }) => {
    await scenario(page, 'empty')
    await loginAsAdmin(page)
    await page.goto(SECURITY)
    await expect(widget(page, 'w-findings-feed')).toHaveAttribute('data-state', 'empty')
    await expect(widget(page, 'w-findings-summary')).toContainText(
      'Ningún cliente muestra señales de botnet',
    )
  })

  test('un dato en vivo muestra su frescura ("En vivo · hace N s")', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(NOC)
    await expect(widget(page, 'w-traffic-now').getByTestId('freshness')).toContainText(
      /En vivo · hace \d+ s/,
      { timeout: 12_000 },
    )
    await expect(page.getByTestId('noc-header-updated')).toContainText(/Actualizado\s*hace \d+ s/)
  })

  test('al cambiar de ISP el dashboard muestra los datos del ISP nuevo', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(NOC)
    await expect(page.getByTestId('noc-header-tenant')).toHaveText('Fibra Norte')
    await page.getByTestId('tenant-switcher').click()
    await page.getByRole('menuitemcheckbox', { name: 'Valle Conecta' }).click()
    await expect(page).toHaveURL(/\/t\/valle-conecta\/dashboards$/)
    await page.getByRole('link', { name: /NOC del ISP/ }).click()
    await expect(page.getByTestId('noc-header-tenant')).toHaveText('Valle Conecta')
    // Valle Conecta tiene 2 nodos: los exportadores son los suyos, no los de Fibra Norte.
    await expect(widget(page, 'w-exporters').locator('[data-exporter-state]')).toHaveCount(2)
  })
})

test.describe('vista mural @widgets', () => {
  test.use({ viewport: { width: 1920, height: 1080 } })

  test('escala mural: sin barra lateral ni scroll, tipografía legible a distancia', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(`${NOC}?scale=wall`)
    await expect(page.getByTestId('dashboard-wall')).toBeVisible()
    await expect(page.locator('#app-sidebar')).toHaveCount(0)
    await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(9)

    const metrics = await page.evaluate(() => {
      const grid = document.querySelector('[data-testid="dashboard-grid"]')!
      const sizes = (sel: string) =>
        [...grid.querySelectorAll(sel)].map((el) => parseFloat(getComputedStyle(el).fontSize))
      // Texto visible más pequeño dentro de los widgets (sin los ocultos para lectores).
      const texts = [...grid.querySelectorAll<HTMLElement>('*')].filter(
        (el) =>
          [...el.childNodes].some((n) => n.nodeType === 3 && n.textContent!.trim()) &&
          el.offsetParent !== null &&
          !el.closest('.sr-only'),
      )
      return {
        titles: Math.min(...sizes('.w-title')),
        kpis: Math.min(...sizes('.w-kpi')),
        minText: Math.min(...texts.map((el) => parseFloat(getComputedStyle(el).fontSize))),
        overflow: document.documentElement.scrollHeight - window.innerHeight,
      }
    })
    expect(metrics.titles).toBeGreaterThanOrEqual(28)
    expect(metrics.kpis).toBeGreaterThanOrEqual(72)
    expect(metrics.minText).toBeGreaterThanOrEqual(20)
    expect(metrics.overflow).toBeLessThanOrEqual(0)

    await page.keyboard.press('Escape')
    await expect(page.getByRole('heading', { level: 1, name: 'NOC del ISP' })).toBeVisible()
  })

  test('la galería /dev/widgets muestra cada tipo registrado', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/t/fibra-norte/dev/widgets')
    for (const type of ['noc_header', 'traffic_now', 'botnet_signals', 'watched_ports']) {
      await expect(page.getByTestId(`dev-widget-${type}`)).toBeVisible()
    }
  })
})
