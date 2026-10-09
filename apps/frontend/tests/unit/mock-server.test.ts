import { describe, expect, it } from 'vitest'
import {
  createAuthFetch,
  createHorusClient,
  unwrap,
  WITH_CSRF,
  type ApiError,
} from '~/utils/api-client'
import { tokenForPath } from '~/composables/useAuth'
import { createMockFetch, TEMPLATES, type MockScenario } from '~~/mocks/server'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { WIDGET_CATALOG } from '~~/types/api/widget-catalog'

function memoryStorage() {
  const map = new Map<string, string>()
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  }
}

function setup(scenario: MockScenario = 'normal', now = () => new Date('2026-10-08T15:00:00Z')) {
  const tokens: { session: string | null; scoped: { token: string } | null } = {
    session: null,
    scoped: null,
  }
  const api = createHorusClient(
    'http://horus.test/api/v1',
    createAuthFetch({
      fetch: createMockFetch({
        latencyMs: 0,
        storage: memoryStorage(),
        scenario: () => scenario,
        now,
      }),
      basePath: '/api/v1',
      getAccessToken: (path) => tokenForPath(path, tokens.session, tokens.scoped),
    }),
  )
  async function loginNoc() {
    const res = await unwrap(
      api.POST('/auth/login', {
        body: { username: 'noc@fibranorte.example', password: MOCK_PASSWORD },
      }),
    )
    if ('mfa_required' in res) throw new Error('inesperado')
    tokens.session = res.access_token
  }
  async function useTenant(tenantId: string) {
    const res = await unwrap(api.POST('/auth/token', { body: { tenant_id: tenantId } }))
    tokens.scoped = { token: res.access_token }
    return res
  }
  return { api, tokens, loginNoc, useTenant }
}

