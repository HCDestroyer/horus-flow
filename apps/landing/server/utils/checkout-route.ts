// Utilidades comunes de las rutas de pago (/api/checkout/**): rate limit por IP, cuerpo con
// referencia + token y traducción de errores.
import type { H3Event } from 'h3'
import { z } from 'zod'
import { CheckoutError } from '../lib/checkout'
import { PaypalError } from '../lib/paypal'
import { REFERENCE_RE } from './reference'
import { RateLimiter } from './rate-limit'

const checkoutLimiter = new RateLimiter(40, 10 * 60_000)

export const checkoutBaseSchema = z.object({
  reference: z.string().regex(REFERENCE_RE),
  token: z.string().min(20).max(100),
})

export async function readCheckoutBody<S extends z.ZodType>(
  event: H3Event,
  schema: S,
): Promise<z.output<S>> {
  const rl = checkoutLimiter.hit(requestClientIp(event))
  if (!rl.allowed) {
    setResponseHeader(event, 'Retry-After', rl.retryAfterSec)
    throw createError({ statusCode: 429, data: { ok: false, code: 'RATE_LIMITED' } })
  }
  const parsed = schema.safeParse(await readJsonBody(event))
  if (!parsed.success)
    throw createError({ statusCode: 422, data: { ok: false, code: 'VALIDATION' } })
  return parsed.data
}

export function checkoutFail(err: unknown): never {
  if (err instanceof CheckoutError) {
    throw createError({ statusCode: err.status, data: { ok: false, code: err.code } })
  }
  if (err instanceof PaypalError) {
    useSubmissionDeps().log('error', 'paypal.api_error', { status: err.status })
    throw createError({ statusCode: 502, data: { ok: false, code: 'PAYPAL_ERROR' } })
  }
  throw err
}
