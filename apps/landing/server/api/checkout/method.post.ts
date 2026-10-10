// POST /api/checkout/method — { reference, token, method: 'neo' | 'transfer' } → instrucciones
// de pago (link Neo o datos bancarios) y correo con ellas. La solicitud queda pendiente de pago.
import { z } from 'zod'
import { chooseManualMethod } from '../../lib/checkout'

const schema = checkoutBaseSchema.extend({ method: z.enum(['neo', 'transfer']) })

export default defineEventHandler(async (event) => {
  const body = await readCheckoutBody(event, schema)
  try {
    return await chooseManualMethod(useCheckoutDeps(), body.reference, body.token, body.method)
  } catch (err) {
    checkoutFail(err)
  }
})
