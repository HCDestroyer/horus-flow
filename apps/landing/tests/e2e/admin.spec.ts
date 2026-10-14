// Panel de administración y compra con los tres métodos, de punta a punta, contra el servidor
// de producción con base de datos nueva y PayPal simulado. En serie: cada paso usa lo que dejó
// el anterior (alta del TOTP → configurar pagos → cambiar un precio → comprar).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { loginAdmin } from './admin-helpers'
import { mockPaypalSdk, submitPurchase } from './checkout-helpers'
import { hydrated, mailsFor } from './helpers'

test.describe.configure({ mode: 'serial' })

let admin: Page

async function axe(page: Page, label: string) {
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice'])
    .analyze()
  const summary = results.violations.map((v) => ({
    id: v.id,
    nodes: v.nodes.slice(0, 3).map((n) => n.target.join(' ')),
  }))
  expect(summary, label).toEqual([])
}

test.beforeAll(async ({ browser }) => {
  admin = await (await browser.newContext()).newPage()
})
test.afterAll(async () => {
  await admin.context().close()
})

test('sin sesión no se accede a ningún dato del panel', async ({ request, page }) => {
  for (const path of [
    '/api/admin/requests',
    '/api/admin/pricing',
    '/api/admin/payments',
    '/api/admin/admins',
    '/api/admin/audit',
    '/api/admin/requests/export',
  ]) {
    expect((await request.get(path)).status(), path).toBe(401)
  }
  // Sin token CSRF ni siquiera llega a la comprobación de sesión.
  expect((await request.post('/api/admin/pricing', { data: {} })).status()).toBe(403)
  // La página del panel lleva al login y no se indexa.
  const res = await page.goto('/admin/solicitudes')
  expect(res?.headers()['x-robots-tag']).toContain('noindex')
  await expect(page).toHaveURL(/\/admin\/login/)
  const sitemap = await (await request.get('/sitemap.xml')).text()
  expect(sitemap).not.toContain('/admin')
  expect(await (await request.get('/robots.txt')).text()).toContain('Disallow: /admin')
})

test('login: contraseña incorrecta, alta del TOTP por QR y códigos de recuperación', async () => {
  await admin.emulateMedia({ reducedMotion: 'reduce' })
  await admin.goto('/admin/login')
  await expect(admin.getByRole('heading', { level: 1 })).toHaveText('Panel de administración')
  await axe(admin, 'login')
  await admin.getByLabel('Correo').fill('admin@kns.gt')
  await admin.getByLabel('Contraseña').fill('no-es-la-contraseña')
  await admin.getByRole('button', { name: 'Continuar' }).click()
  await expect(admin.getByRole('alert')).toContainText('Correo o contraseña incorrectos')

  await loginAdmin(admin)
  // La cookie de sesión es __Host-, HttpOnly, Secure y SameSite=Strict.
  const cookie = (await admin.context().cookies()).find((c) => c.name === '__Host-hf_admin')
  expect(cookie).toMatchObject({ httpOnly: true, secure: true, sameSite: 'Strict', path: '/' })
})

test('axe sin violaciones en las páginas del panel (claro y oscuro)', async () => {
  for (const scheme of ['light', 'dark'] as const) {
    await admin.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' })
    for (const path of [
      '/admin/solicitudes',
      '/admin/precios',
      '/admin/pagos',
      '/admin/ajustes',
      '/admin/auditoria',
    ]) {
      await admin.goto(path)
      await expect(admin.getByRole('heading', { level: 1 })).toBeVisible()
      await admin.waitForLoadState('networkidle')
      await axe(admin, `${path} (${scheme})`)
    }
  }
  await admin.emulateMedia({ colorScheme: 'light' })
})

