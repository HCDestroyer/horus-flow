import type { Page } from '@playwright/test'
import { hydrated } from './helpers'

/**
 * Sustituye el JS SDK de PayPal (https://www.paypal.com/sdk/js) por uno simulado: un botón que
 * llama a createOrder (nuestro servidor crea la orden en el PayPal simulado) y, tras aprobar la
 * orden en el simulador, un segundo botón que llama a onApprove (nuestro servidor captura).
 */
export async function mockPaypalSdk(page: Page) {
  await page.route('https://www.paypal.com/sdk/js**', (route) =>
    route.fulfill({
      contentType: 'application/javascript',
      body: `window.paypal = { Buttons: function (opts) { return { render: async function (el) {
        var b = document.createElement('button'); b.type = 'button'; b.textContent = 'PayPal (simulado)';
        b.onclick = async function () {
          var id = await opts.createOrder(); el.dataset.orderId = id;
          var c = document.createElement('button'); c.type = 'button'; c.textContent = 'Confirmar pago simulado';
          c.onclick = function () { opts.onApprove({ orderID: id }) }; el.appendChild(c);
        };
        el.appendChild(b);
      } } } };`,
    }),
  )
}

/** Rellena y envía la compra; devuelve la referencia. */
export async function submitPurchase(
  page: Page,
  opts: { query?: string; company?: string; email?: string } = {},
): Promise<string> {
  await page.goto(`/comprar${opts.query ?? '?plan=medium&period=annual'}`)
  await hydrated(page)
  await page.getByLabel('Razón social').fill(opts.company ?? 'Fibra Sur, Sociedad Anónima')
  await page.getByLabel('NIT').fill('576937-K')
  await page.getByLabel('Nombre de contacto').fill('Ana Pérez')
  await page.getByLabel('Correo').fill(opts.email ?? 'compras@fibrasur.example')
  await page.locator('aside').getByRole('checkbox').check()
  await page.waitForTimeout(1100)
  await page.locator('aside').getByRole('button').last().click()
  await page.getByTestId('purchase-success').waitFor()
  return (await page.getByTestId('purchase-reference').textContent())!.trim()
}
