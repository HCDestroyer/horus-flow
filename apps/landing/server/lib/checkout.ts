// Pago de una solicitud de compra con los tres métodos:
//   PayPal      — orden creada y capturada en el servidor (Orders v2) por el importe en USD
//                 guardado al crear la solicitud; confirmación también por webhook verificado.
//   Link Neo    — se entrega el link configurado para el plan y periodo; pendiente de pago.
//   Transferencia — datos bancarios y referencia, también por correo; pendiente de pago.
// El administrador confirma en el panel los pagos por Neo y transferencia.
import type { Catalog } from '../../shared/catalog'
import type { ServerConfig } from '../utils/config'
import {
  paymentConfirmedCustomerEmail,
  paymentConfirmedSalesEmail,
  paymentInstructionsEmail,
  planName,
  type PurchaseSummary,
} from '../utils/emails'
import type { LogFn } from '../utils/log'
import type { Mailer } from '../utils/mailer'
import type { DB } from './db'
import { nowIso } from './db'
import {
  neoLink,
  readPaypalCredentials,
  readPayments,
  type TransferAccount,
} from './payment-config'
import { PaypalClient, type PaypalFetch } from './paypal'
import {
  authorizePurchase,
  choosePaymentMethod,
  getRequestByReference,
  markPaidByPaypal,
  type RequestRow,
} from './requests'
import type { SecretBox } from './secrets'

export interface CheckoutDeps {
  db: DB
  box: SecretBox | null
  config: ServerConfig
  mailer: Mailer
  log: LogFn
  now: () => number
  catalog: () => Catalog
  paypalFetch: PaypalFetch
  /** Se llama tras un cambio de estado (para invalidar cachés, etc.). */
  onChange?: () => void
}

export class CheckoutError extends Error {
  constructor(
    readonly code: string,
    readonly status = 400,
  ) {
    super(code)
  }
}

function summary(deps: CheckoutDeps, row: RequestRow): PurchaseSummary {
  const data = JSON.parse(row.data) as { contactName?: string; legalName?: string }
  return {
    reference: row.reference,
    locale: row.locale,
    contactName: data.contactName ?? row.name,
    email: row.email,
    company: data.legalName ?? row.company,
    plan: planName(deps.catalog(), row.plan ?? '', row.locale),
    period: row.period ?? 'annual',
    currency: row.currency ?? 'USD',
    amount: row.amount_minor === null ? null : row.amount_minor / 100,
  }
}

function payable(db: DB, reference: string, token: string): RequestRow {
  const row = authorizePurchase(db, reference, token)
  if (!row) throw new CheckoutError('NOT_FOUND', 404)
  if (row.status === 'cancelled') throw new CheckoutError('CANCELLED', 409)
  if (row.amount_minor === null) throw new CheckoutError('NO_AMOUNT', 409)
  return row
}

async function send(deps: CheckoutDeps, reference: string, fn: () => Promise<void>) {
  try {
    await fn()
  } catch (err) {
    deps.log('error', 'mail.payment_failed', { reference, error: String(err) })
  }
}

// ---- PayPal --------------------------------------------------------------------------------

function paypalClient(deps: CheckoutDeps): { client: PaypalClient; webhookId: string } {
  const cfg = readPayments(deps.db, deps.box)
  const creds = readPaypalCredentials(deps.db, deps.box)
  if (!cfg.paypal.enabled || !creds?.clientId || !creds.clientSecret) {
    throw new CheckoutError('PAYPAL_DISABLED', 409)
  }
  return {
    client: new PaypalClient({
      mode: cfg.paypal.mode,
      clientId: creds.clientId,
      clientSecret: creds.clientSecret,
      fetch: deps.paypalFetch,
      apiBase: deps.config.paypalApiBase || undefined,
      now: deps.now,
    }),
    webhookId: cfg.paypal.webhookId,
  }
}

