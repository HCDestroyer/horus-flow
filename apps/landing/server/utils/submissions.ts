// Lógica de los formularios, independiente de h3 para poder probarla: rate limit → antispam
// (honeypot y tiempo mínimo) → validación zod → guardar en la base de datos → correo a ventas →
// confirmación al cliente → webhook opcional. Las rutas (server/api/*.post.ts) solo traducen
// petición ↔ resultado.
import { catalogPrice, type Catalog, type PublicPayments } from '../../shared/catalog'
import {
  antispamSchema,
  fieldErrors,
  leadSchema,
  purchaseSchema,
  type PurchaseOk,
  type SubmitError,
  type SubmitOk,
} from '../../shared/schemas'
import type { DB } from '../lib/db'
import { insertRequest } from '../lib/requests'
import type { ServerConfig } from './config'
import {
  leadCustomerEmail,
  leadSalesEmail,
  planName,
  purchaseCustomerEmail,
  purchaseSalesEmail,
} from './emails'
import { maskEmail, maskIp, type LogFn } from './log'
import type { Mailer } from './mailer'
import type { RateLimiter } from './rate-limit'
import { newReference } from './reference'
import { postWebhook, type FetchLike } from './webhook'

/** Base de datos y lo que la landing lee de ella (catálogo y métodos de pago activos). */
export interface SubmissionStore {
  db: DB
  catalog: () => Catalog
  payments: () => PublicPayments
  /** Link Neo configurado para un plan y periodo, o null. */
  neoLink: (plan: string, period: 'monthly' | 'annual') => string | null
}

export interface SubmissionDeps {
  config: ServerConfig
  mailer: Mailer
  limiter: RateLimiter
  log: LogFn
  fetch: FetchLike
  now: () => number
  /** null solo en tests de la lógica de correo: sin base de datos no se guarda nada. */
  store: SubmissionStore | null
}

export interface SubmissionResult {
  status: number
  body: SubmitOk | PurchaseOk | SubmitError
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

  // En producción sin base de datos, SMTP ni webhook la solicitud se perdería: mejor decirlo.
  if (deps.config.isProd && !deps.store && deps.mailer.mode === 'log' && !deps.config.webhook) {
    deps.log('error', 'form.unavailable', { form, reason: 'no_store_no_smtp_no_webhook' })
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

/** Aviso a ventas. Si falla y la solicitud no quedó guardada en ningún sitio → 502. */
async function notifySales(
  deps: SubmissionDeps,
  reference: string,
  stored: boolean,
  send: () => Promise<void>,
): Promise<boolean> {
  try {
    await send()
    return true
  } catch (err) {
    deps.log('error', 'mail.sales_failed', { reference, error: String(err) })
    return stored || Boolean(deps.config.webhook)
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

  let stored = false
  if (deps.store) {
    insertRequest(
      deps.store.db,
      {
        reference,
        kind: lead.kind,
        locale: lead.locale,
        name: lead.name,
        company: lead.company,
        email: lead.email,
        country: lead.country,
        data: { ...lead, consent: true },
      },
      deps.now(),
    )
    stored = true
  }

  const ok = await notifySales(deps, reference, stored, () =>
    deps.mailer.send(leadSalesEmail(lead, reference, deps.config.mail.salesTo)),
  )
  if (!ok) return fail(502, 'SERVER')
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
    stored,
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

  // El importe sale SIEMPRE del catálogo del servidor (base de datos), nunca del navegador.
  const catalog = deps.store?.catalog() ?? null
  const plan = catalog?.plans.find((x) => x.id === p.plan)
  if (catalog && (!plan || !plan.visible || !plan.prices)) {
    return fail(422, 'VALIDATION', { plan: 'invalid' })
  }
  const amount = catalog && plan ? catalogPrice(catalog, plan, p.currency, p.period) : null
  const amountUsd = catalog && plan ? catalogPrice(catalog, plan, 'USD', p.period) : null
  const reference = newReference('P', new Date(deps.now()))
  const name = planName(catalog, p.plan, 'es')

  let accessToken: string | null = null
  if (deps.store) {
    accessToken = insertRequest(
      deps.store.db,
      {
        reference,
        kind: 'purchase',
        locale: p.locale,
        name: p.contactName,
        company: p.legalName,
        email: p.email,
        country: p.country,
        data: { ...p, consent: true },
        plan: p.plan,
        period: p.period,
        currency: p.currency,
        amountMinor: amount === null ? null : Math.round(amount * 100),
        amountUsdMinor: amountUsd === null ? null : Math.round(amountUsd * 100),
      },
      deps.now(),
    ).accessToken
  }

  const ok = await notifySales(deps, reference, Boolean(deps.store), () =>
    deps.mailer.send(purchaseSalesEmail(p, reference, amount, name, deps.config.mail.salesTo)),
  )
  if (!ok) return fail(502, 'SERVER')
  await confirmCustomer(deps, reference, () =>
    deps.mailer.send(
      purchaseCustomerEmail(
        p,
        reference,
        amount,
        planName(catalog, p.plan, p.locale),
        deps.config.mail.salesTo,
      ),
    ),
  )
  await notifyWebhook(deps, {
    type: 'purchase',
    reference,
    receivedAt: new Date(deps.now()).toISOString(),
    amount,
    pricingConfirmed: catalog?.confirmed ?? false,
    ...p,
  })

  // Métodos de pago que se ofrecen: solo con importe publicable.
  const pay = deps.store?.payments()
  const methods =
    amount !== null && pay
      ? {
          paypal: pay.paypal.enabled && amountUsd !== null,
          neo: pay.neo.enabled && Boolean(deps.store?.neoLink(p.plan, p.period)),
          transfer: pay.transfer.enabled,
        }
      : { paypal: false, neo: false, transfer: false }

  deps.log('info', 'purchase.received', {
    reference,
    plan: p.plan,
    period: p.period,
    currency: p.currency,
    country: p.country,
    email: maskEmail(p.email),
    mail: deps.mailer.mode,
    stored: Boolean(deps.store),
  })
  return {
    status: 200,
    body: {
      ok: true,
      reference,
      accessToken: accessToken ?? '',
      amount,
      currency: p.currency,
      amountUsd,
      methods,
    },
  }
}
