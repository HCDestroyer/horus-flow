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
          { id: 'w-traffic-now', type: 'traffic_now', title: 'Tráfico ahora' },
          { id: 'w-traffic-24h', type: 'traffic_timeseries', title: 'Tráfico 24 h' },
          { id: 'w-top-categories', type: 'top_categories', title: 'Top categorías' },
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
    if (path.endsWith('/w-traffic-now/data'))
      return json({
        data: { kind: 'state', values: { download_bps: 2_400_000_000, upload_bps: 310_000_000 } },
        meta,
      })
    if (path.endsWith('/w-traffic-24h/data'))
      return json({
        data: {
          kind: 'series',
          series: [
            {
              metric: 'download_bps',
              unit: 'bps',
              points: [
                [now, 1e9],
                [now, null],
                [now, 2e9],
              ],
            },
          ],
        },
        meta,
      })
    if (path.endsWith('/w-top-categories/data'))
      return json({
        data: {
          kind: 'table',
          columns: [
            { key: 'label', type: 'string' },
            { key: 'bytes', type: 'bytes' },
          ],
          rows: [
            { label: 'Streaming de video', bytes: 5_200_000_000 },
            { label: 'Videojuegos', bytes: 900_000_000 },
          ],
        },
        meta,
      })
    return json({ code: 'NOT_FOUND' }, 404)
  })
}

test.describe('pantalla NOC ligera @kiosk-lite', () => {
  test('el enlace con código conecta la TV y dibuja estado, serie y tabla', async ({ page }) => {
    await page.setViewportSize(WALL)
    await fakeApi(page)
    await page.goto('/kiosk-lite.html#code=ABCD2345')
    await expect(page.locator('#player')).toBeVisible()
    await expect(page.locator('#dash-name')).toHaveText('NOC del ISP')
    await expect(page.locator('#tenant')).toHaveText('Fibra Norte')
    await expect(page.locator('#grid')).toContainText('2.4 Gbps')
    await expect(page.locator('#grid')).toContainText('Streaming de video')
    await expect(page.locator('#grid')).toContainText('5.2 GB')
    await expect(page.locator('#grid svg polyline')).toHaveCount(1)
    expect(page.url()).not.toContain('code=')
  })

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
