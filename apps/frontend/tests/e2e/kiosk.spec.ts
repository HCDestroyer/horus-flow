import { expect, test, type BrowserContext, type Page } from '@playwright/test'
import { loginAsAdmin } from './helpers'

/**
 * I1-21 · Modo kiosco: enrolamiento por código, reproducción sin interacción, rotación,
 * corte de red, nueva versión, revocación y 24 h con reloj acelerado.
 * `pnpm e2e --grep @kiosk`.
 */

const KIOSKS = '/t/fibra-norte/admin/kiosks'
const WALL = { width: 1920, height: 1080 }

async function codeFor(admin: Page, name = 'Pantalla de recepción') {
  await admin.goto(KIOSKS)
  await admin.locator(`[data-kiosk="${name}"]`).getByTestId('generate-code').click()
  const code = (await admin.getByTestId('enrollment-code').textContent())!.trim()
  await admin.keyboard.press('Escape')
  return code
}

async function openKiosk(context: BrowserContext, clock = false) {
  const tv = await context.newPage()
  await tv.setViewportSize(WALL)
  await tv.emulateMedia({ reducedMotion: 'reduce' })
  if (clock) await tv.clock.install()
  await tv.goto('/kiosk')
  return tv
}

async function enroll(tv: Page, code: string) {
  await tv.getByTestId('kiosk-code').fill(code)
  await tv.getByTestId('kiosk-submit').click()
  await expect(tv.getByTestId('kiosk-current')).toBeVisible()
  await expect(
    tv.getByTestId('kiosk-current').locator('[data-testid="widget"][data-state="ready"]'),
  ).toHaveCount(9)
}

