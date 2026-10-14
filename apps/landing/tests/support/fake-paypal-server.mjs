// PayPal simulado como servidor HTTP para el e2e (playwright.config.ts lo arranca y la landing
// lo usa con PAYPAL_API_BASE). Además de la API simulada expone POST /__approve/:orderId, que
// hace las veces del comprador aprobando el pago en la ventana de PayPal.
//
//   PORT=4175 node tests/support/fake-paypal-server.mjs
import { createServer } from 'node:http'
import { createFakePaypal } from './fake-paypal.mjs'

const port = Number(process.env.PORT ?? 4175)
const paypal = createFakePaypal()

createServer((req, res) => {
  let body = ''
  req.on('data', (c) => (body += c))
  req.on('end', () => {
    const url = new URL(req.url ?? '/', `http://127.0.0.1:${port}`)
    const approve = /^\/__approve\/([^/]+)$/.exec(url.pathname)
    let status = 200
    let out = { ok: true }
    if (url.pathname === '/__health') {
      out = { ok: true }
    } else if (approve && req.method === 'POST') {
      paypal.approve(decodeURIComponent(approve[1]))
    } else {
      const r = paypal.handle(req.method ?? 'GET', url.toString(), req.headers, body)
      status = r.status
      out = r.body
    }
    res.writeHead(status, { 'content-type': 'application/json' })
    res.end(JSON.stringify(out))
  })
}).listen(port, '127.0.0.1')
