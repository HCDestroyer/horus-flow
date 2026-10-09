import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { ADMIN, fillCredentials, loginAsAdmin, loginAsNoc, setTheme } from './helpers'
import { layoutReport } from './layout-audit'
import {
  DEFAULT_NAV_INCREMENT,
  isIncrementVisible,
  NAV_SECTIONS,
  sectionHref,
} from '../../app/utils/navigation'

/**
 * Recorrido completo de la UI para revisión humana: una captura por pantalla y sección.
 * `pnpm exec playwright test --grep @tour` → docs/screenshots/tour/*.png. No forma parte del humo.
 * Cada captura de dashboard pasa además la auditoría "sin desbordamiento" (ver @layout).
 */
const dir = resolve(import.meta.dirname, '../../docs/screenshots/tour')
mkdirSync(dir, { recursive: true })

const SLUG = 'fibra-norte'
const NOC_DASHBOARD = `/t/${SLUG}/dashboards/0192f000-0000-7000-8000-00000000d001`
const SECURITY_DASHBOARD = `/t/${SLUG}/dashboards/0192f000-0000-7000-8000-00000000d002`

/** Secciones del incremento entregado (las futuras no existen en la UI: dan 404). */
const SECTIONS = NAV_SECTIONS.filter(
  (s) => s.id !== 'overview' && isIncrementVisible(s.increment, DEFAULT_NAV_INCREMENT),
)
const FUTURE = NAV_SECTIONS.find((s) => !isIncrementVisible(s.increment, DEFAULT_NAV_INCREMENT))!

async function prepare(page: Page, theme: 'light' | 'dark') {
  await setTheme(page, theme)
  await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' })
}