/** Crea (o reutiliza) la orden de PayPal de la solicitud. El importe sale de la base de datos. */
export async function createPaypalOrder(
  deps: CheckoutDeps,
  reference: string,
  token: string,
): Promise<{ orderId: string }> {
  const row = payable(deps.db, reference, token)
  if (row.status === 'paid') throw new CheckoutError('ALREADY_PAID', 409)
  if (!row.amount_usd_minor) throw new CheckoutError('NO_AMOUNT', 409)
  const { client } = paypalClient(deps)
  const s = summary(deps, row)
  const order = await client.createOrder({
    reference,
    amountMinor: row.amount_usd_minor,
    description: `Horus Flow ${s.plan} (${row.period}) · ${reference}`,
    locale: row.locale,
  })
  deps.db
    .prepare('UPDATE requests SET paypal_order_id = ?, updated_at = ? WHERE id = ?')
    .run(order.id, nowIso(deps.now()), row.id)
  choosePaymentMethod(deps.db, getRequestByReference(deps.db, reference)!, 'paypal', deps.now())
  deps.onChange?.()
  deps.log('info', 'paypal.order_created', { reference, orderId: order.id })
  return { orderId: order.id }
}

async function confirmPaid(
  deps: CheckoutDeps,
  reference: string,
  capture: { captureId: string; orderId?: string; amountMinor: number; currency: string },
  source: string,
): Promise<boolean> {
  const { changed, row } = markPaidByPaypal(deps.db, reference, { ...capture, source }, deps.now())
  if (!changed || !row) return false
  deps.onChange?.()
  const s = summary(deps, row)
  const paid = {
    amount: capture.amountMinor / 100,
    currency: capture.currency,
    method: 'PayPal',
    transactionId: capture.captureId,
  }
  await send(deps, reference, () =>
    deps.mailer.send(paymentConfirmedSalesEmail(s, paid, deps.config.mail.salesTo)),
  )
  await send(deps, reference, () =>
    deps.mailer.send(paymentConfirmedCustomerEmail(s, paid, deps.config.mail.salesTo)),
  )
  deps.log('info', 'payment.confirmed', { reference, method: 'paypal', source })
  return true
}

/** Captura la orden aprobada en el navegador y comprueba importe, moneda y referencia. */
export async function capturePaypalOrder(
  deps: CheckoutDeps,
  reference: string,
  token: string,
  orderId: string,
): Promise<{ status: 'paid' }> {
  const row = payable(deps.db, reference, token)
  if (row.status === 'paid') return { status: 'paid' }
  if (!row.paypal_order_id || row.paypal_order_id !== orderId) {
    throw new CheckoutError('ORDER_MISMATCH', 409)
  }
  const { client } = paypalClient(deps)
  const capture = await client.captureOrder(orderId, reference)
  if (
    capture.status !== 'COMPLETED' ||
    capture.currency !== 'USD' ||
    capture.amountMinor !== row.amount_usd_minor ||
    (capture.reference && capture.reference !== reference)
  ) {
    deps.log('error', 'paypal.capture_mismatch', {
      reference,
      status: capture.status,
      currency: capture.currency,
      amount: capture.amountMinor,
    })
    throw new CheckoutError('CAPTURE_NOT_COMPLETED', 402)
  }
  await confirmPaid(deps, reference, { ...capture, orderId }, 'captura')
  return { status: 'paid' }
}

export interface WebhookResult {
  status: number
  handled: 'paid' | 'duplicate' | 'ignored' | 'rejected'
}

/**
 * Webhook de PayPal. Solo se procesa si la API de verificación de firmas responde SUCCESS; un
 * evento no verificado no cambia nada (400). PAYMENT.CAPTURE.COMPLETED marca la referencia
 * (custom_id) como pagada si el importe y la moneda coinciden. Idempotente por id de evento.
 */
