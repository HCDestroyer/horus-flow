import { randomBytes } from 'node:crypto'
import { beforeEach, describe, expect, it } from 'vitest'
import { publishCatalog, readCatalog, seedIfEmpty } from '../../server/lib/catalog'
import {
  CheckoutError,
  capturePaypalOrder,
  chooseManualMethod,
  createPaypalOrder,
  handlePaypalWebhook,
  type CheckoutDeps,
} from '../../server/lib/checkout'
import { openDatabase, type DB } from '../../server/lib/db'
import {
  neoLink,
  publicPayments,
  readPayments,
  writeNeo,
  writePaypal,
  writeTransfer,
} from '../../server/lib/payment-config'
import { getRequestByReference } from '../../server/lib/requests'
import { SecretBox } from '../../server/lib/secrets'
import { loadConfig } from '../../server/utils/config'
import type { MailMessage, Mailer } from '../../server/utils/mailer'
import { RateLimiter } from '../../server/utils/rate-limit'
import { submitPurchase, type SubmissionDeps } from '../../server/utils/submissions'
import { createFakePaypal, webhookHeaders } from '../support/fake-paypal.mjs'

const NOW = Date.UTC(2026, 9, 10, 12, 0, 0)
const IP = '203.0.113.7'

function purchase(over: Record<string, unknown> = {}) {
  return {
    plan: 'medium',
    period: 'annual',
    currency: 'GTQ',
    legalName: 'Fibra Sur, S.A.',
    nit: '1234567-8',
    country: 'GT',
    address: '',
    contactName: 'Ana Pérez',
    email: 'ana@fibrasur.example',
    phone: '',
    notes: '',
    consent: true,
    locale: 'es',
    website: '',
    startedAt: NOW - 10_000,
    ...over,
  }
}

interface Ctx {
  db: DB
  box: SecretBox
  sent: MailMessage[]
  paypal: ReturnType<typeof createFakePaypal>
  sub: SubmissionDeps
  co: CheckoutDeps
}

function setup(): Ctx {
  const db = openDatabase(':memory:')
  seedIfEmpty(db)
  const box = new SecretBox(randomBytes(32))
  const paypal = createFakePaypal()
  writePaypal(db, box, {
    enabled: true,
    mode: 'sandbox',
    webhookId: paypal.webhookId,
    clientId: paypal.clientId,
    clientSecret: paypal.clientSecret,
  })
  writeNeo(db, {
    enabled: true,
    links: { medium: { annual: 'https://pagos.neo.example/l/medium-anual', monthly: '' } },
  })
  writeTransfer(db, {
    enabled: true,
    accounts: [
      {
        bank: 'Banco Industrial',
        type: { es: 'Monetaria', en: 'Checking' },
        number: '000-123456-7',
        holder: 'Connection And Solutions Company, S.A.',
        currency: 'GTQ',
      },
    ],
    instructions: { es: 'Envía el comprobante.', en: 'Send the receipt.' },
  })
  const sent: MailMessage[] = []
  const mailer: Mailer = { mode: 'smtp', send: async (m) => void sent.push(m) }
  const config = loadConfig({
    SMTP_HOST: 'smtp.example',
    PAYPAL_API_BASE: 'https://paypal.simulado',
  })
  const sub: SubmissionDeps = {
    config,
    mailer,
    limiter: new RateLimiter(100, 60_000),
    log: () => {},
    fetch: async () => ({ ok: true, status: 200 }),
    now: () => NOW,
    store: {
      db,
      catalog: () => readCatalog(db),
      payments: () => publicPayments(db, box),
      neoLink: (plan, period) => neoLink(readPayments(db, box), plan as never, period),
    },
  }
  const co: CheckoutDeps = {
    db,
    box,
    config,
    mailer,
    log: () => {},
    now: () => NOW,
    catalog: () => readCatalog(db),
    paypalFetch: paypal.fetch,
  }
  return { db, box, sent, paypal, sub, co }
}

async function newPurchase(c: Ctx, over: Record<string, unknown> = {}) {
  const res = await submitPurchase(purchase(over), IP, c.sub)
  expect(res.status).toBe(200)
  return res.body as {
    reference: string
    accessToken: string
    amount: number
    amountUsd: number
    methods: Record<string, boolean>
  }
}

async function expectCheckoutError(p: Promise<unknown>, code: string) {
  await expect(p).rejects.toSatisfy((e: unknown) => e instanceof CheckoutError && e.code === code)
}

