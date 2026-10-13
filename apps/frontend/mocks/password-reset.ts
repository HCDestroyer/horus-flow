import type { PasswordResetIssued } from '~~/types/api'
import { USERS, type MockUser } from './data'
import { json, noContent, problem, randomHex } from './http'
import { readFlag } from './members'
import { mockState } from './state'

/**
 * Restablecimiento de contraseña simulado (auth.yaml `/auth/password/*`,
 * `POST /members/{user_id}/password-reset`, `POST /platform/users/{user_id}/password-reset`).
 *
 * Los tokens viven en memoria de la pestaña (`mockState.passwordResets`): un enlace emitido en
 * esta pestaña se puede abrir en ella misma. Para las pruebas e2e hay además un token fijo,
 * `RESET_DEMO_TOKEN`, que restablece la contraseña del operador NOC de demo y, como cualquier
 * otro, deja de valer al usarse (segundo uso → 422 `USED`).
 *
 * - `localStorage['horus.mock.smtp']`: `POST /auth/password/forgot` responde
 *   `email_delivery: true` salvo que valga `'0'` (la instalación "envía correo"); el
 *   `email_sent` de los enlaces emitidos por un administrador sigue la misma bandera que las
 *   invitaciones (`'1'`).
 */

/** Vida del enlace (30 min, api.md). */
export const PASSWORD_RESET_TTL = 30 * 60_000

/** Token fijo para pruebas: restablece la contraseña de `noc@fibranorte.example`. */
export const RESET_DEMO_TOKEN = 'reset-demo-token-0000000000000000'
const DEMO_USER_ID = '01926b3e-1111-7000-8000-000000000002'

function emailDelivery() {
  try {
    return globalThis.localStorage?.getItem('horus.mock.smtp') !== '0'
  } catch {
    return true
  }
}

function tokenError(code: 'INVALID_TOKEN' | 'EXPIRED' | 'USED') {
  const message =
    code === 'USED'
      ? 'Este enlace ya se usó'
      : code === 'EXPIRED'
        ? 'Este enlace ha caducado'
        : 'El enlace no es válido'
  return problem(
    422,
    'VALIDATION_FAILED',
    'El enlace de restablecimiento no sirve',
    {},
    { errors: [{ field: 'token', code, message }] },
  )
}

/**
 * Tokens consumidos, en el storage de la sesión simulada: el token fijo de pruebas debe seguir
 * "usado" aunque la página se recargue (la memoria de la pestaña se pierde al recargar).
 */
const USED_KEY = 'horus.mock.usedPasswordResets'
function usedTokens(): Set<string> {
  try {
    return new Set(JSON.parse(globalThis.sessionStorage?.getItem(USED_KEY) ?? '[]') as string[])
  } catch {
    return new Set()
  }
}
function markUsed(token: string) {
  const used = usedTokens()
  used.add(token)
  try {
    globalThis.sessionStorage?.setItem(USED_KEY, JSON.stringify([...used]))
  } catch {
    // sin storage: solo en memoria
  }
}

function records() {
  const map = mockState.passwordResets
  if (!map.has(RESET_DEMO_TOKEN)) {
    const demo = USERS.find((u) => u.me.id === DEMO_USER_ID)
    if (demo) {
      map.set(RESET_DEMO_TOKEN, {
        userId: demo.me.id,
        email: demo.me.email,
        displayName: demo.me.display_name,
        expires: Number.MAX_SAFE_INTEGER,
        used: false,
      })
    }
  }
  return map
}

/** Emite un enlace para una cuenta (o para un miembro sintético sin cuenta de demo). */
export function issuePasswordReset(
  target: { user_id: string; email: string; display_name: string },
  now: Date,
): PasswordResetIssued {
  const expires = now.getTime() + PASSWORD_RESET_TTL
  const token = `prt_${randomHex(40)}`
  // Un enlace nuevo invalida los anteriores de la misma cuenta.
  for (const [key, rec] of records()) {
    if (rec.userId === target.user_id && key !== RESET_DEMO_TOKEN) rec.used = true
  }
  records().set(token, {
    userId: target.user_id,
    email: target.email,
    displayName: target.display_name,
    expires,
    used: false,
  })
  const origin = globalThis.location?.origin ?? 'https://horus.example'
  return {
    user_id: target.user_id,
    reset_url: `${origin}/reset-password#token=${token}`,
    expires_at: new Date(expires).toISOString(),
    email_sent: readFlag('horus.mock.smtp'),
  }
}

function lookup(token: unknown, now: Date) {
  const raw = String(token ?? '')
  const rec = raw.length >= 32 ? records().get(raw) : undefined
  if (!rec) return tokenError('INVALID_TOKEN')
  if (rec.used || usedTokens().has(raw)) return tokenError('USED')
  if (rec.expires <= now.getTime()) return tokenError('EXPIRED')
  return rec
}

/** `POST /auth/password/forgot`, `/reset/inspect` y `/reset` (sin token de sesión). */
export function passwordResetRoute(
  route: string,
  body: Record<string, unknown>,
  now: Date,
  saveAccount: (user: MockUser) => void,
): Response | undefined {
  if (route === 'POST /auth/password/forgot') {
    const email = String(body.email ?? '')
      .trim()
      .toLowerCase()
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Revisa los campos marcados',
        {},
        { errors: [{ field: 'email', code: 'INVALID', message: 'Escribe un correo válido' }] },
      )
    }
    const user = USERS.find((u) => u.username === email)
    if (user && emailDelivery()) {
      issuePasswordReset(
        { user_id: user.me.id, email: user.me.email, display_name: user.me.display_name },
        now,
      )
    }
    // Misma respuesta exista o no la cuenta (security.md §4.5).
    return json({ email_delivery: emailDelivery() }, 202, { 'Cache-Control': 'no-store' })
  }
  if (route === 'POST /auth/password/reset/inspect') {
    const rec = lookup(body.token, now)
    if (rec instanceof Response) return rec
    return json(
      {
        email: rec.email,
        display_name: rec.displayName,
        expires_at: new Date(
          Math.min(rec.expires, now.getTime() + PASSWORD_RESET_TTL),
        ).toISOString(),
      },
      200,
      { 'Cache-Control': 'no-store' },
    )
  }
  if (route === 'POST /auth/password/reset') {
    const rec = lookup(body.token, now)
    if (rec instanceof Response) return rec
    const password = typeof body.new_password === 'string' ? body.new_password : ''
    if (password.length < 12) {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Revisa los campos marcados',
        {},
        {
          errors: [{ field: 'new_password', code: 'TOO_SHORT', message: 'Mínimo 12 caracteres' }],
        },
      )
    }
    rec.used = true
    markUsed(String(body.token))
    const user = USERS.find((u) => u.me.id === rec.userId)
    if (user) {
      user.password = password
      user.me.must_change_password = false
      saveAccount(user)
    }
    return noContent()
  }
}
