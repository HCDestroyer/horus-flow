// GET /api/admin/payments — configuración de los métodos de pago. Nunca devuelve el client id
// ni el secret de PayPal: solo si están guardados y los últimos 4 caracteres del client id.
import { useApp } from '../../../lib/context'
import { readPayments } from '../../../lib/payment-config'

export default defineEventHandler((event) => {
  const app = useApp()
  const base = useRuntimeConfig(event).public.siteUrl.replace(/\/+$/, '')
  return {
    ...readPayments(app.db, app.key.box),
    dataKey: { origin: app.key.origin, error: app.key.error ?? '' },
    webhookUrl: `${base}/api/payments/paypal/webhook`,
  }
})