test('configurar PayPal (sandbox), links Neo y transferencia', async () => {
  await admin.goto('/admin/pagos')
  const pp = admin.locator('section', { has: admin.getByRole('heading', { name: 'PayPal' }) })
  await pp.getByLabel('Ofrecer PayPal en la compra').check()
  await pp.getByLabel('Client ID').fill('client-test')
  await pp.getByLabel('Secret').fill('secret-test')
  await pp.getByLabel('Webhook ID').fill('WH-TEST')
  await pp.getByRole('button', { name: 'Guardar PayPal' }).click()
  // Solo escritura: ya no se muestran, solo que están guardadas.
  await expect(admin.getByTestId('paypal-credentials')).toContainText('guardadas (client id …test)')
  await expect(pp.getByLabel('Client ID')).toHaveValue('')

  const neo = admin.locator('section', {
    has: admin.getByRole('heading', { name: 'Link de pago Neo' }),
  })
  await neo.getByLabel('Ofrecer link de pago Neo en la compra').check()
  await neo.getByLabel('Link Neo Mediano Anual').fill('https://pagos.neo.example/l/mediano-anual')
  await neo.getByRole('button', { name: 'Guardar Neo' }).click()
  await expect(admin.getByText('Guardado').first()).toBeVisible()

  const tr = admin.locator('section', {
    has: admin.getByRole('heading', { name: 'Transferencia bancaria' }),
  })
  await tr.getByLabel('Ofrecer transferencia bancaria en la compra').check()
  await tr.getByRole('button', { name: 'Añadir cuenta' }).click()
  await tr.getByLabel('Banco').fill('Banco Industrial')
  await tr.getByLabel('Número de cuenta').fill('000-123456-7')
  await tr.getByRole('button', { name: 'Guardar transferencia' }).click()
  await admin.reload()
  await expect(admin.getByLabel('Número de cuenta')).toHaveValue('000-123456-7')
})

test('cambiar un precio en el panel y verlo en la web sin redesplegar', async ({ page }) => {
  await admin.goto('/admin/precios')
  const large = admin.locator('[data-plan-editor="large"]')
  await large.getByLabel('Grande, GTQ, Mensual').fill('7990')
  await expect(admin.getByText('Hay cambios sin publicar.')).toBeVisible()
  await admin.getByRole('button', { name: 'Vista previa' }).click()
  await expect(admin.getByRole('region', { name: 'Vista previa' })).toBeVisible()
  await admin.getByLabel('Nota de la versión').fill('Subida plan Grande (e2e)')
  await admin.getByRole('button', { name: 'Publicar' }).click()
  await expect(admin.getByText('El borrador coincide con lo publicado.')).toBeVisible()
  await expect(admin.getByRole('cell', { name: 'Subida plan Grande (e2e)' })).toBeVisible()

  await page.goto('/')
  await hydrated(page)
  const pricing = page.locator('#pricing')
  await pricing.scrollIntoViewIfNeeded()
  // La sección se hidrata al acercarse a la pantalla: se reintenta hasta que los selectores
  // responden.
  await expect(async () => {
    await pricing.getByText('Mensual', { exact: true }).click()
    await pricing.getByText('GTQ', { exact: true }).click()
    await expect(pricing.locator('[data-plan="large"]')).toContainText('7,990', { timeout: 1000 })
  }).toPass({ timeout: 15_000 })

  // La auditoría guarda quién y el antes/después.
  await admin.goto('/admin/auditoria')
  await expect(admin.getByText('Precios publicados').first()).toBeVisible()
})

