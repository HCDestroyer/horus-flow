import createClient, { type Client } from 'openapi-fetch'
import type { ProblemDetails, paths } from '~~/types/api'

/**
 * Cliente HTTP de la SPA (I0-15, conventions.md §3.5, frontend.md §16).
 *
 * - `openapi-fetch` tipado con los tipos generados del OpenAPI (C5): rutas, parámetros,
 *   cuerpos y respuestas se comprueban en `pnpm typecheck` contra el contrato.
 * - El transporte es un `fetch` (el real o el de la API simulada de `mocks/`), envuelto por
 *   `createAuthFetch`: Bearer en memoria, cabecera anti-CSRF en las rutas con cookie,
 *   refresh único ante 401 y reintento transparente una sola vez.
 */

export type ApiFetch = (request: Request) => Promise<Response>
export type HorusClient = Client<paths>

/** Error de la API con el `code` del contrato (docs/api.md §1.4, RFC 9457). */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly problem: ProblemDetails
  /** Segundos de espera en 429/503 (cabecera `Retry-After`). */
  readonly retryAfter?: number

  constructor(problem: ProblemDetails, retryAfter?: number) {
    super(problem.title)
    this.name = 'ApiError'
    this.status = problem.status
    this.code = problem.code
    this.problem = problem
    this.retryAfter = retryAfter
  }
}

/** Fallo de red: el gateway no responde (frontend.md §9.4, "No se puede conectar con Horus Flow"). */
export class NetworkError extends Error {
  constructor(cause?: unknown) {
    super('NETWORK_UNREACHABLE', { cause })
    this.name = 'NetworkError'
  }
}

/**
 * Cabecera anti-CSRF de `/auth/refresh` y `/auth/logout` (parámetro `XRequestedWith` del
 * contrato): `$api.POST('/auth/refresh', WITH_CSRF)`. `createAuthFetch` la añade igualmente.
 */
export const WITH_CSRF = { params: { header: { 'X-Requested-With': 'horus' as const } } }

/** Rutas que usan la cookie de refresh y exigen la cabecera anti-CSRF (security.md §5.1). */
export const COOKIE_AUTH_PATHS = ['/auth/refresh', '/auth/logout', '/kiosk/enroll', '/kiosk/token']
/** Rutas en las que un 401 no dispara refresh (credenciales, no un token caducado). */
const NO_REFRESH_PATHS = [
  '/auth/login',
  '/auth/mfa/verify',
  '/auth/refresh',
  '/auth/logout',
  // El kiosco renueva su propio token con la credencial de dispositivo (useKiosk).
  '/kiosk/enroll',
  '/kiosk/token',
  '/kiosk/config',
]

export interface AuthFetchOptions {
  /** Transporte: `globalThis.fetch` o el de la API simulada. */
  fetch: ApiFetch
  /** Prefijo de la API dentro de la URL (`/api/v1`). */
  basePath: string
  /** Access token en memoria para una ruta (sesión, tenant o plataforma). */
  getAccessToken: (path: string) => string | null
  /**
   * Renueva los tokens con la cookie de refresh. Debe devolver `true` si lo consiguió.
   * `createAuthFetch` garantiza una sola llamada concurrente por pestaña.
   */
  refresh?: () => Promise<boolean>
  /** Se llama cuando el refresh falla: la sesión terminó. */
  onSessionExpired?: (path: string) => void
}

/** Ruta de la API sin origen ni prefijo: `https://h/api/v1/me?x=1` → `/me`. */
export function apiPath(url: string, basePath: string) {
  const { pathname } = new URL(url, 'http://localhost')
  return pathname.startsWith(basePath) ? pathname.slice(basePath.length) || '/' : pathname
}

export function createAuthFetch(options: AuthFetchOptions): ApiFetch {
  let refreshing: Promise<boolean> | null = null

  function refreshOnce(): Promise<boolean> {
    if (!options.refresh) return Promise.resolve(false)
    refreshing ??= options.refresh().finally(() => {
      refreshing = null
    })
    return refreshing
  }

  async function send(request: Request, path: string) {
    if (COOKIE_AUTH_PATHS.includes(path)) request.headers.set('X-Requested-With', 'horus')
    const token = options.getAccessToken(path)
    if (token) request.headers.set('Authorization', `Bearer ${token}`)
    else request.headers.delete('Authorization')
    try {
      return await options.fetch(request)
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') throw error
      throw new NetworkError(error)
    }
  }

  return async (request) => {
    const path = apiPath(request.url, options.basePath)
    const canRefresh = !NO_REFRESH_PATHS.includes(path)
    // El cuerpo de un Request solo se lee una vez: se guarda una copia para el reintento.
    const retry = canRefresh ? request.clone() : null

    let response = await send(request, path)
    if (response.status === 401 && retry) {
      if (await refreshOnce()) response = await send(retry, path)
      if (response.status === 401) options.onSessionExpired?.(path)
    }
    return response
  }
}

export function createHorusClient(baseUrl: string, fetch: ApiFetch): HorusClient {
  return createClient<paths>({ baseUrl, fetch })
}

function isProblem(value: unknown): value is ProblemDetails {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as ProblemDetails).code === 'string' &&
    typeof (value as ProblemDetails).status === 'number'
  )
}

export function toProblem(status: number, body: unknown): ProblemDetails {
  if (isProblem(body)) return body
  return {
    type: 'about:blank',
    title: 'Respuesta inesperada del servidor',
    status,
    code: status >= 500 ? 'INTERNAL' : 'UNEXPECTED_RESPONSE',
  }
}

interface FetchResult<T> {
  data?: T
  error?: unknown
  response: Response
}

/**
 * Devuelve `data` o lanza `ApiError` con el Problem del contrato:
 * `const me = await unwrap($api.GET('/me'))`.
 */
export async function unwrap<T>(pending: Promise<FetchResult<T>>): Promise<NonNullable<T>> {
  const { data, error, response } = await pending
  if (!response.ok || error !== undefined) {
    const retryAfter = Number(response.headers.get('retry-after') ?? Number.NaN)
    throw new ApiError(
      toProblem(response.status, error),
      Number.isFinite(retryAfter) ? retryAfter : undefined,
    )
  }
  return data as NonNullable<T>
}

/** URL base absoluta: `new Request()` exige URL absoluta fuera del navegador. */
export function absoluteBase(apiBase: string, origin?: string) {
  if (/^https?:\/\//.test(apiBase)) return apiBase.replace(/\/$/, '')
  const base =
    origin ?? (typeof window === 'undefined' ? 'http://localhost' : window.location.origin)
  return `${base}${apiBase}`.replace(/\/$/, '')
}
