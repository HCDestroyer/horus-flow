// Capturas del panel y de la compra con los tres métodos (docs/screenshots/). Solo con
// `pnpm screenshots`: entra como el primer administrador, configura los pagos de ejemplo, crea
// solicitudes y fotografía cada pantalla.
import { expect, test, type Page } from '@playwright/test'
import { loginAdmin } from './admin-helpers'
import { mockPaypalSdk, submitPurchase } from './checkout-helpers'

test.describe.configure({ mode: 'serial' })

const DIR = 'docs/screenshots'

async function shot(page: Page, name: string, fullPage = true) {
  await page.waitForLoadState('networkidle')
  await page.screenshot({ path: `${DIR}/${name}.jpg`, fullPage, type: 'jpeg', quality: 72 })
}

async function api(page: Page, path: string, body: unknown) {
  return page.evaluate(
    async ({ path, body }) => {
      const s = await (await fetch('/api/admin/auth/session')).json()
      const r = await fetch(`/api/admin${path}`, {
        method: 'POST',
        headers: { 'content-type': 'application/json', 'x-csrf-token': s.csrf },
        body: JSON.stringify(body),
      })
      return r.status
    },
    { path, body },
  )
}

test('@screenshots panel y compra con PayPal, link Neo y transferencia', async ({ browser }) => {
  test.setTimeout(240_000)
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const admin = await ctx.newPage()
  await admin.emulateMedia({ reducedMotion: 'reduce' })

  // Login con alta del TOTP (captura del QR antes de completar).
  await admin.goto('/admin/login')
  await shot(admin, 'admin-login-1440-claro', false)
  await loginAdmin(admin)

  expect(
    await api(admin, '/payments/paypal', {
      enabled: true,
      mode: 'sandbox',
      webhookId: 'WH-TEST',
      clientId: 'client-test',
      clientSecret: 'secret-test',
    }),
  ).toBe(200)
  expect(
    await api(admin, '/payments/neo', {
      enabled: true,
      links: { medium: { monthly: '', annual: 'https://pagos.neo.example/l/mediano-anual' } },
    }),
  ).toBe(200)
  expect(
    await api(admin, '/payments/transfer', {
      enabled: true,
      accounts: [
        {
          bank: 'Banco Industrial',
          type: { es: 'Monetaria', en: 'Checking' },
          number: '000-123456-7',
          holder: 'Connection And Solutions Company, S.A.',
          currency: 'GTQ',
        },
        {
          bank: 'Banco Industrial',
          type: { es: 'Monetaria en dólares', en: 'USD checking' },
          number: '000-765432-1',
          holder: 'Connection And Solutions Company, S.A.',
          currency: 'USD',
        },
      ],
      instructions: {
        es: 'Envía el comprobante a info@kns.gt indicando la referencia.',
        en: 'Send the receipt to info@kns.gt with the reference.',
      },
    }),
  ).toBe(200)

  // Compra con cada método (cliente en otra ventana).
  for (const [method, width] of [
    ['paypal', 1440],
    ['neo', 1440],
    ['transfer', 1440],
    ['transfer', 390],
  ] as const) {
    const buyer = await browser.newPage({ viewport: { width, height: width === 390 ? 844 : 900 } })
    await buyer.emulateMedia({ reducedMotion: 'reduce' })
    await mockPaypalSdk(buyer)
    await submitPurchase(buyer, {
      company: `ISP ${method} ${width}`,
      email: `${method}@isp.example`,
      query: '?plan=medium&period=annual',
    })
    await buyer.locator(`label[data-method="${method}"]`).click()
    await expect(buyer.getByTestId(`pay-${method}`)).toBeVisible()
    if (method === 'paypal')
      await buyer.getByRole('button', { name: 'PayPal (simulado)' }).waitFor()
    await shot(buyer, `comprar-pago-${method}-${width}-claro`)
    await buyer.close()
  }

  // Panel.
  for (const scheme of ['light', 'dark'] as const) {
    const suffix = scheme === 'light' ? 'claro' : 'oscuro'
    await admin.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' })
    await admin.goto('/admin/solicitudes')
    await expect(admin.getByRole('row').nth(1)).toBeVisible()
    await shot(admin, `admin-solicitudes-1440-${suffix}`)
    if (scheme === 'dark') continue
    await admin.getByRole('row').nth(1).getByRole('link').click()
    await expect(admin.getByRole('heading', { name: 'Datos del cliente' })).toBeVisible()
    await shot(admin, 'admin-solicitud-1440-claro')
    await admin.goto('/admin/precios')
    await expect(admin.locator('[data-plan-editor="small"]')).toBeVisible()
    await shot(admin, 'admin-precios-1440-claro')
    await admin
      .locator('[data-plan-editor="medium"]')
      .getByLabel('Mediano, USD, Anual')
      .fill('3790')
    await admin.getByRole('button', { name: 'Vista previa' }).click()
    await shot(admin, 'admin-precios-vista-previa-1440-claro')
    await admin.goto('/admin/pagos')
    await expect(admin.getByTestId('paypal-credentials')).toBeVisible()
    await shot(admin, 'admin-pagos-1440-claro')
    await admin.goto('/admin/ajustes')
    await expect(admin.getByRole('heading', { name: 'Administradores' })).toBeVisible()
    await shot(admin, 'admin-ajustes-1440-claro')
    await admin.goto('/admin/auditoria')
    await expect(admin.getByText('TOTP activado').first()).toBeVisible()
    await shot(admin, 'admin-auditoria-1440-claro')
  }

  // Móvil.
  await admin.setViewportSize({ width: 390, height: 844 })
  await admin.emulateMedia({ colorScheme: 'light', reducedMotion: 'reduce' })
  await admin.goto('/admin/solicitudes')
  await expect(admin.getByRole('heading', { level: 1 })).toBeVisible()
  await shot(admin, 'admin-solicitudes-390-claro')
  await admin.goto('/admin/precios')
  await expect(admin.locator('[data-plan-editor="small"]')).toBeVisible()
  await shot(admin, 'admin-precios-390-claro', false)
  await ctx.close()
})
