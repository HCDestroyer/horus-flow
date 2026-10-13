import { expect, test, type Page } from '@playwright/test'

/**
 * Pantalla NOC ligera (public/kiosk-lite.html, ES5) para TVs antiguas. La API se simula con
 * page.route: el mock de la SPA no atiende a esta página, que no carga la app.
 * `pnpm e2e --grep @kiosk-lite`.
 */
const WALL = { width: 1920, height: 1080 }
const DASH = '0192f000-0000-7000-8000-00000000d001'

async function fakeApi(
  page: Page,
  opts: { items?: { dashboard_id: string; duration_seconds: number }[] } = {},
) {
  let enrolled = false
  const items = opts.items ?? [{ dashboard_id: DASH, duration_seconds: 30 }]
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    const json = (body: unknown, status = 200) =>
      route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    if (path === '/kiosk/enroll') {
      const code = JSON.parse(route.request().postData() ?? '{}').code
      if (code !== 'ABCD2345') return json({ code: 'KIOSK_ENROLLMENT_CODE_INVALID' }, 422)
      enrolled = true
      return route.fulfill({ status: 204 })
    }
    if (path === '/kiosk/token') {
      if (!enrolled) return json({ code: 'UNAUTHENTICATED' }, 401)
      return json({
        access_token: 'jwt',
        expires_at: new Date(Date.now() + 600_000).toISOString(),
        tenant_id: 't',
      })
    }
    if (path === '/kiosk/config')
      return json({ tenant_name: 'Fibra Norte', transition: 'fade', items })
    if (path === `/dashboards/${DASH}`)
      return json({
        id: DASH,
        name: 'NOC del ISP',
        widgets: [
          { id: 'w-header', type: 'noc_header', title: '', position: { x: 0, y: 0, w: 12, h: 1 } },
          {
            id: 'w-traffic-now',
            type: 'traffic_now',
            title: 'Tráfico ahora',
            position: { x: 0, y: 1, w: 4, h: 2 },
          },
          {
            id: 'w-customers',
            type: 'customers_active',
            title: 'Clientes activos',
            position: { x: 4, y: 1, w: 3, h: 2 },
          },
          {
            id: 'w-findings',
            type: 'findings_summary',
            title: 'Hallazgos abiertos',
            position: { x: 7, y: 1, w: 5, h: 2 },
          },
          {
            id: 'w-exporters',
            type: 'exporters_status',
            title: 'Exportadores',
            position: { x: 0, y: 3, w: 12, h: 2 },
          },
          {
            id: 'w-traffic-24h',
            type: 'traffic_timeseries',
            title: 'Tráfico 24 h',
            position: { x: 0, y: 5, w: 8, h: 4 },
          },
          {
            id: 'w-top-categories',
            type: 'top_categories',
            title: 'Top categorías',
            position: { x: 8, y: 5, w: 4, h: 4 },
          },
          {
            id: 'w-feed',
            type: 'findings_feed',
            title: 'Hallazgos recientes',
            position: { x: 0, y: 9, w: 12, h: 3 },
          },
        ],
      })
    const now = new Date().toISOString()
    const meta = {
      widget_type: 'x',
      data_endpoint_kind: 'state',
      generated_at: now,
      partial: false,
      masked_personal_data: true,
    }
    const state = (values: unknown) => json({ data: { kind: 'state', values }, meta })
    const table = (rows: unknown[]) => json({ data: { kind: 'table', columns: [], rows }, meta })
    if (path.endsWith('/w-traffic-now/data'))
      return state({
        down_bps: 2_400_000_000,
        up_bps: 310_000_000,
        down_bps_yesterday: 2_000_000_000,
        up_bps_yesterday: 310_000_000,
        flows_per_second: 2184,
        sparkline: { step_seconds: 60, down_bps: [], up_bps: [] },
      })
    if (path.endsWith('/w-customers/data'))
      return state({ active: 1834, new_today: 12, total: 2010 })
    if (path.endsWith('/w-findings/data'))
      return state({
        open_total: 7,
        open_by_severity: { critical: 1, high: 2, medium: 4 },
        new_last_24h: 3,
        affected_customers: 5,
        by_security_state: { infected: 1, suspected: 4 },
      })
    if (path.endsWith('/w-exporters/data'))
      return table([
        {
          router: 'TC-MK',
          site: 'Centro',
          state: 'exporting',
          state_since: now,
          last_flow_at: now,
          flows_per_second: 2184,
          loss_ratio: 0,
        },
        {
          router: 'CDN',
          site: 'Norte',
          state: 'silent',
          state_since: now,
          last_flow_at: null,
          flows_per_second: null,
          loss_ratio: null,
        },
      ])
    if (path.endsWith('/w-traffic-24h/data')) {
      const t = (h: number) => new Date(Date.now() - h * 3_600_000).toISOString()
      return json({
        data: {
          kind: 'series',
          series: [
            {
              metric: 'down_bps',
              unit: 'bps',
              points: [
                [t(3), 1e9],
                [t(2), null],
                [t(1), 2e9],
                [t(0), 2.4e9],
              ],
            },
            {
              metric: 'up_bps',
              unit: 'bps',
              points: [
                [t(3), 2e8],
                [t(2), 3e8],
                [t(1), 3e8],
                [t(0), 3.1e8],
              ],
            },
          ],
        },
        meta,
      })
    }
    if (path.endsWith('/w-top-categories/data'))
      return table([
        { label: 'Streaming de video', down_bytes: 5_000_000_000, up_bytes: 200_000_000 },
        { label: 'Videojuegos', down_bytes: 800_000_000, up_bytes: 100_000_000 },
      ])
    if (path.endsWith('/w-feed/data'))
      return table([
        {
          id: 'f1',
          severity: 'critical',
          kind: 'c2_contact',
          summary: 'Contacto con servidor de control',
          customer_ip: '10.0.x.x',
          alias: 'Cliente 42',
          site: 'Centro',
          security_state: 'infected',
          confidence: 0.9,
          last_seen_at: new Date(Date.now() - 5 * 60_000).toISOString(),
        },
      ])
    return json({ code: 'NOT_FOUND' }, 404)
  })
}

