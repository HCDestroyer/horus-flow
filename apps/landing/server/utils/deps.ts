// Dependencias de los formularios para el proceso del servidor (una instancia por proceso) y
// utilidades h3 comunes a las rutas.
import type { H3Event } from 'h3'
import { buildTrustList, resolveClientIp } from './client-ip'
import { loadConfig } from './config'
import { log } from './log'
import { createMailer } from './mailer'
import { RateLimiter } from './rate-limit'
import type { SubmissionDeps, SubmissionResult } from './submissions'
import type { FetchLike } from './webhook'

let deps: (SubmissionDeps & { trust: ReturnType<typeof buildTrustList> }) | null = null

export function useSubmissionDeps() {
  if (!deps) {
    const config = loadConfig(process.env)
    deps = {
      config,
      mailer: createMailer(config.mail),
      limiter: new RateLimiter(config.rateLimit.max, config.rateLimit.windowMs),
      log,
      fetch: globalThis.fetch as unknown as FetchLike,
      now: () => Date.now(),
      trust: buildTrustList(config.trustedProxies),
    }
    log('info', 'landing.config', {
      mail: config.mail.mode,
      webhook: Boolean(config.webhook),
      trustedProxies: config.trustedProxies.length,
      payment: config.paymentProvider,
    })
  }
  return deps
}

const MAX_BODY_BYTES = 32 * 1024

/** Lee el cuerpo JSON con un tope de tamaño; null si no es JSON válido. */
export async function readJsonBody(event: H3Event): Promise<unknown> {
  const length = Number(getRequestHeader(event, 'content-length') ?? 0)
  if (length > MAX_BODY_BYTES) {
    throw createError({ statusCode: 413, statusMessage: 'Payload Too Large' })
  }
  const type = getRequestHeader(event, 'content-type') ?? ''
  if (!type.includes('application/json')) {
    throw createError({ statusCode: 415, statusMessage: 'Unsupported Media Type' })
  }
  const text = (await readRawBody(event, 'utf8')) ?? ''
  if (text.length > MAX_BODY_BYTES) {
    throw createError({ statusCode: 413, statusMessage: 'Payload Too Large' })
  }
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

export function requestClientIp(event: H3Event): string {
  const d = useSubmissionDeps()
  return resolveClientIp(
    event.node.req.socket?.remoteAddress,
    event.node.req.headers['x-forwarded-for'],
    d.trust,
  )
}

export function sendResult(event: H3Event, result: SubmissionResult) {
  setResponseStatus(event, result.status)
  if (result.retryAfterSec) setResponseHeader(event, 'Retry-After', result.retryAfterSec)
  return result.body
}