test('compra con PayPal (simulado): pagada, correo al cliente y a info@kns.gt', async ({
  page,
  request,
}) => {
  await mockPaypalSdk(page)
  const reference = await submitPurchase(page, {
    company: 'ISP PayPal E2E',
    email: 'paypal@isp.example',
  })
  await expect(page.getByTestId('checkout-amount')).toHaveText(/3,990/)
  await page.locator('label[data-method="paypal"]').click()
  await expect(
    page.getByText('PayPal cobra en dólares').or(page.getByText('Pulsa el botón de PayPal')),
  ).toBeVisible()
  await page.getByRole('button', { name: 'PayPal (simulado)' }).click()
  const box = page.getByTestId('pay-paypal').locator('[data-order-id]')
  await expect(box).toHaveAttribute('data-order-id', /TEST$/)
  const orderId = (await box.getAttribute('data-order-id'))!
  // El comprador aprueba en "PayPal".
  expect(
    (
      await request.post(
        `http://127.0.0.1:${process.env.FAKE_PAYPAL_PORT ?? 4176}/__approve/${orderId}`,
      )
    ).ok(),
  ).toBe(true)
  await page.getByRole('button', { name: 'Confirmar pago simulado' }).click()
  await expect(page.getByTestId('purchase-title')).toHaveText('Pago recibido')

  await expect.poll(async () => (await mailsFor(reference)).length).toBe(4)
  const mails = await mailsFor(reference)
  expect(mails.some((m) => m.to === 'info@kns.gt' && m.subject.includes('Pago confirmado'))).toBe(
    true,
  )
  expect(
    mails.some((m) => m.to === 'paypal@isp.example' && m.subject.includes('pago recibido')),
  ).toBe(true)

  await admin.goto('/admin/solicitudes')
  await admin.getByLabel('Buscar').fill(reference)
  await expect(admin.getByRole('row', { name: new RegExp(reference) })).toContainText('Pagada')
})

test('compra con link Neo: link del plan y referencia visibles, pendiente de pago', async ({
  page,
}) => {
  const reference = await submitPurchase(page, { company: 'ISP Neo E2E' })
  await page.locator('label[data-method="neo"]').click()
  const neo = page.getByTestId('pay-neo')
  await expect(neo).toContainText(reference)
  await expect(neo.getByRole('link', { name: 'Abrir link de pago Neo' })).toHaveAttribute(
    'href',
    'https://pagos.neo.example/l/mediano-anual',
  )
  await admin.goto('/admin/solicitudes')
  await admin.getByLabel('Buscar').fill(reference)
  await expect(admin.getByRole('row', { name: new RegExp(reference) })).toContainText(
    'Pendiente de pago',
  )
})

test('compra por transferencia: datos bancarios, correo con instrucciones y el admin confirma el pago', async ({
  page,
}) => {
  const reference = await submitPurchase(page, {
    company: 'ISP Transferencia E2E',
    email: 'tr@isp.example',
  })
  await page.locator('label[data-method="transfer"]').click()
  const tr = page.getByTestId('pay-transfer')
  await expect(tr).toContainText('000-123456-7')
  await expect(tr).toContainText(reference)
  await expect
    .poll(async () =>
      (await mailsFor(reference)).some((m) => m.subject.includes('instrucciones de pago')),
    )
    .toBe(true)

  await admin.goto('/admin/solicitudes')
  await admin.getByLabel('Buscar').fill(reference)
  await admin.getByRole('link', { name: reference }).first().click()
  await expect(admin.getByRole('heading', { level: 1 })).toHaveText(reference)
  await axe(admin, 'detalle de solicitud')
  await admin.getByLabel('Estado', { exact: true }).selectOption('paid')
  await admin.getByLabel('Nota').fill('Boleta 998877')
  await admin.getByRole('button', { name: 'Guardar estado' }).click()
  await expect(admin.getByRole('main').getByText('Pagada').first()).toBeVisible()
  await expect(admin.getByText('Boleta 998877').first()).toBeVisible()
  await expect
    .poll(
      async () =>
        (await mailsFor(reference)).filter((m) => /pago (recibido|confirmado)/i.test(m.subject))
          .length,
    )
    .toBe(2)

  // Exportación CSV (descarga desde el panel) con la referencia.
  await admin.goto('/admin/solicitudes')
  const [download] = await Promise.all([
    admin.waitForEvent('download'),
    admin.getByRole('link', { name: 'Exportar CSV' }).click(),
  ])
  expect(download.suggestedFilename()).toMatch(/^horus-solicitudes-\d{4}-\d{2}-\d{2}\.csv$/)
  const { readFile } = await import('node:fs/promises')
  const csv = await readFile((await download.path())!, 'utf8')
  expect(csv).toContain(reference)
  expect(csv).toContain('Pagada')
})
