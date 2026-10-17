import type { RouterTlsFingerprint } from '~~/types/api'
import { forbidden, json, problem } from './http'
import type { MockRouter } from './inventory'
import { mockState } from './state'

/**
 * Huella TLS del router (`GET/POST /routers/{id}/tls-fingerprint…`). Semilla: los routers que ya
 * exportan tienen la huella confirmada; los que tienen túnel pero aún no exportan, una huella
 * observada pendiente; sin túnel, sin leer. `localStorage['horus.mock.tlsChanged']` con el id de
 * un router simula que su certificado cambió (aviso y re-fijado en las pruebas e2e). Huellas
 * obviamente falsas (bytes repetidos).
 */
export const MOCK_FP_PINNED = Array(32).fill('5A').join(':')
export const MOCK_FP_NEW = Array(32).fill('C3').join(':')

function changedRouter(): string | null {
  try {
    return window.localStorage.getItem('horus.mock.tlsChanged')
  } catch {
    return null
  }
}

export function tlsPinOf(item: MockRouter, now: Date): RouterTlsFingerprint {
  const saved = mockState.tlsPins.get(item.router.id)
  if (saved) return saved
  const base: RouterTlsFingerprint = {
    router_id: item.router.id,
    status: 'not_captured',
    pinned_sha256: null,
    observed_sha256: null,
    observed_at: null,
    confirmed_at: null,
    confirmed_by: null,
    last_result: null,
    last_used_at: null,
  }
  const at = new Date(now.getTime() - 3_600_000).toISOString()
  if (changedRouter() === item.router.id) {
    return {
      ...base,
      status: 'changed',
      pinned_sha256: MOCK_FP_PINNED,
      observed_sha256: MOCK_FP_NEW,
      observed_at: at,
      confirmed_at: new Date(now.getTime() - 30 * 86_400_000).toISOString(),
      last_result: 'tls_fingerprint_changed',
      last_used_at: at,
    }
  }
  switch (item.router.onboarding_state) {
    case 'exporting':
      return {
        ...base,
        status: 'confirmed',
        pinned_sha256: MOCK_FP_PINNED,
        confirmed_at: new Date(now.getTime() - 30 * 86_400_000).toISOString(),
        last_result: 'ok',
        last_used_at: at,
      }
    case 'pending_configuration':
      return base
    default:
      return {
        ...base,
        status: 'pending_confirmation',
        observed_sha256: MOCK_FP_PINNED,
        observed_at: at,
      }
  }
}

interface PinRouteContext {
  method: string
  path: string
  body: Record<string, unknown>
  can: (permission: string) => boolean
  now: Date
  userId: string
  reauthRequired?: boolean
}

export function tlsPinRoute(ctx: PinRouteContext, item: MockRouter): Response | undefined {
  const base = `/routers/${item.router.id}/tls-fingerprint`
  if (!ctx.path.startsWith(base)) return
  const sub = ctx.path.slice(base.length)
  const cur = tlsPinOf(item, ctx.now)
  if (ctx.method === 'GET' && sub === '') {
    if (!ctx.can('devices.read')) return forbidden()
    return json(cur)
  }
  if (ctx.method === 'POST' && sub === '/capture') {
    if (!ctx.can('devices.update')) return forbidden()
    if (item.router.onboarding_state === 'pending_configuration') {
      return problem(502, 'ROUTER_UNREACHABLE', 'El router aún no tiene túnel con Horus')
    }
    const observed = cur.status === 'changed' ? MOCK_FP_NEW : (cur.pinned_sha256 ?? MOCK_FP_PINNED)
    const next: RouterTlsFingerprint =
      cur.pinned_sha256 === observed
        ? { ...cur, observed_sha256: null, observed_at: null }
        : { ...cur, observed_sha256: observed, observed_at: ctx.now.toISOString() }
    next.status = !next.pinned_sha256
      ? 'pending_confirmation'
      : next.observed_sha256
        ? 'changed'
        : 'confirmed'
    mockState.tlsPins.set(item.router.id, next)
    return json(next)
  }
  if (ctx.method === 'POST' && sub === '/confirm') {
    if (!ctx.can('devices.credentials.write')) return forbidden()
    if (ctx.reauthRequired) {
      return problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para fijar la huella')
    }
    const raw = String(ctx.body.fingerprint_sha256 ?? '')
    const hex = raw.replaceAll(':', '').trim().toUpperCase()
    if (!/^[0-9A-F]{64}$/.test(hex)) {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Revisa los campos marcados',
        {},
        {
          errors: [
            {
              field: 'fingerprint_sha256',
              code: 'INVALID_FORMAT',
              message: "SHA-256 en hexadecimal (64 dígitos, con o sin ':').",
            },
          ],
        },
      )
    }
    const fp = hex.match(/../g)!.join(':')
    if (!cur.observed_sha256 || cur.observed_sha256 !== fp) {
      return problem(
        409,
        'ROUTER_TLS_FINGERPRINT_MISMATCH',
        'La huella no coincide con la que presenta el router',
      )
    }
    const next: RouterTlsFingerprint = {
      ...cur,
      status: 'confirmed',
      pinned_sha256: fp,
      observed_sha256: null,
      observed_at: null,
      confirmed_at: ctx.now.toISOString(),
      confirmed_by: ctx.userId,
    }
    mockState.tlsPins.set(item.router.id, next)
    return json(next)
  }
  return undefined
}
