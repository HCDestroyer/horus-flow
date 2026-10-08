import type { ApiRequest, ApiResponse, ApiTransport } from '~/utils/api-client'
import type {
  AccessTokenResponse,
  LoginRequest,
  MfaVerifyRequest,
  ProblemDetails,
  SystemStatus,
} from '~~/shared/api/types'
import { MOCK_TOTP_CODE, USERS, type MockUser } from './data'

/**
 * API simulada en memoria para desarrollo, tests y demos sin backend (I0-14).
 *
 * Reproduce el contrato de docs/api.md §2.1 y §2.7 lo justo para el login y el layout:
 * mismos códigos de error, respuesta idéntica exista o no el usuario, 2FA con `mfa_token`
 * y rate limit. En I0-15 se sustituye por handlers generados del OpenAPI.
 *
 * La cookie HttpOnly de refresh no se puede simular desde JS: la "sesión" del mock vive en
 * `sessionStorage` SOLO en este transporte, para que recargar la página no cierre sesión.
 * El código de la app nunca persiste tokens.
 */

export type MockScenario = 'normal' | 'degraded' | 'system-error'

export interface MockOptions {
  /** Latencia simulada en ms (para ver los estados de carga). */
  latencyMs?: number
  /** Escenario; por defecto se lee de `localStorage['horus.mock.scenario']`. */
  scenario?: () => MockScenario
  storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> | null
  now?: () => Date
}

const SESSION_KEY = 'horus.mock.session'
const SCENARIO_KEY = 'horus.mock.scenario'
const MAX_FAILURES = 5
const LOCK_SECONDS = 60

function problem(
  status: number,
  code: string,
  title: string,
  extra: Partial<ProblemDetails> = {},
): ApiResponse & { headers: Record<string, string> } {
  return {
    status,
    headers: {},
    data: {
      type: `https://docs.horus-flow.local/errors/${code.toLowerCase().replaceAll('_', '-')}`,
      title,
      status,
      code,
      trace_id: randomHex(32),
      ...extra,
    } satisfies ProblemDetails,
  }
}

function ok(data: unknown, status = 200): ApiResponse {
  return { status, headers: {}, data }
}

