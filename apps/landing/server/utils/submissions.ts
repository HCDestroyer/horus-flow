// Lógica de los formularios, independiente de h3 para poder probarla: rate limit → antispam
// (honeypot y tiempo mínimo) → validación zod → correo a ventas → confirmación al cliente →
// webhook opcional. Las rutas (server/api/*.post.ts) solo traducen petición ↔ resultado.
import { pricing, planPrice } from '../../app/config/pricing'
import {
  antispamSchema,
  fieldErrors,
  leadSchema,
  purchaseSchema,
  type SubmitError,
  type SubmitOk,
} from '../../shared/schemas'
import { getPaymentProvider, type PaymentResult, type PurchaseOrder } from '../payments'
import type { ServerConfig } from './config'
import {
  leadCustomerEmail,
  leadSalesEmail,
  purchaseCustomerEmail,
  purchaseSalesEmail,
} from './emails'
import { maskEmail, maskIp, type LogFn } from './log'
import type { Mailer } from './mailer'
import type { RateLimiter } from './rate-limit'
import { newReference } from './reference'
import { postWebhook, type FetchLike } from './webhook'

export interface SubmissionDeps {
  config: ServerConfig
  mailer: Mailer
  limiter: RateLimiter
  log: LogFn
  fetch: FetchLike
  now: () => number
}

export interface SubmissionResult {
  status: number
  body: SubmitOk | SubmitError
  retryAfterSec?: number
}

type Gate = { ok: true } | { ok: false; result: SubmissionResult }

function fail(status: number, code: string, fields?: Record<string, string>): SubmissionResult {
  return { status, body: { ok: false, code, ...(fields ? { fields } : {}) } }
}

/** Rate limit y antispam comunes. `honeypot` → respuesta OK falsa sin enviar nada. */
function gate(form: string, raw: unknown, ip: string, deps: SubmissionDeps): Gate {
  const rl = deps.limiter.hit(ip, deps.now())
  if (!rl.allowed) {
    deps.log('warn', 'form.rate_limited', { form, ip: maskIp(ip) })
    return {
      ok: false,
      result: { ...fail(429, 'RATE_LIMITED'), retryAfterSec: rl.retryAfterSec },
    }
  }

  const body = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
  const spam = antispamSchema.safeParse({ website: body.website, startedAt: body.startedAt })
  if (!spam.success) return { ok: false, result: fail(400, 'VALIDATION') }

  if (spam.data.website !== '') {
    deps.log('warn', 'form.honeypot', { form, ip: maskIp(ip) })
    // Al bot se le responde como si todo hubiera ido bien, con una referencia que no existe.
    return {
      ok: false,
      result: {
        status: 200,
        body: { ok: true, reference: newReference('D', new Date(deps.now())) },
      },
    }
  }

  const started = spam.data.startedAt
  if (started === undefined || deps.now() - started < deps.config.minFillMs) {
    deps.log('warn', 'form.too_fast', { form, ip: maskIp(ip) })
    return { ok: false, result: fail(400, 'TOO_FAST') }
  }

  // En producción sin SMTP ni webhook la solicitud se perdería: mejor decirlo.
  if (deps.config.isProd && deps.mailer.mode === 'log' && !deps.config.webhook) {
    deps.log('error', 'form.unavailable', { form, reason: 'no_smtp_no_webhook' })
    return { ok: false, result: fail(503, 'UNAVAILABLE') }
  }
  return { ok: true }
}

async function notifyWebhook(deps: SubmissionDeps, payload: Record<string, unknown>) {
  if (!deps.config.webhook) return
  try {
    await postWebhook(deps.config.webhook, payload, deps.fetch)
  } catch (err) {
    deps.log('error', 'webhook.failed', { reference: payload.reference, error: String(err) })
  }
}

async function confirmCustomer(
  deps: SubmissionDeps,
  reference: string,
  send: () => Promise<void>,
): Promise<void> {
  if (!deps.config.mail.confirmToCustomer) return
  try {
    await send()
  } catch (err) {
    deps.log('error', 'mail.customer_failed', { reference, error: String(err) })
  }
}