describe('cálculo del importe en el servidor', () => {
  let c: Ctx
  beforeEach(() => {
    c = setup()
  })

  it('el importe sale de la base de datos y se ignora cualquier importe del navegador', async () => {
    const r = await newPurchase(c, { amount: 1, price: 1, amountUsd: 1 })
    expect(r.amount).toBe(30900)
    expect(r.amountUsd).toBe(3990)
    expect(r.methods).toEqual({ paypal: true, neo: true, transfer: true })
    const row = getRequestByReference(c.db, r.reference)!
    expect(row).toMatchObject({ amount_minor: 3_090_000, amount_usd_minor: 399_000, status: 'new' })
    // El token de acceso no se guarda en claro.
    expect(row.access_token_hash).not.toBe(r.accessToken)
  })

  it('usa el precio publicado en ese momento (un cambio posterior no altera la solicitud)', async () => {
    const cat = readCatalog(c.db)
    cat.plans.find((p) => p.id === 'medium')!.prices!.USD.annual = 4290
    publishCatalog(c.db, cat, { id: 1, email: 'ana@kns.gt' })
    const r = await newPurchase(c, { currency: 'USD' })
    expect(r.amount).toBe(4290)
    cat.plans.find((p) => p.id === 'medium')!.prices!.USD.annual = 1
    publishCatalog(c.db, cat, { id: 1, email: 'ana@kns.gt' })
    expect(getRequestByReference(c.db, r.reference)!.amount_usd_minor).toBe(429_000)
  })

  it('rechaza planes a medida u ocultos', async () => {
    expect((await submitPurchase(purchase({ plan: 'enterprise' }), IP, c.sub)).body).toMatchObject({
      fields: { plan: 'invalid' },
    })
    const cat = readCatalog(c.db)
    cat.plans.find((p) => p.id === 'small')!.visible = false
    publishCatalog(c.db, cat, { id: 1, email: 'ana@kns.gt' })
    expect((await submitPurchase(purchase({ plan: 'small' }), IP, c.sub)).status).toBe(422)
  })

  it('Neo solo se ofrece si hay link para ese plan y periodo', async () => {
    expect((await newPurchase(c, { period: 'monthly' })).methods.neo).toBe(false)
  })
})

