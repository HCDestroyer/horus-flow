import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'
import { layoutReport } from './layout-audit'

/** I1-17 · Tráfico: tops y series. `pnpm e2e --grep @traffic`. */

const PAGE = '/t/fibra-norte/traffic'

test.describe('tráfico @traffic', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('cambiar rango y nodo recalcula todo y queda en la URL (sin IPs)', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(PAGE)
    const widgets = page.locator('[data-testid="widget"]')
    await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(5)
    await expect(page.getByTestId('traffic-coverage')).toContainText('atribuidos a clientes')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('traffic-range').click()
    await page.getByRole('option', { name: 'Últimos 7 días' }).click()
    await expect(page).toHaveURL(/range=7d/)
    await page.getByTestId('traffic-site').click()
    await page.getByRole('option', { name: 'Nodo Norte' }).click()
    await expect(page).toHaveURL(/site=/)
    await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(5)
    await expect(widgets.first()).toContainText('7d')
    expect(new URL(page.url()).search).not.toMatch(/\d+\.\d+\.\d+\.\d+/)
    // Al recargar, el estado sale de la URL.
    await page.reload()
    await expect(page.getByTestId('traffic-range')).toContainText('Últimos 7 días')
  })

  test('desglose por organización y por ASN (pestaña y sentido en la URL)', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(PAGE)
    const breakdown = page.getByTestId('traffic-breakdown')
    const table = page.getByTestId('traffic-breakdown-table')
    // Por defecto, organizaciones.
    await expect(table).toContainText('Google')
    await expect(table).toContainText('Otros')
    await expect(table.getByRole('columnheader', { name: 'Organización' })).toBeVisible()

    await breakdown.getByRole('tab', { name: 'ASN' }).click()
    await expect(page).toHaveURL(/by=asns/)
    await expect(table.getByRole('columnheader', { name: 'ASN' })).toBeVisible()
    const asns = page.getByTestId('traffic-breakdown-asn')
    await expect(asns.first()).toHaveText(/^AS15169\s*Google LLC$/)
    await expect(table).toContainText('AS2906')
    // ASN sin nombre conocido: solo el número.
    await expect(asns.filter({ hasText: 'AS263237' })).toHaveText(/^AS263237$/)
    const firstRow = table.locator('tbody tr').first()
    await expect(firstRow).toContainText('↓')
    await expect(firstRow).toContainText('%')
    expect(await seriousViolations(page)).toEqual([])

    // El sentido reordena y queda en la URL junto a rango y nodo.
    await page.getByTestId('traffic-direction').click()
    await page.getByRole('option', { name: '↑ Subida' }).click()
    await expect(page).toHaveURL(/direction=upload/)
    await page.getByTestId('traffic-site').click()
    await page.getByRole('option', { name: 'Nodo Norte' }).click()
    await expect(page).toHaveURL(/site=/)
    await expect(asns.first()).toHaveText(/^AS\d+/)
    await page.reload()
    await expect(breakdown.getByRole('tab', { name: 'ASN' })).toHaveAttribute(
      'aria-selected',
      'true',
    )
    await expect(page.getByTestId('traffic-direction')).toContainText('Subida')
  })

  test('el tráfico fuera de los prefijos dice en qué rangos está y lleva a su nodo', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(PAGE)
    const rest = page.getByTestId('traffic-unattributed')
    await page.setViewportSize({ width: 390, height: 900 })
    await rest.getByTestId('traffic-unattributed-toggle').click()
    const row = rest.locator('[data-unattributed="100.64.12.0/22"]')
    await expect(row).toContainText('IP vistas')
    // Detalle por IP del rango (dato personal: la administradora puede verlo).
    await row.getByTestId('traffic-unattributed-ips').click()
    const ips = row.getByTestId('unattributed-addresses')
    await expect(ips.locator('[data-address="100.64.12.1"]')).toBeVisible()
    await expect(ips).toContainText('Cuota del rango')
    await row.getByTestId('traffic-unattributed-ips').click()
    await expect(ips).toHaveCount(0)
    // Abierta, la lista tampoco se corta ni se solapa en un móvil.
    expect(await layoutReport(page, { extra: ['#main-content'] })).toEqual([])
    await row.getByRole('link', { name: 'Revisar en el nodo' }).click()
    await expect(page).toHaveURL(/\/t\/fibra-norte\/nodes\/[0-9a-f-]+/)
  })

  test('explorador: servicios, categorías y clientes, y «Mostrar más» hasta 100', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(`${PAGE}?by=services`)
    const breakdown = page.getByTestId('traffic-breakdown')
    const table = page.getByTestId('traffic-breakdown-table')
    await expect(table.getByRole('columnheader', { name: 'Servicio' })).toBeVisible()
    await expect(breakdown).toContainText('Top 10 por')
    await page.getByTestId('traffic-breakdown-more').click()
    await expect(breakdown).toContainText('Top 25 por')
    await breakdown.getByRole('tab', { name: 'Clientes' }).click()
    await expect(page).toHaveURL(/by=customers/)
    await expect(table.getByRole('columnheader', { name: 'Cliente' })).toBeVisible()
    await expect(table.locator('tbody tr').first()).toContainText('↓')
  })

  test('sin dataset de ASN: avisa de que el tráfico se clasifica solo por puerto', async ({
    page,
  }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.scenario', 'no-asn'))
    await loginAsAdmin(page)
    await page.goto(PAGE)
    await expect(page.getByTestId('traffic-classification')).toContainText('solo por puerto')
  })

  test('meta.partial muestra "Datos incompletos en este rango"', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(`${PAGE}?range=30d`)
    await expect(page.getByTestId('traffic-partial')).toContainText(
      'Datos incompletos en este rango',
    )
  })

  test('analítica no disponible: aviso de degradado y el resto de la app funciona', async ({
    page,
  }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.scenario', 'degraded'))
    await loginAsAdmin(page)
    await page.goto(PAGE)
    await expect(page.getByTestId('degraded-notice')).toContainText('Analítica no disponible')
    await expect(
      page.locator('[data-testid="widget"][data-state="degraded"]').first(),
    ).toBeVisible()
    // La navegación y otras secciones siguen funcionando.
    await page.getByTestId('main-nav').getByRole('link', { name: 'Hallazgos' }).click()
    await expect(page.getByTestId('findings-table')).toBeVisible()
  })
})