test.describe('pantalla NOC ligera @kiosk-lite', () => {
  test('el enlace con código conecta la TV y dibuja la distribución del NOC', async ({ page }) => {
    await page.setViewportSize(WALL)
    await fakeApi(page)
    await page.goto('/kiosk-lite.html#code=ABCD2345')
    await expect(page.locator('#player')).toBeVisible()
    await expect(page.locator('#dash-name')).toHaveText('NOC del ISP')
    await expect(page.locator('#tenant')).toHaveText('Fibra Norte')
    const grid = page.locator('#grid')
    await expect(grid).toContainText('2.4 Gbps')
    await expect(grid).toContainText('vs. ayer ↓ +20 %')
    await expect(grid).toContainText('1.834')
    await expect(grid).toContainText('+12 nuevos hoy')
    await expect(grid).toContainText('Crítica1')
    await expect(grid).toContainText('1 de 2 exportando')
    await expect(grid).toContainText('Streaming de video')
    await expect(grid).toContainText('5.2 GB')
    await expect(grid).toContainText('hace 5 min')
    // Dos series; la bajada tiene un hueco (null) → dos tramos.
    await expect(page.locator('#grid svg polyline:not(.glow)')).toHaveCount(3)
    await expect(page.locator('#updated')).toContainText('Actualizado')
    expect(page.url()).not.toContain('code=')
    // Rejilla de 12 columnas: la cabecera no ocupa tarjeta, nada se solapa ni se sale.
    const boxes = await page
      .locator('#grid > .card')
      .evaluateAll((els) => els.map((e) => e.getBoundingClientRect().toJSON() as DOMRect))
    expect(boxes).toHaveLength(7)
    for (const [i, a] of boxes.entries()) {
      expect(a.right).toBeLessThanOrEqual(WALL.width + 1)
      expect(a.bottom).toBeLessThanOrEqual(WALL.height + 1)
      for (const b of boxes.slice(i + 1)) {
        const overlap =
          Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1 &&
          Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1
        expect(overlap).toBe(false)
      }
    }
    // Tráfico ahora (w4) a la izquierda de Clientes (w3), en la misma fila.
    expect(Math.abs(boxes[0]!.top - boxes[1]!.top)).toBeLessThan(2)
    expect(boxes[0]!.width / boxes[1]!.width).toBeCloseTo(4 / 3, 1)
  })

  for (const vp of [
    { name: 'tablet', width: 1024, height: 768, cols: 2 },
    { name: 'móvil vertical', width: 390, height: 844, cols: 1 },
  ]) {
    test(`se reacomoda en ${vp.name} sin scroll horizontal`, async ({ page }) => {
      await page.setViewportSize({ width: vp.width, height: vp.height })
      await fakeApi(page)
      await page.goto('/kiosk-lite.html#code=ABCD2345')
      await expect(page.locator('#grid')).toContainText('Streaming de video')
      const lefts = await page
        .locator('#grid > .card')
        .evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().left)))
      expect(new Set(lefts).size).toBe(vp.cols)
      const sw = await page.evaluate(() => document.documentElement.scrollWidth)
      expect(sw).toBeLessThanOrEqual(vp.width)
      await expect(page.locator('#grid svg polyline:not(.glow)')).toHaveCount(3)
    })
  }

  test('código erróneo: mensaje; sin dashboards: lo dice en vez de quedar en negro', async ({
    page,
  }) => {
    await page.setViewportSize(WALL)
    await fakeApi(page, { items: [] })
    await page.goto('/kiosk-lite.html')
    await expect(page.locator('#enroll')).toBeVisible()
    await page.locator('#code').fill('ZZZZ9999')
    await page.locator('#enroll-submit').click()
    await expect(page.locator('#enroll-error')).toContainText('no es válido')
    await page.locator('#code').fill('ABCD2345')
    await page.locator('#enroll-submit').click()
    await expect(page.locator('#empty')).toContainText('no tiene dashboards')
  })

  test('/kiosk en un navegador sin CSS moderno va a la versión ligera conservando el código', async ({
    page,
  }) => {
    await page.setViewportSize(WALL)
    await page.addInitScript(() => {
      const orig = CSS.supports.bind(CSS)
      CSS.supports = ((a: string, b?: string) =>
        typeof b === 'string' && b.includes('oklch')
          ? false
          : b === undefined
            ? orig(a)
            : orig(a, b)) as typeof CSS.supports
    })
    await fakeApi(page)
    await page.goto('/kiosk/enroll#code=ABCD2345')
    await expect(page).toHaveURL(/\/kiosk-lite\.html/)
    await expect(page.locator('#dash-name')).toHaveText('NOC del ISP')
  })
})
