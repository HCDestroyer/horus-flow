import type { ProblemDetails } from '~~/shared/api/types'

/**
 * Cliente HTTP mínimo de la SPA (conventions.md §3.5).
 *
 * Independiente del transporte: en I0-14 se usa el transporte simulado (mocks/) o `fetch`;
 * en I0-15 se sustituye por `openapi-fetch` con tipos generados, conservando esta interfaz
 * (token en memoria, refresh único ante 401, errores tipados por `code`).
 */

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export interface ApiRequest {
  method: HttpMethod
  path: string
  body?: unknown
  headers: Record<string, string>
  signal?: AbortSignal
}

export interface ApiResponse {
  status: number
  headers: Record<string, string>
  data: unknown
}

export type ApiTransport = (request: ApiRequest) => Promise<ApiResponse>

/** Error de la API con el `code` del contrato (docs/api.md §1.4). */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly problem: ProblemDetails
  /** Segundos de espera en 429 (cabecera `Retry-After`). */
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

export interface ApiClientOptions {
  transport: ApiTransport
  /** Access token en memoria (nunca persistido, security.md §5.1). */
  getAccessToken: () => string | null
  /**
   * Renueva el access token con la cookie de refresh. Debe devolver `true` si lo consiguió.
   * El cliente garantiza una sola llamada concurrente.
   */
  refresh?: () => Promise<boolean>
  /** Se llama cuando el refresh falla: la sesión terminó. */
  onSessionExpired?: () => void
}

export interface RequestOptions {
  body?: unknown
  signal?: AbortSignal
  headers?: Record<string, string>
}

/** Rutas que usan la cookie de refresh y exigen la cabecera anti-CSRF (security.md §5.1). */
const COOKIE_AUTH_PATHS = ['/auth/refresh', '/auth/logout']
/** Rutas en las que un 401 no dispara refresh (credenciales, no token caducado). */
const NO_REFRESH_PATHS = ['/auth/login', '/auth/mfa/verify', '/auth/refresh', '/auth/logout']

function isProblem(value: unknown): value is ProblemDetails {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as ProblemDetails).code === 'string' &&
    typeof (value as ProblemDetails).status === 'number'
  )
}

function toProblem(response: ApiResponse): ProblemDetails {
  if (isProblem(response.data)) return response.data
  return {
    title: 'Respuesta inesperada del servidor',
    status: response.status,
    code: response.status >= 500 ? 'INTERNAL' : 'UNEXPECTED_RESPONSE',
  }
}

export function createApiClient(options: ApiClientOptions) {
  let refreshing: Promise<boolean> | null = null

  function refreshOnce(): Promise<boolean> {
    if (!options.refresh) return Promise.resolve(false)
    refreshing ??= options.refresh().finally(() => {
      refreshing = null
    })
    return refreshing
  }

  async function send(method: HttpMethod, path: string, opts: RequestOptions) {
    const headers: Record<string, string> = { Accept: 'application/json', ...opts.headers }
    if (opts.body !== undefined) headers['Content-Type'] = 'application/json'
    if (COOKIE_AUTH_PATHS.includes(path)) headers['X-Requested-With'] = 'horus'
    const token = options.getAccessToken()
    if (token) headers.Authorization = `Bearer ${token}`
    try {
      return await options.transport({
        method,
        path,
        body: opts.body,
        headers,
        signal: opts.signal,
      })
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') throw error
      throw new NetworkError(error)
    }
  }

  async function request<T>(method: HttpMethod, path: string, opts: RequestOptions = {}) {
    let response = await send(method, path, opts)

    if (response.status === 401 && !NO_REFRESH_PATHS.includes(path)) {
      if (await refreshOnce()) {
        response = await send(method, path, opts)
      }
      if (response.status === 401) options.onSessionExpired?.()
    }

    if (response.status >= 400) {
      const retryAfter = Number(response.headers['retry-after'])
      throw new ApiError(toProblem(response), Number.isFinite(retryAfter) ? retryAfter : undefined)
    }
    return response.data as T
  }

  return {
    request,
    get: <T>(path: string, opts?: RequestOptions) => request<T>('GET', path, opts),
    post: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
      request<T>('POST', path, { ...opts, body }),
  }
}

export type ApiClient = ReturnType<typeof createApiClient>

/** Transporte real sobre `fetch` contra el gateway (`baseURL` relativa, D14). */
export function createFetchTransport(baseURL: string): ApiTransport {
  return async ({ method, path, body, headers, signal }) => {
    const res = await fetch(`${baseURL}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'include',
      signal,
    })
    const text = await res.text()
    let data: unknown = null
    if (text) {
      try {
        data = JSON.parse(text)
      } catch {
        data = text
      }
    }
    const outHeaders: Record<string, string> = {}
    res.headers.forEach((value, key) => {
      outHeaders[key.toLowerCase()] = value
    })
    return { status: res.status, headers: outHeaders, data }
  }
}
