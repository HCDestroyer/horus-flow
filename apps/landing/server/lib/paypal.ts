// Cliente mínimo de la API REST de PayPal: OAuth2 (client credentials), Orders v2 (crear y
// capturar) y verificación de la firma de los webhooks (POST /v1/notifications/
// verify-webhook-signature). Sin SDK: fetch inyectable para probarlo con un PayPal simulado.
//
// PayPal no admite quetzales (GTQ): los pagos con PayPal se cobran siempre en USD, con el
// importe en USD del plan y periodo elegidos.

export type PaypalMode = 'sandbox' | 'live'

export const PAYPAL_API = {
  sandbox: 'https://api-m.sandbox.paypal.com',
  live: 'https://api-m.paypal.com',
} as const

export interface PaypalResponse {
  ok: boolean
  status: number
  json(): Promise<unknown>
}

export type PaypalFetch = (
  url: string,
  init: { method: string; headers: Record<string, string>; body?: string; signal?: AbortSignal },
) => Promise<PaypalResponse>

export interface PaypalClientOptions {
  mode: PaypalMode
  clientId: string
  clientSecret: string
  fetch: PaypalFetch
  /** Solo pruebas: base de un PayPal simulado (PAYPAL_API_BASE). */
  apiBase?: string
  now?: () => number
}

export class PaypalError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly details?: unknown,
  ) {
    super(message)
  }
}

export interface PaypalOrderInput {
  reference: string
  /** Importe en USD con 2 decimales (lo calcula el servidor). */
  amountMinor: number
  description: string
  locale: 'es' | 'en'
}

export interface PaypalCapture {
  status: string
  orderId: string
  captureId: string
  amountMinor: number
  currency: string
  reference: string
}

/** "149.00" ⇄ 14900 */
export const minorToValue = (minor: number) => (minor / 100).toFixed(2)
export const valueToMinor = (value: string) => Math.round(Number.parseFloat(value) * 100)

interface CaptureUnit {
  reference_id?: string
  custom_id?: string
  payments?: {
    captures?: {
      id: string
      status: string
      custom_id?: string
      amount?: { value: string; currency_code: string }
    }[]
  }
}

export class PaypalClient {
  private token: { value: string; expiresAt: number } | null = null
  readonly base: string
  private readonly now: () => number

  constructor(private readonly opts: PaypalClientOptions) {
    this.base = (opts.apiBase || PAYPAL_API[opts.mode]).replace(/\/+$/, '')
    this.now = opts.now ?? Date.now
  }