function randomHex(length: number) {
  const bytes = new Uint8Array(length / 2)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

function safeStorage(): MockOptions['storage'] {
  try {
    return typeof window === 'undefined' ? null : window.sessionStorage
  } catch {
    return null
  }
}

function readScenario(): MockScenario {
  try {
    const value = window.localStorage.getItem(SCENARIO_KEY)
    if (value === 'degraded' || value === 'system-error') return value
  } catch {
    // sin storage: escenario normal
  }
  return 'normal'
}

export function createMockTransport(options: MockOptions = {}): ApiTransport {
  const latency = options.latencyMs ?? 250
  const scenario = options.scenario ?? readScenario
  const storage = options.storage === undefined ? safeStorage() : options.storage
  const now = options.now ?? (() => new Date())

  const accessTokens = new Map<string, string>() // token → userId
  const mfaTokens = new Map<string, { userId: string; expires: number }>()
  const failures = new Map<string, { count: number; lockedUntil: number }>()

  const findUser = (id: string) => USERS.find((u) => u.me.id === id)

  function issueToken(user: MockUser): ApiResponse {
    const token = `mock.${randomHex(24)}`
    accessTokens.set(token, user.me.id)
    storage?.setItem(SESSION_KEY, user.me.id)
    const body: AccessTokenResponse = {
      access_token: token,
      expires_at: new Date(now().getTime() + 10 * 60_000).toISOString(),
    }
    return ok(body)
  }

  function currentUser(req: ApiRequest) {
    const header = req.headers.Authorization ?? ''
    const token = header.startsWith('Bearer ') ? header.slice(7) : ''
    const userId = accessTokens.get(token)
    return userId ? findUser(userId) : undefined
  }

  function invalidCredentials() {
    // Mismo mensaje exista o no la cuenta (security.md §4.5, criterio 1 de I0-14).
    return problem(401, 'INVALID_CREDENTIALS', 'Correo o contraseña incorrectos')
  }

  function login(body: Partial<LoginRequest>): ApiResponse {
    const email = String(body.email ?? '')
      .trim()
      .toLowerCase()
    const entry = failures.get(email)
    const t = now().getTime()
    if (entry && entry.lockedUntil > t) {
      const wait = Math.ceil((entry.lockedUntil - t) / 1000)
      const res = problem(429, 'RATE_LIMITED', 'Demasiados intentos')
      res.headers['retry-after'] = String(wait)
      return res
    }
    const user = USERS.find((u) => u.me.email === email)
    if (!user || user.password !== body.password) {
      const count = (entry?.count ?? 0) + 1
      failures.set(email, {
        count: count >= MAX_FAILURES ? 0 : count,
        lockedUntil: count >= MAX_FAILURES ? t + LOCK_SECONDS * 1000 : 0,
      })
      return invalidCredentials()
    }
    failures.delete(email)
    if (user.mfa) {
      const mfaToken = `mfa.${randomHex(16)}`
      mfaTokens.set(mfaToken, { userId: user.me.id, expires: t + 5 * 60_000 })
      return ok({ mfa_required: true, mfa_token: mfaToken })
    }
    return issueToken(user)
  }

  function verifyMfa(body: Partial<MfaVerifyRequest>): ApiResponse {
    const pending = mfaTokens.get(String(body.mfa_token ?? ''))
    if (!pending || pending.expires < now().getTime()) {
      return problem(401, 'UNAUTHENTICATED', 'La verificación caducó; vuelve a iniciar sesión')
    }
    if (String(body.code ?? '').replace(/\s/g, '') !== MOCK_TOTP_CODE) {
      return problem(401, 'INVALID_CREDENTIALS', 'Código incorrecto')
    }
    mfaTokens.delete(String(body.mfa_token))
    const user = findUser(pending.userId)
    return user ? issueToken(user) : invalidCredentials()
  }

  function systemStatus(): ApiResponse {
    const mode = scenario()
    if (mode === 'system-error') {
      return problem(503, 'SERVICE_UNAVAILABLE', 'Servicio no disponible')
    }
    const degraded = mode === 'degraded'
    const body: SystemStatus = {
      status: degraded ? 'degraded' : 'ok',
      checked_at: now().toISOString(),
      capabilities: {
        auth: 'ok',
        inventory: 'ok',
        wireguard: 'ok',
        monitoring: degraded ? 'stale' : 'ok',
        realtime: 'ok',
        analytics: degraded ? 'unavailable' : 'ok',
        reports: degraded ? 'unavailable' : 'ok',
      },
    }
    return ok(body)
  }

  async function handle(req: ApiRequest): Promise<ApiResponse> {
    const route = `${req.method} ${req.path.split('?')[0]}`
    const body = (req.body ?? {}) as Record<string, unknown>

    switch (route) {
      case 'POST /auth/login':
        return login(body)
      case 'POST /auth/mfa/verify':
        return verifyMfa(body)
      case 'POST /auth/refresh': {
        if (req.headers['X-Requested-With'] !== 'horus') {
          return problem(403, 'ORIGIN_NOT_ALLOWED', 'Origen no permitido')
        }
        const userId = storage?.getItem(SESSION_KEY)
        const user = userId ? findUser(userId) : undefined
        return user ? issueToken(user) : problem(401, 'UNAUTHENTICATED', 'Sesión no iniciada')
      }
      case 'POST /auth/logout':
        storage?.removeItem(SESSION_KEY)
        accessTokens.clear()
        return { status: 204, headers: {}, data: null }
    }

    const user = currentUser(req)
    if (!user) return problem(401, 'TOKEN_EXPIRED', 'La sesión caducó')

    switch (route) {
      case 'GET /me':
        return ok(user.me)
      case 'GET /system/status':
        return systemStatus()
      default:
        return problem(404, 'NOT_FOUND', 'No encontrado')
    }
  }

  return async (req) => {
    if (latency > 0) await new Promise((resolve) => setTimeout(resolve, latency))
    if (req.signal?.aborted) throw new DOMException('Aborted', 'AbortError')
    return handle(req)
  }
}