async function shot(page: Page, name: string, wait = 1200) {
  await page.waitForLoadState('load')
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${dir}/${name}.png` })
}

/** Dashboard listo (todos sus widgets con datos o sin datos propios) y sin desbordamientos. */
async function dashboardShot(page: Page, name: string, widgets: number, wall = false) {
  await expect(page.locator('[data-testid="widget"][data-state="ready"]')).toHaveCount(widgets)
  await shot(page, name, 3000)
  const extra = wall ? [] : ['[data-testid="app-navbar"]']
  expect(await layoutReport(page, { extra, noScroll: wall }), name).toEqual([])
}

test.describe('recorrido completo @tour', () => {
  test.describe.configure({ mode: 'serial', timeout: 300_000 })
  test.use({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 })

  test('acceso', async ({ page }) => {
    await prepare(page, 'dark')
    await page.goto('/login')
    await expect(page.getByTestId('login-card')).toBeVisible()
    await shot(page, '01-login')
    await fillCredentials(page, ADMIN)
    await expect(page.getByLabel('Código de verificación')).toBeVisible()
    await shot(page, '02-login-totp')
  })

  test('secciones del ISP y de plataforma (administradora)', async ({ page }) => {
    await prepare(page, 'dark')
    await loginAsAdmin(page)
    await shot(page, '03-resumen')

    let n = 4
    const next = () => String(n++).padStart(2, '0')
    for (const section of SECTIONS) {
      await page.goto(sectionHref(section, SLUG))
      await shot(page, `${next()}-${section.id}`)
    }
    // Una sección de un incremento posterior: fuera del menú y su URL da "No encontrado".
    await page.goto(sectionHref(FUTURE, SLUG))
    await expect(page.getByTestId('error-page')).toContainText('No encontrado')
    await shot(page, `${next()}-seccion-futura-${FUTURE.id}-no-encontrada`)

    await page.goto(NOC_DASHBOARD)
    await dashboardShot(page, `${next()}-dashboard-noc`, 9)
    await page.goto(`${NOC_DASHBOARD}?scale=wall`)
    await dashboardShot(page, `${next()}-dashboard-noc-mural`, 9, true)
    await page.goto(SECURITY_DASHBOARD)
    await dashboardShot(page, `${next()}-dashboard-seguridad`, 7)
    await page.goto(`/t/${SLUG}/dev/widgets`)
    await shot(page, `${next()}-galeria-widgets`, 6000)
    await page.goto('/account')
    await shot(page, `${next()}-cuenta`)
    await page.goto('/t/isp-que-no-existe')
    await shot(page, `${next()}-isp-no-encontrado`)
  })

  test('vista mural a varias resoluciones', async ({ page }) => {
    await prepare(page, 'dark')
    await loginAsAdmin(page)
    const sizes = [
      [1280, 720],
      [1920, 1080],
      [3840, 2160],
    ] as const
    let n = 30
    for (const [width, height] of sizes) {
      await page.setViewportSize({ width, height })
      await page.goto(`${NOC_DASHBOARD}?scale=wall`)
      await dashboardShot(page, `${n++}-mural-noc-${width}x${height}`, 9, true)
      await page.goto(`${SECURITY_DASHBOARD}?scale=wall`)
      await dashboardShot(page, `${n++}-mural-seguridad-${width}x${height}`, 7, true)
    }
  })

  test('vista del rol NOC y tema claro', async ({ page }) => {
    await prepare(page, 'light')
    await loginAsNoc(page)
    await shot(page, '40-noc-resumen-claro')
    await page.goto(SECURITY_DASHBOARD)
    await dashboardShot(page, '41-noc-dashboard-seguridad-claro', 7)
    await page.goto(`/t/${SLUG}/clients`)
    await shot(page, '42-noc-sin-permiso-clientes')
  })

  test('móvil (390 y 320 px)', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await prepare(page, 'dark')
    await loginAsAdmin(page)
    await shot(page, '50-movil-resumen')
    await page.goto(NOC_DASHBOARD)
    await dashboardShot(page, '51-movil-dashboard-noc', 9)
    await page.locator('[data-widget-id="w-exporters"]').scrollIntoViewIfNeeded()
    await shot(page, '55-movil-dashboard-noc-exportadores', 500)
    await page.goto(SECURITY_DASHBOARD)
    await dashboardShot(page, '52-movil-dashboard-seguridad', 7)
    await page.setViewportSize({ width: 320, height: 720 })
    await page.goto(NOC_DASHBOARD)
    await dashboardShot(page, '53-movil-320-dashboard-noc', 9)
    await page.goto(SECURITY_DASHBOARD)
    await dashboardShot(page, '54-movil-320-dashboard-seguridad', 7)
  })

  test('pantallas de I1 (clientes, hallazgos, router, plataforma)', async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem('horus.mock.findingEveryMs', '0')
      window.localStorage.setItem('horus.mock.onboardingStepMs', '60000')
    })
    await prepare(page, 'dark')
    await loginAsAdmin(page)
    const T = `/t/${SLUG}`
    const shots: [string, string][] = [
      ['60-cliente-infectado', `${T}/clients/0193c000-0000-7000-8000-000000100001`],
      ['61-cliente-ipv6', `${T}/clients/0193c000-0000-7000-8000-000000100040`],
      [
        '62-cliente-comercial-historial',
        `${T}/clients/0193c000-0000-7000-8000-000000100005?tab=history`,
      ],
      ['63-cliente-inactivo', `${T}/clients/0193c000-0000-7000-8000-000000100046`],
      ['64-hallazgo-detalle', `${T}/security/findings/0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f00`],
      ['65-hallazgo-c2', `${T}/security/findings/0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f01`],
      ['66-nodo-descubrimiento', `${T}/nodes/0192e111-0000-7000-8000-000000000105`],
      ['67-router-prefijos', `${T}/routers/0192e333-0000-7000-8000-000000000101/prefixes`],
      ['68-router-silencioso', '/t/valle-conecta/routers/0192e333-0000-7000-8000-000000000202'],
      ['69-trafico-30d-parcial', `${T}/traffic?range=30d`],
    ]
    for (const [name, url] of shots) {
      await page.goto(url)
      await shot(page, name, 2500)
    }
    await page.goto(`${T}/routers/0192e333-0000-7000-8000-000000000106/connection`)
    await page.getByTestId('generate-script').click()
    await expect(page.getByTestId('provisioning-script')).toBeVisible()
    await shot(page, '70-router-onboarding-script', 800)
    await page.goto(`${T}/routers/0192e333-0000-7000-8000-000000000101/prefixes`)
    await page.getByTestId('import-prefixes').click()
    await expect(page.getByTestId('import-preview')).toBeVisible()
    await shot(page, '71-importar-mikrotik-ipv6', 800)
    await page.goto(`${T}/security/findings/0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f02`)
    await page.getByTestId('false-positive').click()
    await shot(page, '72-falso-positivo', 800)
    await page.goto(`${T}/admin/kiosks`)
    await page.locator('[data-kiosk="Pantalla de recepción"]').getByTestId('generate-code').click()
    await expect(page.getByTestId('enrollment-code')).toBeVisible()
    await shot(page, '73-kiosco-codigo', 800)
  })

  test('kiosco en una TV 1920×1080', async ({ page, context }) => {
    await prepare(page, 'dark')
    await loginAsAdmin(page)
    await page.goto(`/t/${SLUG}/admin/kiosks`)
    await page.locator('[data-kiosk="Pantalla de recepción"]').getByTestId('generate-code').click()
    const code = (await page.getByTestId('enrollment-code').textContent())!.trim()
    const tv = await context.newPage()
    await tv.setViewportSize({ width: 1920, height: 1080 })
    await tv.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' })
    await tv.goto('/kiosk')
    await tv.getByTestId('kiosk-code').fill('ZZZZ9999')
    await tv.getByTestId('kiosk-submit').click()
    await expect(tv.getByTestId('kiosk-enroll-error')).not.toBeEmpty()
    await shot(tv, '74-kiosco-codigo-erroneo', 500)
    await tv.getByTestId('kiosk-code').fill(code)
    await tv.getByTestId('kiosk-submit').click()
    await expect(
      tv.getByTestId('kiosk-current').locator('[data-testid="widget"][data-state="ready"]'),
    ).toHaveCount(9)
    await shot(tv, '75-kiosco-noc', 3000)
    expect(await layoutReport(tv, { noScroll: true }), 'kiosco').toEqual([])
    await tv.keyboard.press('ArrowRight')
    await expect(
      tv.getByTestId('kiosk-current').locator('[data-testid="widget"][data-state="ready"]'),
    ).toHaveCount(7)
    await shot(tv, '76-kiosco-seguridad', 3000)
  })

  test('móvil y tema claro de I1', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.findingEveryMs', '0'))
    await page.setViewportSize({ width: 390, height: 844 })
    await prepare(page, 'light')
    await loginAsAdmin(page)
    const T = `/t/${SLUG}`
    const shots: [string, string][] = [
      ['80-movil-clientes-claro', `${T}/clients`],
      ['81-movil-hallazgos-claro', `${T}/security/findings`],
      ['82-movil-hallazgo-claro', `${T}/security/findings/0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f00`],
      ['83-movil-nodos-claro', `${T}/nodes`],
      ['84-movil-onboarding-claro', `${T}/routers/0192e333-0000-7000-8000-000000000106/connection`],
      ['85-movil-trafico-claro', `${T}/traffic`],
    ]
    for (const [name, url] of shots) {
      await page.goto(url)
      await shot(page, name, 2500)
    }
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto('/platform/system')
    await shot(page, '86-estado-del-sistema-claro', 1500)
    await page.goto(`${T}/clients/0193c000-0000-7000-8000-000000100001`)
    await shot(page, '87-cliente-claro', 2000)
  })
})
