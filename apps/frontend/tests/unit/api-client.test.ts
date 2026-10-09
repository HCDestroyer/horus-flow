import { describe, expect, it, vi } from 'vitest'
import {
  ApiError,
  apiPath,
  createAuthFetch,
  createHorusClient,
  NetworkError,
  unwrap,
  WITH_CSRF,
  type ApiFetch,
} from '~/utils/api-client'
import { withCrossTabLock } from '~/utils/cross-tab'
import { describeError } from '~/utils/errors'
import { tokenForPath } from '~/composables/useAuth'

const BASE = 'http://horus.test/api/v1'

function json(body: unknown, status = 200, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

const unauthorized = () =>
  json({ type: 'about:blank', status: 401, code: 'TOKEN_EXPIRED', title: 'La sesión caducó' }, 401)

function client(fetch: ApiFetch, opts: Partial<Parameters<typeof createAuthFetch>[0]> = {}) {
  const transport = vi.fn(fetch)
  const api = createHorusClient(
    BASE,
    createAuthFetch({ fetch: transport, basePath: '/api/v1', getAccessToken: () => null, ...opts }),
  )
  return { api, transport }
}

describe('cliente generado (openapi-fetch + createAuthFetch)', () => {
  it('envía el access token en memoria como Bearer y devuelve el cuerpo tipado', async () => {
    const { api, transport } = client(async () => json({ id: 'u1' }), {
      getAccessToken: () => 'abc',
    })
    const me = await unwrap(api.GET('/me'))
    expect(me.id).toBe('u1')
    const sent = transport.mock.calls[0]![0]
    expect(sent.headers.get('Authorization')).toBe('Bearer abc')
    expect(new URL(sent.url).pathname).toBe('/api/v1/me')
  })

  it('añade X-Requested-With solo en las rutas que usan la cookie de refresh', async () => {
    const { api, transport } = client(async () =>
      json({ access_token: 'x', token_type: 'Bearer', expires_at: '', scope: 'session' }),
    )
    await api.POST('/auth/refresh', WITH_CSRF)
    await api.POST('/auth/login', { body: { username: 'a', password: 'b' } })
    expect(transport.mock.calls[0]![0].headers.get('X-Requested-With')).toBe('horus')
    expect(transport.mock.calls[1]![0].headers.get('X-Requested-With')).toBeNull()
  })

  it('ante 401 refresca una sola vez (aunque haya peticiones concurrentes) y reintenta con el cuerpo', async () => {
    let token = 'viejo'
    const bodies: string[] = []
    const { api } = client(
      async (req) => {
        if (req.headers.get('Authorization') !== 'Bearer nuevo') return unauthorized()
        bodies.push(await req.text())
        return json({ path: new URL(req.url).pathname })
      },
      {
        getAccessToken: () => token,
        refresh: vi.fn(async () => {
          await new Promise((r) => setTimeout(r, 5))
          token = 'nuevo'
          return true
        }),
      },
    )
    const [a, b] = await Promise.all([
      unwrap(api.GET('/me')),
      unwrap(
        api.POST('/auth/token', { body: { tenant_id: '01926b3e-0000-7000-8000-000000000001' } }),
      ),
    ])
    expect(a).toEqual({ path: '/api/v1/me' })
    expect(b).toEqual({ path: '/api/v1/auth/token' })
    // El reintento lleva el cuerpo original (el Request se clona antes del primer envío).
    expect(bodies).toContain(JSON.stringify({ tenant_id: '01926b3e-0000-7000-8000-000000000001' }))
  })

  it('cuenta un único refresh para peticiones concurrentes', async () => {
    let token = 'viejo'
    const refresh = vi.fn(async () => {
      await new Promise((r) => setTimeout(r, 5))
      token = 'nuevo'
      return true
    })
    const { api } = client(
      async (req) =>
        req.headers.get('Authorization') === 'Bearer nuevo' ? json({}) : unauthorized(),
      { getAccessToken: () => token, refresh },
    )
    await Promise.all([api.GET('/me'), api.GET('/system/status'), api.GET('/widget-types')])
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('si el refresh falla, avisa de sesión caducada y lanza el error tipado', async () => {
    const onSessionExpired = vi.fn()
    const { api } = client(async () => unauthorized(), {
      getAccessToken: () => 'x',
      refresh: async () => false,
      onSessionExpired,
    })
    await expect(unwrap(api.GET('/me'))).rejects.toMatchObject({
      status: 401,
      code: 'TOKEN_EXPIRED',
    })
    expect(onSessionExpired).toHaveBeenCalledOnce()
  })

  it('un 401 del login no dispara refresh (son credenciales, no un token caducado)', async () => {
    const refresh = vi.fn(async () => true)
    const { api } = client(
      async () => json({ status: 401, code: 'INVALID_CREDENTIALS', title: 'x' }, 401),
      { refresh },
    )
    await expect(
      unwrap(api.POST('/auth/login', { body: { username: 'a', password: 'b' } })),
    ).rejects.toBeInstanceOf(ApiError)
    expect(refresh).not.toHaveBeenCalled()
  })

  it('lee Retry-After en 429 y traduce fallos de red a NetworkError', async () => {
    const limited = client(async () =>
      json({ status: 429, code: 'RATE_LIMITED', title: 'x' }, 429, { 'Retry-After': '42' }),
    )
    const error = await unwrap(limited.api.GET('/me')).catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).retryAfter).toBe(42)

    const offline = client(async () => {
      throw new TypeError('Failed to fetch')
    })
    await expect(offline.api.GET('/me')).rejects.toBeInstanceOf(NetworkError)
  })

  it('normaliza respuestas de error que no son Problem Details', async () => {
    const { api } = client(async () => new Response('<html>Bad gateway', { status: 502 }))
    await expect(unwrap(api.GET('/me'))).rejects.toMatchObject({ status: 502, code: 'INTERNAL' })
  })

  it('apiPath quita origen y prefijo', () => {
    expect(apiPath('http://h/api/v1/me?x=1', '/api/v1')).toBe('/me')
    expect(apiPath('http://h/otra', '/api/v1')).toBe('/otra')
  })
})

describe('tokens por ámbito (api.md §0.2)', () => {
  const scoped = { token: 'isp' }
  it('/me y /auth/* usan el token de sesión; el resto, el del ISP', () => {
    expect(tokenForPath('/me', 'ses', scoped)).toBe('ses')
    expect(tokenForPath('/auth/token', 'ses', scoped)).toBe('ses')
    expect(tokenForPath('/dashboards', 'ses', scoped)).toBe('isp')
    expect(tokenForPath('/dashboards', 'ses', null)).toBe('ses')
  })
})

describe('refresh entre pestañas', () => {
  it('serializa con Web Locks: dos "pestañas" nunca renuevan a la vez', async () => {
    // Simulación mínima de navigator.locks: una cola por nombre.
    const queues = new Map<string, Promise<unknown>>()
    const locks = {
      request<T>(name: string, fn: () => Promise<T>) {
        const prev = queues.get(name) ?? Promise.resolve()
        const next = prev.then(fn, fn)
        queues.set(
          name,
          next.catch(() => undefined),
        )
        return next
      },
    }
    let inFlight = 0
    let maxInFlight = 0
    const renew = async () => {
      inFlight++
      maxInFlight = Math.max(maxInFlight, inFlight)
      await new Promise((r) => setTimeout(r, 5))
      inFlight--
      return true
    }
    await Promise.all([
      withCrossTabLock('horus.auth.refresh', renew, locks),
      withCrossTabLock('horus.auth.refresh', renew, locks),
      withCrossTabLock('horus.auth.refresh', renew, locks),
    ])
    expect(maxInFlight).toBe(1)
  })

  it('sin Web Locks (navegador antiguo) ejecuta directamente', async () => {
    await expect(withCrossTabLock('x', async () => 7, undefined)).resolves.toBe(7)
  })
})

describe('describeError', () => {
  it('mapea estados a copia y conserva el detalle técnico', () => {
    const err = new ApiError({
      type: 'about:blank',
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
      describeError(
        new ApiError({ type: 'about:blank', status: 403, code: 'PERMISSION_DENIED', title: 'x' }),
      ),
    ).toMatchObject({ messageKey: 'errors.forbidden.title', retryable: false })
  })
})