describe('PayPal (simulado)', () => {
  let c: Ctx
  beforeEach(() => {
    c = setup()
  })

  it('crear y capturar: importe en USD del servidor, pagada, correo al cliente y a info@kns.gt', async () => {
    const r = await newPurchase(c)
    const { orderId } = await createPaypalOrder(c.co, r.reference, r.accessToken)
    const order = c.paypal.orders.get(orderId)
    expect(order.unit.amount).toEqual({ currency_code: 'USD', value: '3990.00' })
    expect(order.unit.custom_id).toBe(r.reference)
    expect(getRequestByReference(c.db, r.reference)!.status).toBe('pending_payment')

    // Idempotencia: otra llamada devuelve la misma orden (PayPal-Request-Id = referencia).
    expect((await createPaypalOrder(c.co, r.reference, r.accessToken)).orderId).toBe(orderId)

    c.paypal.approve(orderId)
    c.sent.length = 0
    expect(await capturePaypalOrder(c.co, r.reference, r.accessToken, orderId)).toEqual({
      status: 'paid',
    })
    const row = getRequestByReference(c.db, r.reference)!
    expect(row).toMatchObject({
      status: 'paid',
      payment_method: 'paypal',
      paid_amount_minor: 399_000,
      paid_currency: 'USD',
    })
    expect(c.sent.map((m) => m.to).sort()).toEqual(['ana@fibrasur.example', 'info@kns.gt'])
    expect(c.sent.find((m) => m.to === 'info@kns.gt')!.subject).toContain('Pago confirmado')

    // Repetir la captura no duplica correos.
    expect(await capturePaypalOrder(c.co, r.reference, r.accessToken, orderId)).toEqual({
      status: 'paid',
    })
    expect(c.sent).toHaveLength(2)
  })

  it('sin el token de la solicitud no se puede pagar ni consultar', async () => {
    const r = await newPurchase(c)
    await expectCheckoutError(createPaypalOrder(c.co, r.reference, 'x'.repeat(32)), 'NOT_FOUND')
  })

  it('una captura no aprobada o de otra orden no marca nada como pagado', async () => {
    const r = await newPurchase(c)
    const { orderId } = await createPaypalOrder(c.co, r.reference, r.accessToken)
    await expectCheckoutError(
      capturePaypalOrder(c.co, r.reference, r.accessToken, 'OTRA-ORDEN'),
      'ORDER_MISMATCH',
    )
    await expect(capturePaypalOrder(c.co, r.reference, r.accessToken, orderId)).rejects.toThrow()
    expect(getRequestByReference(c.db, r.reference)!.status).toBe('pending_payment')
  })

  it('PayPal desactivado: no se crean órdenes', async () => {
    const r = await newPurchase(c)
    writePaypal(c.db, c.box, { enabled: false, mode: 'sandbox', webhookId: c.paypal.webhookId })
    await expectCheckoutError(
      createPaypalOrder(c.co, r.reference, r.accessToken),
      'PAYPAL_DISABLED',
    )
  })

  it('webhook verificado PAYMENT.CAPTURE.COMPLETED marca la solicitud como pagada (una sola vez)', async () => {
    const r = await newPurchase(c)
    const { orderId } = await createPaypalOrder(c.co, r.reference, r.accessToken)
    c.paypal.approve(orderId)
    // Captura hecha en PayPal sin que el navegador vuelva (p. ej. se cerró la ventana).
    c.paypal.handle(
      'POST',
      `https://x/v2/checkout/orders/${orderId}/capture`,
      { authorization: 'Bearer A21-token' },
      '{}',
    )
    const event = c.paypal.captureEvent(orderId)
    c.sent.length = 0
    const res = await handlePaypalWebhook(c.co, webhookHeaders(true), JSON.stringify(event))
    expect(res).toEqual({ status: 200, handled: 'paid' })
    expect(getRequestByReference(c.db, r.reference)!.status).toBe('paid')
    expect(c.sent).toHaveLength(2)
    // Reenvío del mismo evento: idempotente.
    expect(await handlePaypalWebhook(c.co, webhookHeaders(true), JSON.stringify(event))).toEqual({
      status: 200,
      handled: 'duplicate',
    })
    expect(c.sent).toHaveLength(2)
    // La verificación se pidió a PayPal con nuestro webhook id.
    const verify = c.paypal.calls.find(
      (x: { path: string }) => x.path === '/v1/notifications/verify-webhook-signature',
    )
    expect(JSON.parse(verify.body).webhook_id).toBe('WH-TEST')
  })

  it('webhook NO verificado: 400 y nada cambia', async () => {
    const r = await newPurchase(c)
    const { orderId } = await createPaypalOrder(c.co, r.reference, r.accessToken)
    c.paypal.approve(orderId)
    c.paypal.handle(
      'POST',
      `https://x/v2/checkout/orders/${orderId}/capture`,
      { authorization: 'Bearer A21-token' },
      '{}',
    )
    const event = c.paypal.captureEvent(orderId)
    expect(await handlePaypalWebhook(c.co, webhookHeaders(false), JSON.stringify(event))).toEqual({
      status: 400,
      handled: 'rejected',
    })
    // Sin cabeceras de firma ni siquiera se consulta a PayPal.
    expect(await handlePaypalWebhook(c.co, {}, JSON.stringify(event))).toMatchObject({
      status: 400,
    })
    expect(getRequestByReference(c.db, r.reference)!.status).toBe('pending_payment')
    expect(c.db.prepare('SELECT COUNT(*) AS n FROM paypal_events').get()).toEqual({ n: 0 })
  })

  it('webhook verificado con importe distinto: se ignora', async () => {
    const r = await newPurchase(c)
    const { orderId } = await createPaypalOrder(c.co, r.reference, r.accessToken)
    c.paypal.approve(orderId)
    c.paypal.handle(
      'POST',
      `https://x/v2/checkout/orders/${orderId}/capture`,
      { authorization: 'Bearer A21-token' },
      '{}',
    )
    const event = c.paypal.captureEvent(orderId, {
      amount: { currency_code: 'USD', value: '1.00' },
    })
    expect(await handlePaypalWebhook(c.co, webhookHeaders(true), JSON.stringify(event))).toEqual({
      status: 200,
      handled: 'ignored',
    })
    expect(getRequestByReference(c.db, r.reference)!.status).toBe('pending_payment')
  })
})

describe('link Neo y transferencia', () => {
  let c: Ctx
  beforeEach(() => {
    c = setup()
  })

  it('Neo: devuelve el link del plan y periodo y deja la solicitud pendiente de pago', async () => {
    const r = await newPurchase(c)
    c.sent.length = 0
    const res = await chooseManualMethod(c.co, r.reference, r.accessToken, 'neo')
    expect(res).toMatchObject({
      method: 'neo',
      neoUrl: 'https://pagos.neo.example/l/medium-anual',
      reference: r.reference,
    })
    expect(getRequestByReference(c.db, r.reference)).toMatchObject({
      status: 'pending_payment',
      payment_method: 'neo',
    })
    expect(c.sent).toHaveLength(1)
    expect(c.sent[0]!.text).toContain('https://pagos.neo.example/l/medium-anual')
  })

  it('transferencia: datos bancarios y referencia, por correo una sola vez', async () => {
    const r = await newPurchase(c, { locale: 'en' })
    c.sent.length = 0
    const res = await chooseManualMethod(c.co, r.reference, r.accessToken, 'transfer')
    expect(res.accounts![0]!.number).toBe('000-123456-7')
    await chooseManualMethod(c.co, r.reference, r.accessToken, 'transfer')
    expect(c.sent).toHaveLength(1)
    expect(c.sent[0]!.subject).toBe(`Horus Flow: payment instructions ${r.reference}`)
    expect(c.sent[0]!.text).toContain('000-123456-7')
    expect(c.sent[0]!.text).toContain(r.reference)
    expect(c.sent[0]!.text).toContain('Checking')
  })
})
