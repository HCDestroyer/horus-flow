// POST /api/admin/requests/:id/status — cambia el estado con una nota. Al marcar como pagada una
// compra (link Neo o transferencia) se avisa por correo al cliente y a ventas si notify = true.
import { z } from 'zod'
import { audit } from '../../../../lib/auth/service'
import { useApp } from '../../../../lib/context'
import { REQUEST_STATUSES, getRequest, setStatus } from '../../../../lib/requests'
import {
  paymentConfirmedCustomerEmail,
  paymentConfirmedSalesEmail,
  planName,
} from '../../../../utils/emails'

const schema = z.object({
  status: z.enum(REQUEST_STATUSES),
  note: z.string().trim().max(1000).optional().default(''),
  notify: z.boolean().optional().default(true),
})

const METHOD: Record<string, string> = {
  paypal: 'PayPal',
  neo: 'Link de pago Neo',
  transfer: 'Transferencia bancaria',
}

export default defineEventHandler(async (event) => {
  const id = adminId(event)
  const body = await readAdminBody(event, schema)
  const app = useApp()
  const row = getRequest(app.db, id)
  if (!row) throw createError({ statusCode: 404, data: { code: 'NOT_FOUND' } })
  if (body.status === 'paid' && row.kind !== 'purchase') {
    throw createError({ statusCode: 422, data: { code: 'NOT_A_PURCHASE' } })
  }
  const actor = adminActor(event)
  const change = setStatus(app.db, id, body.status, actor.email, body.note)
  audit(
    app.auth(),
    actor,
    'request.status',
    'request',
    row.reference,
    { status: change.before },
    { status: change.after, note: body.note },
  )

  if (body.status === 'paid' && change.before !== 'paid' && body.notify) {
    const deps = useSubmissionDeps()
    const data = JSON.parse(row.data) as { contactName?: string; legalName?: string }
    const summary = {
      reference: row.reference,
      locale: row.locale,
      contactName: data.contactName ?? row.name,
      email: row.email,
      company: data.legalName ?? row.company,
      plan: planName(app.cache.get().catalog, row.plan ?? '', row.locale),
      period: row.period ?? 'annual',
      currency: row.currency ?? 'USD',
      amount: row.amount_minor === null ? null : row.amount_minor / 100,
    } as const
    const paid = {
      amount: (row.amount_minor ?? 0) / 100,
      currency: row.currency ?? 'USD',
      method: METHOD[row.payment_method ?? 'transfer'] ?? 'Transferencia bancaria',
      transactionId: body.note || 'confirmado en el panel',
    }
    for (const msg of [
      paymentConfirmedCustomerEmail(summary, paid, deps.config.mail.salesTo),
      paymentConfirmedSalesEmail(summary, paid, deps.config.mail.salesTo),
    ]) {
      try {
        await deps.mailer.send(msg)
      } catch (err) {
        deps.log('error', 'mail.payment_failed', { reference: row.reference, error: String(err) })
      }
    }
  }
  return { ok: true, status: change.after }
})
