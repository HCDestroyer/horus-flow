import { describe, expect, it } from 'vitest'
import { createApiClient, type ApiError } from '~/utils/api-client'
import { createMockTransport } from '~~/mocks/transport'
import { MOCK_PASSWORD, MOCK_TOTP_CODE } from '~~/mocks/data'
import type { AccessTokenResponse, LoginResponse, Me } from '~~/shared/api/types'

function memoryStorage() {
  const map = new Map<string, string>()
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  }
}

function setup() {
  let token: string | null = null
  const api = createApiClient({
    transport: createMockTransport({
      latencyMs: 0,
      storage: memoryStorage(),
      scenario: () => 'normal',
    }),
    getAccessToken: () => token,
  })
  return { api, setToken: (t: string) => (token = t) }
}

describe('API simulada', () => {
  it('mismo error para usuario inexistente y contraseña incorrecta', async () => {
    const { api } = setup()
    const a = (await api
      .post('/auth/login', { email: 'nadie@x.example', password: 'x' })
      .catch((e: unknown) => e)) as ApiError
    const b = (await api
      .post('/auth/login', { email: 'noc@fibranorte.example', password: 'x' })
      .catch((e: unknown) => e)) as ApiError
    expect(a.code).toBe('INVALID_CREDENTIALS')
    expect(b.code).toBe(a.code)
    expect(b.problem.title).toBe(a.problem.title)
  })

  it('login con 2FA: devuelve mfa_token y el código correcto da un token de sesión', async () => {
    const { api, setToken } = setup()
    const res = await api.post<LoginResponse>('/auth/login', {
      email: 'ana.ruiz@fibranorte.example',
      password: MOCK_PASSWORD,
    })
    expect('mfa_required' in res && res.mfa_required).toBe(true)
    const mfaToken = 'mfa_token' in res ? res.mfa_token : ''
    await expect(
      api.post('/auth/mfa/verify', { mfa_token: mfaToken, code: '000000' }),
    ).rejects.toMatchObject({ code: 'INVALID_CREDENTIALS' })
    const ok = await api.post<AccessTokenResponse>('/auth/mfa/verify', {
      mfa_token: mfaToken,
      code: MOCK_TOTP_CODE,
    })
    setToken(ok.access_token)
    const me = await api.get<Me>('/me')
    expect(me.memberships.map((m) => m.tenant_slug)).toEqual(['fibra-norte', 'valle-conecta'])
  })

  it('bloquea tras 5 intentos fallidos con 429 y Retry-After', async () => {
    const { api } = setup()
    const attempt = () =>
      api
        .post('/auth/login', { email: 'noc@fibranorte.example', password: 'mal' })
        .catch((e: ApiError) => e)
    for (let i = 0; i < 5; i++) await attempt()
    const error = await attempt()
    expect(error).toMatchObject({ status: 429, code: 'RATE_LIMITED' })
    expect((error as ApiError).retryAfter).toBeGreaterThan(0)
  })

  it('el refresh exige X-Requested-With y recupera la sesión iniciada', async () => {
    const { api } = setup()
    await expect(api.post('/auth/refresh')).rejects.toMatchObject({ code: 'UNAUTHENTICATED' })
    await api.post('/auth/login', { email: 'noc@fibranorte.example', password: MOCK_PASSWORD })
    const res = await api.post<AccessTokenResponse>('/auth/refresh')
    expect(res.access_token).toMatch(/^mock\./)
  })

  it('las rutas autenticadas sin token responden 401', async () => {
    const { api } = setup()
    await expect(api.get('/me')).rejects.toMatchObject({ status: 401 })
  })
})