test.describe('modo kiosco @kiosk', () => {
  test.describe.configure({ timeout: 180_000 })

  test('código erróneo: mensaje legible; código correcto: dashboard oscuro sin controles', async ({
    page,
    context,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await loginAsAdmin(page)
    const code = await codeFor(page)
    const tv = await openKiosk(context)
    await expect(tv.getByTestId('kiosk-enroll')).toBeVisible()
    await tv.getByTestId('kiosk-code').fill('ZZZZ9999')
    await tv.getByTestId('kiosk-submit').click()
    const error = tv.getByTestId('kiosk-enroll-error')
    await expect(error).toContainText('no es válido')
    // Legible a distancia: ≥ 28 px a 1080p.
    const size = await error.evaluate((el) => parseFloat(getComputedStyle(el).fontSize))
    expect(size).toBeGreaterThanOrEqual(28)

    await enroll(tv, code)
    await expect(tv.locator('html')).toHaveClass(/dark/)
    await expect(tv.getByTestId('main-nav')).toHaveCount(0)
    await expect(tv.getByRole('button', { name: /cerrar sesión|salir/i })).toHaveCount(0)
    // El admin la ve en línea.
    await page.goto(KIOSKS)
    await expect(
      page.locator('[data-kiosk="Pantalla de recepción"]').getByTestId('kiosk-presence'),
    ).toHaveText('En línea')
  })

  test('rota según la playlist, precarga el siguiente y Espacio pausa', async ({
    page,
    context,
  }) => {
    await loginAsAdmin(page)
    const code = await codeFor(page)
    const tv = await openKiosk(context, true)
    await enroll(tv, code)
    const first = await tv.getByTestId('kiosk-current').getAttribute('data-dashboard')
    // El siguiente ya está montado (precargado) antes del cambio.
    await expect(
      tv.getByTestId('kiosk-next').locator('[data-testid="widget"][data-state="ready"]'),
    ).toHaveCount(7)
    await expect(tv.getByTestId('noc-header-rotation')).toContainText('1/2')
    await tv.clock.fastForward(31_000)
    await expect(tv.getByTestId('kiosk-current')).not.toHaveAttribute('data-dashboard', first!)
    // Sin esqueleto al cambiar: todos sus widgets ya tienen datos.
    await expect(
      tv.getByTestId('kiosk-current').locator('[data-testid="widget"][data-state="loading"]'),
    ).toHaveCount(0)
    await tv.keyboard.press(' ')
    await expect(tv.getByTestId('noc-header-rotation')).toContainText('En pausa')
    const paused = await tv.getByTestId('kiosk-current').getAttribute('data-dashboard')
    await tv.clock.fastForward(61_000)
    await expect(tv.getByTestId('kiosk-current')).toHaveAttribute('data-dashboard', paused!)
  })

  test('corte de red: mantiene los datos con "Sin conexión desde" y se recupera sola', async ({
    page,
    context,
  }) => {
    await loginAsAdmin(page)
    const code = await codeFor(page)
    const tv = await openKiosk(context, true)
    await enroll(tv, code)
    await tv.keyboard.press(' ') // sin rotación: misma pantalla durante el corte
    await tv.evaluate(() => window.localStorage.setItem('horus.mock.offline', '1'))
    for (let i = 0; i < 10; i++) {
      await tv.clock.fastForward(30_000)
      await tv.waitForTimeout(100)
    }
    await expect(tv.getByTestId('kiosk-offline')).toContainText('Sin conexión desde las')
    // Los últimos datos siguen en pantalla.
    await expect(
      tv.getByTestId('kiosk-current').locator('[data-testid="widget"][data-state="ready"]'),
    ).toHaveCount(9)
    await tv.evaluate(() => window.localStorage.removeItem('horus.mock.offline'))
    for (let i = 0; i < 3; i++) {
      await tv.clock.fastForward(31_000)
      await tv.waitForTimeout(400)
    }
    await expect(tv.getByTestId('kiosk-offline')).toHaveCount(0)
  })

  test('nueva versión del frontend: se recarga sola en el siguiente cambio de dashboard', async ({
    page,
    context,
  }) => {
    await loginAsAdmin(page)
    const code = await codeFor(page)
    const tv = await openKiosk(context, true)
    await enroll(tv, code)
    await tv.evaluate(() => {
      ;(window as unknown as { __before: boolean }).__before = true
      window.localStorage.setItem('horus.mock.frontendVersion', 'nueva-version')
    })
    // La siguiente consulta de configuración trae la versión; el cambio de dashboard recarga.
    await tv.clock.fastForward(31_000)
    await tv.waitForTimeout(500)
    await tv.clock.fastForward(31_000)
    await expect
      .poll(() =>
        tv.evaluate(() => (window as unknown as { __before?: boolean }).__before ?? false),
      )
      .toBe(false)
    await expect(tv.getByTestId('kiosk-current')).toBeVisible()
  })

  test('revocación: vuelve a la pantalla de código en menos de 1 min', async ({
    page,
    context,
  }) => {
    await loginAsAdmin(page)
    const code = await codeFor(page)
    const tv = await openKiosk(context, true)
    await enroll(tv, code)
    await page.goto(KIOSKS)
    await page.locator('[data-kiosk="Pantalla de recepción"]').getByTestId('revoke-kiosk').click()
    await page.getByTestId('revoke-confirm').click()
    await tv.clock.fastForward(31_000)
    await expect(tv.getByTestId('kiosk-enroll')).toBeVisible({ timeout: 10_000 })
  })

  test('24 h con reloj acelerado: sin errores y memoria acotada', async ({ page, context }) => {
    await loginAsAdmin(page)
    const code = await codeFor(page)
    const tv = await openKiosk(context, true)
    const errors: string[] = []
    tv.on('pageerror', (e) => errors.push(e.message))
    await enroll(tv, code)
    const cdp = await context.newCDPSession(tv)
    const heap = async () => {
      await cdp.send('HeapProfiler.collectGarbage')
      return (await cdp.send('Runtime.getHeapUsage')).usedSize
    }
    const before = await heap()
    for (let i = 0; i < 144; i++) {
      await tv.clock.fastForward('10:00')
      await tv.waitForTimeout(150)
    }
    await expect(tv.getByTestId('kiosk-current')).toBeVisible({ timeout: 20_000 })
    const after = await heap()
    expect(errors).toEqual([])
    // Sin crecimiento sin límite: como mucho +50 % o +15 MB sobre el arranque.
    expect(after).toBeLessThan(Math.max(before * 1.5, before + 15 * 1024 * 1024))
  })
})
