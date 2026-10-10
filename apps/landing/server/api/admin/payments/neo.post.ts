// POST /api/admin/payments/neo — links de pago Neo por plan y periodo y activar/desactivar.
import { audit } from '../../../lib/auth/service'
import { useApp } from '../../../lib/context'
import { neoUpdateSchema, readPayments, writeNeo } from '../../../lib/payment-config'

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, neoUpdateSchema)
  const app = useApp()
  const before = readPayments(app.db, app.key.box).neo
  writeNeo(app.db, body)
  const after = readPayments(app.db, app.key.box).neo
  audit(app.auth(), adminActor(event), 'payments.neo', 'payment_method', 'neo', before, after)
  app.cache.invalidate()
  return after
})
