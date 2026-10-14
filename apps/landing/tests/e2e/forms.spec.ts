import { expect, test, type Page } from '@playwright/test'
import { hydrated, mailsFor } from './helpers'

async function pick(page: Page, label: string, option: string) {
  await page.getByRole('combobox', { name: label }).click()
  await page.getByRole('option', { name: option, exact: true }).click()
}

test('solicitud de demo: valida, envía y llega al SMTP simulado', async ({ page }) => {
  await page.goto('/#demo')
  await hydrated(page)
  const form = page.locator('#demo form')

  // Sin datos: errores por campo, nada enviado.
  await form.getByRole('button', { name: 'Enviar solicitud' }).click()
  await expect(form.getByText('Este campo es obligatorio.').first()).toBeVisible()
  await expect(form.getByText('Necesitamos tu consentimiento para responderte.')).toBeVisible()

  await form.getByLabel('Nombre').fill('Ana Pérez')
  await form.getByLabel('Empresa o ISP').fill('Fibra Sur E2E')
  await form.getByLabel('Correo').fill('ana@fibrasur.example')
  await form.getByLabel('Teléfono o WhatsApp').fill('+502 5555 1234')
  await pick(page, 'Número de clientes', 'De 300 a 2 000')
  await pick(page, 'Routers MikroTik', 'De 2 a 5')
  await form.getByLabel('Mensaje').fill('Tenemos tres nodos.')
  await form.getByRole('checkbox').check()
  // Respeta el tiempo mínimo de llenado (1 s en e2e).
  await page.waitForTimeout(1100)
  await form.getByRole('button', { name: 'Enviar solicitud' }).click()

  const success = page.getByTestId('demo-success')
  await expect(success).toBeVisible()
  await expect(success).toBeFocused()
  const text = await success.textContent()
  const reference = /HF-D-\d{8}-[0-9A-Z]{6}/.exec(text ?? '')?.[0]
  expect(reference).toBeTruthy()

  await expect.poll(async () => (await mailsFor(reference!)).length).toBe(2)
  const mails = await mailsFor(reference!)
  const sales = mails.find((m) => m.to === 'info@kns.gt')!
  expect(sales.subject).toContain('Fibra Sur E2E')
  expect(sales.text).toContain('De 300 a 2 000')
  expect(mails.find((m) => m.to === 'ana@fibrasur.example')).toBeTruthy()
})

test('compra: plan del enlace, datos de facturación y referencia', async ({ page }) => {
  await page.goto('/comprar?plan=large&period=monthly')
  await hydrated(page)
  await expect(page.getByRole('radio', { name: /Grande/ })).toBeChecked()
  await expect(page.getByRole('radio', { name: 'Mensual' })).toBeChecked()
  await expect(page.locator('aside')).toContainText('USD 990')

  await page.getByRole('radio', { name: 'GTQ' }).check({ force: true })
  await page.getByLabel('Razón social').fill('Fibra Sur, Sociedad Anónima')
  await page.getByLabel('NIT').fill('576937-K')
  await page.getByLabel('Nombre de contacto').fill('Ana Pérez')
  await page.getByLabel('Correo').fill('compras@fibrasur.example')
  await page.locator('aside').getByRole('checkbox').check()
  await page.waitForTimeout(1100)
  await page.locator('aside').getByRole('button').last().click()

  const success = page.getByTestId('purchase-success')
  await expect(success).toBeVisible()
  const reference = (await page.getByTestId('purchase-reference').textContent())!.trim()
  expect(reference).toMatch(/^HF-P-\d{8}-[0-9A-Z]{6}$/)

  await expect.poll(async () => (await mailsFor(reference)).length).toBe(2)
  const sales = (await mailsFor(reference)).find((m) => m.to === 'info@kns.gt')!
  expect(sales.text).toContain('Grande')
  expect(sales.text).toContain('Mensual')
  expect(sales.text).toContain('GTQ')
  expect(sales.text).toContain('576937-K')
  // Importe calculado en el servidor: Grande, mensual, en GTQ (7 650, o 7 990 si el e2e del
  // panel ya lo cambió).
  expect(sales.text).toMatch(/Importe: Q\s?7,(650|990)/)
})

test('el honeypot existe y está fuera del alcance de las personas', async ({ page }) => {
  await page.goto('/')
  const trap = page.locator('#demo input[name="website"]')
  await expect(trap).toHaveAttribute('tabindex', '-1')
  await expect(trap).not.toBeInViewport()
})
