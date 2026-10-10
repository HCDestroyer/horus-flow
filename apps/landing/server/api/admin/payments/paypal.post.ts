// POST /api/admin/payments/paypal — modo, webhook id, activar y credenciales (solo escritura).
import { audit } from '../../../lib/auth/service'
import { useApp } from '../../../lib/context'
import { paypalUpdateSchema, readPayments, writePaypal } from '../../../lib/payment-config'

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, paypalUpdateSchema)
  const app = useApp()
  const before = readPayments(app.db, app.key.box).paypal
  try {
    writePaypal(app.db, app.key.box, body)
  } catch (err) {
    if ((err as Error).message === 'NO_DATA_KEY') {
      throw createError({ statusCode: 409, data: { code: 'NO_DATA_KEY' } })
    }
    throw err
  }
  const after = readPayments(app.db, app.key.box).paypal
  // En la auditoría no hay secretos: solo si cambiaron.
  audit(app.auth(), adminActor(event), 'payments.paypal', 'payment_method', 'paypal', before, {
    ...after,
    credentialsChanged: Boolean(body.clientId || body.clientSecret || body.clearCredentials),
  })
  app.cache.invalidate()
  return after
})
