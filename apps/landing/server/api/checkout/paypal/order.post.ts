// POST /api/checkout/paypal/order — { reference, token } → crea la orden de PayPal por el
// importe en USD calculado en el servidor. El navegador nunca envía importes.
import { createPaypalOrder } from '../../../lib/checkout'

export default defineEventHandler(async (event) => {
  const body = await readCheckoutBody(event, checkoutBaseSchema)
  try {
    return await createPaypalOrder(useCheckoutDeps(), body.reference, body.token)
  } catch (err) {
    checkoutFail(err)
  }
})
