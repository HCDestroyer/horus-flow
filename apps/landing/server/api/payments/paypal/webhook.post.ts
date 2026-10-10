// POST /api/payments/paypal/webhook — webhook de PayPal (PAYMENT.CAPTURE.COMPLETED). Se
// verifica la firma con la API de PayPal antes de tocar nada; sin verificación → 400.
import { handlePaypalWebhook } from '../../../lib/checkout'

const MAX = 64 * 1024

export default defineEventHandler(async (event) => {
  const raw = (await readRawBody(event, 'utf8')) ?? ''
  if (raw.length > MAX) throw createError({ statusCode: 413 })
  const headers = Object.fromEntries(
    Object.entries(getRequestHeaders(event)).map(([k, v]) => [k.toLowerCase(), v]),
  )
  const result = await handlePaypalWebhook(useCheckoutDeps(), headers, raw)
  setResponseStatus(event, result.status)
  return { handled: result.handled }
})
