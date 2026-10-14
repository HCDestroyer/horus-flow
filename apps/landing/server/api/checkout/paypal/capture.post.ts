// POST /api/checkout/paypal/capture — { reference, token, orderId } → captura la orden aprobada
// y, si PayPal confirma el importe, marca la solicitud como pagada.
import { z } from 'zod'
import { capturePaypalOrder } from '../../../lib/checkout'

const schema = checkoutBaseSchema.extend({ orderId: z.string().regex(/^[A-Z0-9-]{5,64}$/i) })

export default defineEventHandler(async (event) => {
  const body = await readCheckoutBody(event, schema)
  try {
    return await capturePaypalOrder(useCheckoutDeps(), body.reference, body.token, body.orderId)
  } catch (err) {
    checkoutFail(err)
  }
})
