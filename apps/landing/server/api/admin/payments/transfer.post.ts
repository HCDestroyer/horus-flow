// POST /api/admin/payments/transfer — cuentas bancarias e instrucciones (ES/EN).
import { audit } from '../../../lib/auth/service'
import { useApp } from '../../../lib/context'
import { readPayments, transferUpdateSchema, writeTransfer } from '../../../lib/payment-config'

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, transferUpdateSchema)
  const app = useApp()
  const before = readPayments(app.db, app.key.box).transfer
  writeTransfer(app.db, body)
  const after = readPayments(app.db, app.key.box).transfer
  audit(
    app.auth(),
    adminActor(event),
    'payments.transfer',
    'payment_method',
    'transfer',
    before,
    after,
  )
  app.cache.invalidate()
  return after
})
