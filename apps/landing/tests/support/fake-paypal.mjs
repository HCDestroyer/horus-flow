// PayPal simulado para los tests: OAuth2, Orders v2 (crear, consultar, capturar) y verificación
// de firmas de webhooks. Lo usan los tests de servidor (como fetch inyectado) y el e2e (como
// servidor HTTP, ver fake-paypal-server.mjs). No es PayPal: solo imita las respuestas que usa
// server/lib/paypal.ts.
//
// - Un webhook se da por verificado si su cabecera paypal-transmission-sig es "firma-valida"
//   y el webhook_id coincide con el esperado.
// - approve(orderId) simula que el comprador aprueba el pago en la ventana de PayPal.

export function createFakePaypal({
  clientId = 'client-test',
  clientSecret = 'secret-test',
  webhookId = 'WH-TEST',
} = {}) {
  let seq = 0
  /** @type {Map<string, any>} */
  const orders = new Map()
  /** @type {Map<string, string>} PayPal-Request-Id → orderId */
  const requestIds = new Map()
  const calls = []

  function json(status, body) {
    return { status, body }
  }

  function handle(method, url, headers, rawBody) {
    const u = new URL(url)
    const path = u.pathname
    const h = Object.fromEntries(
      Object.entries(headers ?? {}).map(([k, v]) => [k.toLowerCase(), v]),
    )
    calls.push({ method, path, headers: h, body: rawBody })

    if (path === '/v1/oauth2/token' && method === 'POST') {
      const expected = 'Basic ' + Buffer.from(`${clientId}:${clientSecret}`).toString('base64')
      if (h.authorization !== expected) return json(401, { error: 'invalid_client' })
      return json(200, { access_token: 'A21-token', token_type: 'Bearer', expires_in: 32400 })
    }
    if (h.authorization !== 'Bearer A21-token') return json(401, { name: 'AUTHENTICATION_FAILURE' })

    if (path === '/v2/checkout/orders' && method === 'POST') {
      const rid = h['paypal-request-id']
      if (rid && requestIds.has(rid)) {
        const o = orders.get(requestIds.get(rid))
        return json(200, { id: o.id, status: o.status })
      }
      const body = JSON.parse(rawBody)
      const id = `5O${String(++seq).padStart(6, '0')}TEST`
      const unit = body.purchase_units[0]
      orders.set(id, { id, status: 'CREATED', unit, captures: [] })
      if (rid) requestIds.set(rid, id)
      return json(201, { id, status: 'CREATED' })
    }
    const m = /^\/v2\/checkout\/orders\/([^/]+)(\/capture)?$/.exec(path)
    if (m) {
      const o = orders.get(decodeURIComponent(m[1]))
      if (!o) return json(404, { name: 'RESOURCE_NOT_FOUND' })
      const view = () => ({
        id: o.id,
        status: o.status,
        purchase_units: [
          {
            reference_id: o.unit.reference_id,
            custom_id: o.unit.custom_id,
            amount: o.unit.amount,
            payments: { captures: o.captures },
          },
        ],
      })
      if (!m[2] && method === 'GET') return json(200, view())
      if (m[2] && method === 'POST') {
        if (o.status === 'COMPLETED') {
          return json(422, {
            name: 'UNPROCESSABLE_ENTITY',
            details: [{ issue: 'ORDER_ALREADY_CAPTURED' }],
          })
        }
        if (o.status !== 'APPROVED') {
          return json(422, {
            name: 'UNPROCESSABLE_ENTITY',
            details: [{ issue: 'ORDER_NOT_APPROVED' }],
          })
        }
        o.status = 'COMPLETED'
        o.captures = [
          {
            id: `CAP${String(++seq).padStart(8, '0')}`,
            status: 'COMPLETED',
            custom_id: o.unit.custom_id,
            amount: o.unit.amount,
          },
        ]
        return json(201, view())
      }
    }
    if (path === '/v1/notifications/verify-webhook-signature' && method === 'POST') {
      const body = JSON.parse(rawBody)
      const ok = body.transmission_sig === 'firma-valida' && body.webhook_id === webhookId
      return json(200, { verification_status: ok ? 'SUCCESS' : 'FAILURE' })
    }
    return json(404, { name: 'NOT_FOUND' })
  }

  /** fetch compatible con server/lib/paypal.ts (PaypalFetch). */
  async function fetch(url, init) {
    const r = handle(init.method, url, init.headers, init.body ?? '')
    return { ok: r.status >= 200 && r.status < 300, status: r.status, json: async () => r.body }
  }

  return {
    clientId,
    clientSecret,
    webhookId,
    orders,
    calls,
    handle,
    fetch,
    approve(orderId) {
      const o = orders.get(orderId)
      if (o) o.status = 'APPROVED'
    },
    /** Evento PAYMENT.CAPTURE.COMPLETED como lo enviaría PayPal para una orden ya capturada. */
    captureEvent(orderId, over = {}) {
      const o = orders.get(orderId)
      const cap = o.captures[0]
      return {
        id: `WH-EVT-${++seq}`,
        event_type: 'PAYMENT.CAPTURE.COMPLETED',
        resource: {
          id: cap.id,
          status: 'COMPLETED',
          custom_id: o.unit.custom_id,
          amount: cap.amount,
          supplementary_data: { related_ids: { order_id: o.id } },
          ...over,
        },
      }
    },
  }
}

/** Cabeceras de un webhook de PayPal (firma válida o no para el simulador). */
export function webhookHeaders(valid = true) {
  return {
    'paypal-auth-algo': 'SHA256withRSA',
    'paypal-cert-url': 'https://api.sandbox.paypal.com/v1/notifications/certs/CERT-360caa42',
    'paypal-transmission-id': 'b2384410-f8d2-11e6-8d37-2f6a9b6b7d0c',
    'paypal-transmission-sig': valid ? 'firma-valida' : 'firma-falsa',
    'paypal-transmission-time': '2026-10-10T12:00:00Z',
  }
}
