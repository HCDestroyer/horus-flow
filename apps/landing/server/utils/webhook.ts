// Webhook opcional para un CRM (LEADS_WEBHOOK_URL). POST JSON con firma HMAC-SHA256 del cuerpo
// en X-Horus-Signature (sha256=<hex>) si hay LEADS_WEBHOOK_SECRET. Un fallo no rompe el envío:
// se registra y el correo a ventas sigue siendo la vía principal.
import { createHmac } from 'node:crypto'

export type FetchLike = (
  input: string,
  init: { method: string; headers: Record<string, string>; body: string; signal?: AbortSignal },
) => Promise<{ ok: boolean; status: number }>

export function signBody(secret: string, body: string): string {
  return 'sha256=' + createHmac('sha256', secret).update(body).digest('hex')
}

export async function postWebhook(
  hook: { url: string; secret: string },
  payload: unknown,
  fetchImpl: FetchLike,
  timeoutMs = 5000,
): Promise<void> {
  const body = JSON.stringify(payload)
  const headers: Record<string, string> = {
    'content-type': 'application/json',
    'user-agent': 'horus-landing',
  }
  if (hook.secret) headers['x-horus-signature'] = signBody(hook.secret, body)
  const res = await fetchImpl(hook.url, {
    method: 'POST',
    headers,
    body,
    signal: AbortSignal.timeout(timeoutMs),
  })
  if (!res.ok) throw new Error(`webhook respondió ${res.status}`)
}