export async function handlePaypalWebhook(
  deps: CheckoutDeps,
  headers: Record<string, string | undefined>,
  rawBody: string,
): Promise<WebhookResult> {
  let ctx: ReturnType<typeof paypalClient>
  try {
    ctx = paypalClient(deps)
  } catch {
    return { status: 400, handled: 'rejected' }
  }
  let verified: boolean
  try {
    verified = await ctx.client.verifyWebhook(headers, rawBody, ctx.webhookId)
  } catch (err) {
    deps.log('error', 'paypal.webhook_verify_failed', { error: String(err) })
    return { status: 502, handled: 'rejected' }
  }
  if (!verified) {
    deps.log('warn', 'paypal.webhook_unverified', {})
    return { status: 400, handled: 'rejected' }
  }
  const event = JSON.parse(rawBody) as {
    id?: string
    event_type?: string
    resource?: {
      id?: string
      status?: string
      custom_id?: string
      amount?: { value: string; currency_code: string }
      supplementary_data?: { related_ids?: { order_id?: string } }
    }
  }
  if (!event.id) return { status: 400, handled: 'rejected' }
  const seen = deps.db.prepare('SELECT 1 FROM paypal_events WHERE id = ?').get(event.id)
  if (seen) return { status: 200, handled: 'duplicate' }
  const reference = event.resource?.custom_id ?? null
  deps.db
    .prepare('INSERT INTO paypal_events (id, received_at, type, reference) VALUES (?, ?, ?, ?)')
    .run(event.id, nowIso(deps.now()), event.event_type ?? '', reference)

  if (event.event_type !== 'PAYMENT.CAPTURE.COMPLETED' || !reference) {
    return { status: 200, handled: 'ignored' }
  }
  const row = getRequestByReference(deps.db, reference)
  const r = event.resource!
  const amountMinor = r.amount ? Math.round(Number.parseFloat(r.amount.value) * 100) : 0
  if (
    !row ||
    row.kind !== 'purchase' ||
    r.status !== 'COMPLETED' ||
    r.amount?.currency_code !== 'USD' ||
    amountMinor !== row.amount_usd_minor
  ) {
    deps.log('error', 'paypal.webhook_mismatch', { reference, amount: amountMinor })
    return { status: 200, handled: 'ignored' }
  }
  const changed = await confirmPaid(
    deps,
    reference,
    {
      captureId: r.id ?? '',
      orderId: r.supplementary_data?.related_ids?.order_id,
      amountMinor,
      currency: 'USD',
    },
    'webhook',
  )
  return { status: 200, handled: changed ? 'paid' : 'duplicate' }
}

// ---- Link Neo y transferencia ---------------------------------------------------------------

export interface ManualInstructions {
  method: 'neo' | 'transfer'
  reference: string
  amount: number | null
  currency: string
  neoUrl?: string
  accounts?: TransferAccount[]
  instructions?: { es: string; en: string }
}

/**
 * El cliente elige Neo o transferencia: la solicitud pasa a "pendiente de pago" y se le envían
 * las instrucciones por correo (una vez por método).
 */
export async function chooseManualMethod(
  deps: CheckoutDeps,
  reference: string,
  token: string,
  method: 'neo' | 'transfer',
): Promise<ManualInstructions> {
  const row = payable(deps.db, reference, token)
  const cfg = readPayments(deps.db, deps.box)
  const base = {
    method,
    reference,
    amount: row.amount_minor === null ? null : row.amount_minor / 100,
    currency: row.currency ?? 'USD',
  }
  let result: ManualInstructions
  if (method === 'neo') {
    const url =
      cfg.neo.enabled && row.plan && row.period ? neoLink(cfg, row.plan, row.period) : null
    if (!url) throw new CheckoutError('NEO_DISABLED', 409)
    result = { ...base, neoUrl: url }
  } else {
    if (!cfg.transfer.enabled || cfg.transfer.accounts.length === 0) {
      throw new CheckoutError('TRANSFER_DISABLED', 409)
    }
    result = { ...base, accounts: cfg.transfer.accounts, instructions: cfg.transfer.instructions }
  }
  if (row.status === 'paid') return result

  const firstTime = row.payment_method !== method
  choosePaymentMethod(deps.db, row, method, deps.now())
  deps.onChange?.()
  if (firstTime) {
    const s = summary(deps, row)
    await send(deps, reference, () =>
      deps.mailer.send(
        paymentInstructionsEmail(
          s,
          method === 'neo'
            ? { kind: 'neo', url: result.neoUrl! }
            : { kind: 'transfer', accounts: result.accounts!, instructions: result.instructions! },
          deps.config.mail.salesTo,
        ),
      ),
    )
  }
  deps.log('info', 'payment.method_chosen', { reference, method })
  return result
}

/** Estado de una solicitud para la página de compra (con su token). */
export function checkoutStatus(deps: CheckoutDeps, reference: string, token: string) {
  const row = authorizePurchase(deps.db, reference, token)
  if (!row) throw new CheckoutError('NOT_FOUND', 404)
  return { reference, status: row.status, paymentMethod: row.payment_method }
}