export async function submitLead(
  raw: unknown,
  ip: string,
  deps: SubmissionDeps,
): Promise<SubmissionResult> {
  const g = gate('lead', raw, ip, deps)
  if (!g.ok) return g.result

  const parsed = leadSchema.safeParse(raw)
  if (!parsed.success) return fail(422, 'VALIDATION', fieldErrors(parsed.error))
  const lead = parsed.data
  const reference = newReference(lead.kind === 'demo' ? 'D' : 'C', new Date(deps.now()))

  try {
    await deps.mailer.send(leadSalesEmail(lead, reference, deps.config.mail.salesTo))
  } catch (err) {
    deps.log('error', 'mail.sales_failed', { reference, error: String(err) })
    // Si hay webhook, la solicitud no se pierde: se sigue adelante.
    if (!deps.config.webhook) return fail(502, 'SERVER')
  }
  await confirmCustomer(deps, reference, () =>
    deps.mailer.send(leadCustomerEmail(lead, reference, deps.config.mail.salesTo)),
  )
  await notifyWebhook(deps, {
    type: 'lead',
    reference,
    receivedAt: new Date(deps.now()).toISOString(),
    ...lead,
  })

  deps.log('info', 'lead.received', {
    reference,
    kind: lead.kind,
    country: lead.country,
    clients: lead.clients,
    email: maskEmail(lead.email),
    mail: deps.mailer.mode,
  })
  return { status: 200, body: { ok: true, reference } }
}

export async function submitPurchase(
  raw: unknown,
  ip: string,
  deps: SubmissionDeps,
): Promise<SubmissionResult> {
  const g = gate('purchase', raw, ip, deps)
  if (!g.ok) return g.result

  const parsed = purchaseSchema.safeParse(raw)
  if (!parsed.success) return fail(422, 'VALIDATION', fieldErrors(parsed.error))
  const p = parsed.data
  const plan = pricing.plans.find((x) => x.id === p.plan)!
  const amount = planPrice(plan, p.currency, p.period)
  const reference = newReference('P', new Date(deps.now()))

  const order: PurchaseOrder = {
    reference,
    plan: p.plan,
    period: p.period,
    currency: p.currency,
    amount,
    customer: {
      legalName: p.legalName,
      nit: p.nit,
      country: p.country,
      contactName: p.contactName,
      email: p.email,
      phone: p.phone,
    },
    locale: p.locale,
  }

  const provider = getPaymentProvider(deps.config.paymentProvider)
  let payment: PaymentResult = { provider: 'manual', kind: 'manual' }
  try {
    payment = await provider.createPayment(order)
  } catch (err) {
    deps.log('error', 'payment.failed', { reference, provider: provider.id, error: String(err) })
  }

  try {
    await deps.mailer.send(
      purchaseSalesEmail(p, reference, amount, payment.provider, deps.config.mail.salesTo),
    )
  } catch (err) {
    deps.log('error', 'mail.sales_failed', { reference, error: String(err) })
    if (!deps.config.webhook) return fail(502, 'SERVER')
  }
  await confirmCustomer(deps, reference, () =>
    deps.mailer.send(purchaseCustomerEmail(p, reference, amount, deps.config.mail.salesTo)),
  )
  await notifyWebhook(deps, {
    type: 'purchase',
    reference,
    receivedAt: new Date(deps.now()).toISOString(),
    amount,
    pricingConfirmed: pricing.confirmed,
    paymentProvider: payment.provider,
    ...p,
  })

  deps.log('info', 'purchase.received', {
    reference,
    plan: p.plan,
    period: p.period,
    currency: p.currency,
    country: p.country,
    email: maskEmail(p.email),
    provider: payment.provider,
    mail: deps.mailer.mode,
  })
  return {
    status: 200,
    body: {
      ok: true,
      reference,
      payment:
        payment.kind === 'redirect'
          ? { provider: payment.provider, kind: 'redirect', url: payment.url }
          : { provider: payment.provider, kind: 'manual' },
    },
  }
}