  private async call<T>(
    method: string,
    path: string,
    body?: unknown,
    headers: Record<string, string> = {},
  ): Promise<T> {
    const token = await this.accessToken()
    const res = await this.opts.fetch(this.base + path, {
      method,
      headers: {
        authorization: `Bearer ${token}`,
        'content-type': 'application/json',
        accept: 'application/json',
        ...headers,
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(15_000),
    })
    const data = (await res.json().catch(() => null)) as T
    if (!res.ok) throw new PaypalError(`PayPal ${method} ${path} → ${res.status}`, res.status, data)
    return data
  }

  async accessToken(): Promise<string> {
    if (this.token && this.token.expiresAt > this.now() + 60_000) return this.token.value
    const basic = Buffer.from(`${this.opts.clientId}:${this.opts.clientSecret}`).toString('base64')
    const res = await this.opts.fetch(this.base + '/v1/oauth2/token', {
      method: 'POST',
      headers: {
        authorization: `Basic ${basic}`,
        'content-type': 'application/x-www-form-urlencoded',
        accept: 'application/json',
      },
      body: 'grant_type=client_credentials',
      signal: AbortSignal.timeout(15_000),
    })
    const data = (await res.json().catch(() => null)) as {
      access_token?: string
      expires_in?: number
    } | null
    if (!res.ok || !data?.access_token) throw new PaypalError('PayPal OAuth falló', res.status)
    this.token = {
      value: data.access_token,
      expiresAt: this.now() + (data.expires_in ?? 300) * 1000,
    }
    return this.token.value
  }

  /** Crea la orden. PayPal-Request-Id = referencia → reintentos idempotentes. */
  async createOrder(input: PaypalOrderInput): Promise<{ id: string; status: string }> {
    const data = await this.call<{ id: string; status: string }>(
      'POST',
      '/v2/checkout/orders',
      {
        intent: 'CAPTURE',
        purchase_units: [
          {
            reference_id: input.reference,
            custom_id: input.reference,
            invoice_id: input.reference,
            description: input.description.slice(0, 127),
            amount: { currency_code: 'USD', value: minorToValue(input.amountMinor) },
          },
        ],
        payment_source: {
          paypal: {
            experience_context: {
              brand_name: 'Horus Flow',
              locale: input.locale === 'es' ? 'es-GT' : 'en-US',
              shipping_preference: 'NO_SHIPPING',
              user_action: 'PAY_NOW',
            },
          },
        },
      },
      { 'paypal-request-id': `order-${input.reference}`, prefer: 'return=minimal' },
    )
    return { id: data.id, status: data.status }
  }

  async getOrder(orderId: string): Promise<{
    id: string
    status: string
    purchase_units?: (CaptureUnit & { amount?: { value: string; currency_code: string } })[]
  }> {
    return this.call('GET', `/v2/checkout/orders/${encodeURIComponent(orderId)}`)
  }

  /** Captura la orden aprobada por el comprador. Idempotente por PayPal-Request-Id. */
  async captureOrder(orderId: string, reference: string): Promise<PaypalCapture> {
    let data: { id: string; status: string; purchase_units?: CaptureUnit[] }
    try {
      data = await this.call(
        'POST',
        `/v2/checkout/orders/${encodeURIComponent(orderId)}/capture`,
        {},
        { 'paypal-request-id': `capture-${reference}`, prefer: 'return=representation' },
      )
    } catch (err) {
      // Ya capturada (p. ej. reintento tras un corte): se consulta el estado.
      const issue = (err as PaypalError).details as { details?: { issue?: string }[] } | undefined
      if (issue?.details?.some((d) => d.issue === 'ORDER_ALREADY_CAPTURED')) {
        data = await this.getOrder(orderId)
      } else throw err
    }
    const unit = data.purchase_units?.[0]
    const capture = unit?.payments?.captures?.[0]
    return {
      status: capture?.status ?? data.status,
      orderId: data.id,
      captureId: capture?.id ?? '',
      amountMinor: capture?.amount ? valueToMinor(capture.amount.value) : 0,
      currency: capture?.amount?.currency_code ?? '',
      reference: capture?.custom_id ?? unit?.custom_id ?? unit?.reference_id ?? '',
    }
  }

  /**
   * Verifica un webhook con la API de PayPal. Devuelve true solo si PayPal responde SUCCESS para
   * esas cabeceras, ese cuerpo y nuestro webhook id.
   */
  async verifyWebhook(
    headers: Record<string, string | undefined>,
    rawBody: string,
    webhookId: string,
  ): Promise<boolean> {
    const h = (k: string) => headers[k] ?? headers[k.toLowerCase()] ?? ''
    const required = [
      'paypal-auth-algo',
      'paypal-cert-url',
      'paypal-transmission-id',
      'paypal-transmission-sig',
      'paypal-transmission-time',
    ]
    if (!webhookId || required.some((k) => !h(k))) return false
    // La URL del certificado debe ser de PayPal (la API lo comprueba también).
    try {
      const cert = new URL(h('paypal-cert-url'))
      if (cert.protocol !== 'https:' || !/(^|\.)paypal\.com$/.test(cert.hostname)) {
        if (!this.opts.apiBase) return false
      }
    } catch {
      return false
    }
    let event: unknown
    try {
      event = JSON.parse(rawBody)
    } catch {
      return false
    }
    const data = await this.call<{ verification_status?: string }>(
      'POST',
      '/v1/notifications/verify-webhook-signature',
      {
        auth_algo: h('paypal-auth-algo'),
        cert_url: h('paypal-cert-url'),
        transmission_id: h('paypal-transmission-id'),
        transmission_sig: h('paypal-transmission-sig'),
        transmission_time: h('paypal-transmission-time'),
        webhook_id: webhookId,
        webhook_event: event,
      },
    )
    return data?.verification_status === 'SUCCESS'
  }
}