describe('API simulada (contrato C5/C9)', () => {
  it('mismo error para usuario inexistente y contraseña incorrecta', async () => {
    const { api } = setup()
    const a = (await unwrap(
      api.POST('/auth/login', { body: { username: 'nadie@x.example', password: 'x' } }),
    ).catch((e: unknown) => e)) as ApiError
    const b = (await unwrap(
      api.POST('/auth/login', { body: { username: 'noc@fibranorte.example', password: 'x' } }),
    ).catch((e: unknown) => e)) as ApiError
    expect(a.code).toBe('INVALID_CREDENTIALS')
    expect(b.code).toBe(a.code)
    expect(b.problem.title).toBe(a.problem.title)
  })

  it('login con 2FA: mfa_token y el código correcto dan un token de sesión', async () => {
    const { api, tokens } = setup()
    const res = await unwrap(
      api.POST('/auth/login', {
        body: { username: 'ana.ruiz@fibranorte.example', password: MOCK_PASSWORD },
      }),
    )
    expect('mfa_required' in res && res.mfa_required).toBe(true)
    const mfaToken = 'mfa_token' in res ? res.mfa_token : ''
    await expect(
      unwrap(api.POST('/auth/mfa/verify', { body: { mfa_token: mfaToken, code: '000000' } })),
    ).rejects.toMatchObject({ code: 'INVALID_CREDENTIALS' })
    const ok = await unwrap(
      api.POST('/auth/mfa/verify', { body: { mfa_token: mfaToken, code: MOCK_TOTP_CODE } }),
    )
    expect(ok.scope).toBe('session')
    tokens.session = ok.access_token
    const me = await unwrap(api.GET('/me'))
    expect(me.memberships.map((m) => m.tenant_slug)).toEqual([
      'fibra-norte',
      'valle-conecta',
      'red-andina',
    ])
  })

  it('bloquea tras 5 intentos fallidos con 429 y Retry-After', async () => {
    const { api } = setup()
    const attempt = () =>
      unwrap(
        api.POST('/auth/login', {
          body: { username: 'noc@fibranorte.example', password: 'mal' },
        }),
      ).catch((e: ApiError) => e)
    for (let i = 0; i < 5; i++) await attempt()
    const error = await attempt()
    expect(error).toMatchObject({ status: 429, code: 'RATE_LIMITED' })
    expect((error as ApiError).retryAfter).toBeGreaterThan(0)
  })

  it('el refresh exige X-Requested-With (lo pone el cliente) y recupera la sesión', async () => {
    const { api, loginNoc } = setup()
    await expect(unwrap(api.POST('/auth/refresh', WITH_CSRF))).rejects.toMatchObject({
      code: 'UNAUTHENTICATED',
    })
    await loginNoc()
    const res = await unwrap(api.POST('/auth/refresh', WITH_CSRF))
    expect(res.access_token).toMatch(/^mock\.session\./)
  })

  it('token por ISP: no miembro → 404 TENANT_NOT_FOUND; rutas de negocio exigen token de ISP', async () => {
    const { api, loginNoc, useTenant } = setup()
    await loginNoc()
    await expect(
      unwrap(api.POST('/auth/token', { body: { tenant_id: TENANTS.valleConecta.tenant_id } })),
    ).rejects.toMatchObject({ status: 404, code: 'TENANT_NOT_FOUND' })
    await expect(unwrap(api.GET('/dashboards'))).rejects.toMatchObject({
      status: 403,
      code: 'TOKEN_SCOPE_INVALID',
    })
    const token = await useTenant(TENANTS.fibraNorte.tenant_id)
    expect(token).toMatchObject({ scope: 'tenant', tenant_id: TENANTS.fibraNorte.tenant_id })
    const list = await unwrap(api.GET('/dashboards'))
    expect(list.data.map((d) => d.name)).toEqual(['NOC del ISP', 'Seguridad'])
  })

  it('las rutas autenticadas sin token responden 401', async () => {
    const { api } = setup()
    await expect(unwrap(api.GET('/me'))).rejects.toMatchObject({ status: 401 })
  })

  it('sirve el catálogo de widgets del contrato', async () => {
    const { api, loginNoc } = setup()
    await loginNoc()
    const res = await unwrap(api.GET('/widget-types'))
    expect(res.data.map((t) => t.type)).toEqual(WIDGET_CATALOG.map((t) => t.type))
  })

  it('datos de widget con el sobre de C9; 403 WIDGET_TYPE_NOT_ALLOWED sin el permiso del tipo', async () => {
    const { api, loginNoc, useTenant } = setup()
    await loginNoc()
    await useTenant(TENANTS.fibraNorte.tenant_id)
    const noc = TEMPLATES[0]!
    const get = (wid: string) =>
      unwrap(
        api.GET('/dashboards/{dashboard_id}/widgets/{widget_id}/data', {
          params: { path: { dashboard_id: noc.id, widget_id: wid } },
        }),
      )
    const traffic = await get('w-traffic-24h')
    expect(traffic.meta).toMatchObject({
      widget_type: 'traffic_timeseries',
      data_endpoint_kind: 'series',
    })
    expect(traffic.data.kind).toBe('series')
    // Huecos como null, nunca cero.
    const points = traffic.data.kind === 'series' ? traffic.data.series[0]!.points : []
    expect(points.some(([, v]) => v === null)).toBe(true)
    // El rol noc no tiene customers.read: top_customers está bloqueado; el feed, enmascarado.
    await expect(get('w-top-customers')).rejects.toMatchObject({
      status: 403,
      code: 'WIDGET_TYPE_NOT_ALLOWED',
    })
    const feed = await get('w-findings-feed')
    expect(feed.meta.masked_personal_data).toBe(true)
    const rows = feed.data.kind === 'table' ? feed.data.rows : []
    expect(String(rows[0]!.customer_ip)).toMatch(/\.•••$/)
  })

  it('escenario degraded: la analítica responde 503 y el resto de widgets sigue', async () => {
    const { api, loginNoc, useTenant } = setup('degraded')
    await loginNoc()
    await useTenant(TENANTS.fibraNorte.tenant_id)
    const noc = TEMPLATES[0]!
    const get = (wid: string) =>
      unwrap(
        api.GET('/dashboards/{dashboard_id}/widgets/{widget_id}/data', {
          params: { path: { dashboard_id: noc.id, widget_id: wid } },
        }),
      )
    await expect(get('w-traffic-24h')).rejects.toMatchObject({
      status: 503,
      code: 'ANALYTICS_UNAVAILABLE',
    })
    await expect(get('w-exporters')).resolves.toMatchObject({
      meta: { widget_type: 'exporters_status' },
    })
  })

  it('escenario widget-error: falla una vez y el reintento funciona', async () => {
    const { api, loginNoc, useTenant } = setup('widget-error')
    await loginNoc()
    await useTenant(TENANTS.fibraNorte.tenant_id)
    const noc = TEMPLATES[0]!
    const get = () =>
      unwrap(
        api.GET('/dashboards/{dashboard_id}/widgets/{widget_id}/data', {
          params: { path: { dashboard_id: noc.id, widget_id: 'w-traffic-24h' } },
        }),
      )
    await expect(get()).rejects.toMatchObject({ status: 500 })
    await expect(get()).resolves.toBeTruthy()
  })

  it('un token caducado da 401 y el refresh lo renueva', async () => {
    let t = new Date('2026-10-08T15:00:00Z').getTime()
    const { api, tokens, loginNoc } = setup('normal', () => new Date(t))
    await loginNoc()
    t += 11 * 60_000
    await expect(unwrap(api.GET('/me'))).rejects.toMatchObject({ status: 401 })
    const res = await unwrap(api.POST('/auth/refresh', WITH_CSRF))
    tokens.session = res.access_token
    await expect(unwrap(api.GET('/me'))).resolves.toMatchObject({ display_name: 'Luis Prieto' })
  })
})
