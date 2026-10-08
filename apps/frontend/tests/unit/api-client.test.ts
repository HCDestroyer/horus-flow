import { describe, expect, it, vi } from 'vitest'
import {
  ApiError,
  createApiClient,
  NetworkError,
  type ApiRequest,
  type ApiResponse,
} from '~/utils/api-client'
import { describeError } from '~/utils/errors'

function transportFrom(handler: (req: ApiRequest) => ApiResponse | Promise<ApiResponse>) {
  return vi.fn(async (req: ApiRequest) => handler(req))
}

const unauthorized: ApiResponse = {
  status: 401,
  headers: {},
  data: { status: 401, code: 'TOKEN_EXPIRED', title: 'La sesión caducó' },
}

describe('createApiClient', () => {
  it('envía el access token en memoria como Bearer', async () => {
    const transport = transportFrom(() => ({ status: 200, headers: {}, data: { ok: true } }))
    const api = createApiClient({ transport, getAccessToken: () => 'abc' })
    await expect(api.get('/me')).resolves.toEqual({ ok: true })
    expect(transport.mock.calls[0]![0].headers.Authorization).toBe('Bearer abc')
  })

  it('añade X-Requested-With solo en las rutas que usan la cookie de refresh', async () => {
    const transport = transportFrom(() => ({ status: 200, headers: {}, data: {} }))
    const api = createApiClient({ transport, getAccessToken: () => null })
    await api.post('/auth/refresh')
    await api.post('/auth/login', { email: 'a', password: 'b' })
    expect(transport.mock.calls[0]![0].headers['X-Requested-With']).toBe('horus')
    expect(transport.mock.calls[1]![0].headers['X-Requested-With']).toBeUndefined()
  })

  it('ante 401 refresca una sola vez (aunque haya peticiones concurrentes) y reintenta', async () => {
    let token = 'viejo'
    const transport = transportFrom((req) =>
      req.headers.Authorization === 'Bearer nuevo'
        ? { status: 200, headers: {}, data: { path: req.path } }
        : unauthorized,
    )
    const refresh = vi.fn(async () => {
      await new Promise((r) => setTimeout(r, 5))
      token = 'nuevo'
      return true
    })
    const api = createApiClient({ transport, getAccessToken: () => token, refresh })
    const [a, b] = await Promise.all([api.get('/me'), api.get('/system/status')])
    expect(a).toEqual({ path: '/me' })
    expect(b).toEqual({ path: '/system/status' })
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('si el refresh falla, avisa de sesión caducada y lanza el error tipado', async () => {
    const onSessionExpired = vi.fn()
    const api = createApiClient({
      transport: transportFrom(() => unauthorized),
      getAccessToken: () => 'x',
      refresh: async () => false,
      onSessionExpired,
    })
    await expect(api.get('/me')).rejects.toMatchObject({ status: 401, code: 'TOKEN_EXPIRED' })
    expect(onSessionExpired).toHaveBeenCalledOnce()
  })

  it('un 401 del login no dispara refresh (son credenciales, no un token caducado)', async () => {
    const refresh = vi.fn(async () => true)
    const api = createApiClient({
      transport: transportFrom(() => ({
        status: 401,
        headers: {},
        data: { status: 401, code: 'INVALID_CREDENTIALS', title: 'x' },
      })),
      getAccessToken: () => null,
      refresh,
    })
    await expect(api.post('/auth/login', {})).rejects.toBeInstanceOf(ApiError)
    expect(refresh).not.toHaveBeenCalled()
  })

  it('lee Retry-After en 429 y traduce fallos de red a NetworkError', async () => {
    const limited = createApiClient({
      transport: transportFrom(() => ({
        status: 429,
        headers: { 'retry-after': '42' },
        data: { status: 429, code: 'RATE_LIMITED', title: 'x' },
      })),
      getAccessToken: () => null,
    })
    const error = await limited.get('/x').catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).retryAfter).toBe(42)

    const offline = createApiClient({
      transport: async () => {
        throw new TypeError('Failed to fetch')
      },
      getAccessToken: () => null,
    })
    await expect(offline.get('/x')).rejects.toBeInstanceOf(NetworkError)
  })

  it('normaliza respuestas de error que no son Problem Details', async () => {
    const api = createApiClient({
      transport: transportFrom(() => ({ status: 502, headers: {}, data: '<html>Bad gateway' })),
      getAccessToken: () => null,
    })
    await expect(api.get('/x')).rejects.toMatchObject({ status: 502, code: 'INTERNAL' })
  })
})

describe('describeError', () => {
  it('mapea estados a copia y conserva el detalle técnico', () => {
    const err = new ApiError({
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
      title: 'x',
      trace_id: 't1',
    })
    expect(describeError(err)).toMatchObject({
      messageKey: 'errors.service',
      code: 'SERVICE_UNAVAILABLE',
      traceId: 't1',
      retryable: true,
    })
    expect(describeError(new NetworkError())).toMatchObject({ messageKey: 'errors.network' })
    expect(
      describeError(new ApiError({ status: 403, code: 'PERMISSION_DENIED', title: 'x' })),
    ).toMatchObject({ messageKey: 'errors.forbidden.title', retryable: false })
  })
})
