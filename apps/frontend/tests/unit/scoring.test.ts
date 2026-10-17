import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { tokenForPath } from '~/composables/useAuth'
import { createAuthFetch, createHorusClient, unwrap } from '~/utils/api-client'
import {
  candidateLevel,
  commercialScore,
  decisionReason,
  scoreColor,
  sortCandidates,
  topReasons,
} from '~/utils/scoring'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'
import type { Customer, Reason } from '~~/types/api'

/** Scoring residencial/comercial (I2): utilidades de presentación y API simulada. */

const reasons: Reason[] = [
  { code: 'always_on', detail: 'Actividad en el 97 % de las horas', weight: 0.1 },
  { code: 'dynamic_pool', detail: 'Pool dinámico', weight: null },
  { code: 'inbound_services', detail: '3 puertos con 240 IPs', weight: 0.3 },
  { code: 'upload_vs_residential_median', detail: 'Subida 5,2×', weight: 0.2 },
]

function cust(over: Partial<Customer>): Customer {
  return {
    id: 'c',
    commercial_score: null,
    suggested_kind: null,
    suggested_confidence: null,
    suggested_reasons: [],
    commercial_use_suspected: false,
    ...over,
  } as Customer
}

describe('utilidades del scoring', () => {
  it('topReasons ordena por peso y deja al final las de peso nulo', () => {
    expect(topReasons(reasons, 3).map((r) => r.code)).toEqual([
      'inbound_services',
      'upload_vs_residential_median',
      'always_on',
    ])
    expect(topReasons(reasons, 10).at(-1)!.code).toBe('dynamic_pool')
    expect(topReasons(null)).toEqual([])
  })

  it('commercialScore y candidateLevel', () => {
    expect(commercialScore(cust({ commercial_score: 64 }))).toBe(64)
    expect(commercialScore(cust({ suggested_kind: 'commercial', suggested_confidence: 87 }))).toBe(
      87,
    )
    expect(commercialScore(cust({}))).toBeNull()
    expect(candidateLevel(cust({ suggested_kind: 'commercial' }))).toBe('commercial')
    expect(candidateLevel(cust({ commercial_use_suspected: true }))).toBe('suspected')
    expect(candidateLevel(cust({}))).toBe('none')
  })

  it('sortCandidates: mayor puntuación primero, estable y sin puntuación al final', () => {
    const list = [
      cust({ id: 'a', commercial_score: 55 }),
      cust({ id: 'b' }),
      cust({ id: 'c', commercial_score: 91 }),
      cust({ id: 'd', commercial_score: 55 }),
    ]
    expect(sortCandidates(list).map((c) => c.id)).toEqual(['c', 'a', 'd', 'b'])
  })

  it('scoreColor sigue los umbrales del ISP (info ≥ comercial, warning ≥ indicios)', () => {
    expect(scoreColor(85)).toBe('info')
    expect(scoreColor(60)).toBe('warning')
    expect(scoreColor(20)).toBe('neutral')
    expect(scoreColor(null)).toBe('neutral')
    expect(scoreColor(75, { commercial: 70, suspected: 40 })).toBe('info')
  })

  it('decisionReason compone un motivo válido para set-kind (3–500)', () => {
    const r = decisionReason(
      'confirm',
      cust({ commercial_score: 87, suggested_reasons: reasons }),
      { confirm: 'Confirmado', discard: 'Descartado' },
    )
    expect(r).toBe(
      'Confirmado (87/100 · inbound_services, upload_vs_residential_median, always_on)',
    )
    expect(decisionReason('discard', cust({}), { confirm: 'C', discard: 'Descartado' })).toBe(
      'Descartado',
    )
  })
})

// --- API simulada ---------------------------------------------------------------------------

function memoryStorage() {
  const map = new Map<string, string>()
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  }
}

beforeEach(() => vi.stubGlobal('localStorage', memoryStorage()))
afterEach(() => {
  vi.unstubAllGlobals()
  resetMockState()
})

async function client() {
  const tokens: { session: string | null; scoped: { token: string } | null } = {
    session: null,
    scoped: null,
  }
  const api = createHorusClient(
    'http://horus.test/api/v1',
    createAuthFetch({
      fetch: createMockFetch({ latencyMs: 0, storage: memoryStorage() }),
      basePath: '/api/v1',
      getAccessToken: (path) => tokenForPath(path, tokens.session, tokens.scoped),
    }),
  )
  const res = await unwrap(
    api.POST('/auth/login', {
      body: { username: 'ana.ruiz@fibranorte.example', password: MOCK_PASSWORD },
    }),
  )
  if (!('mfa_required' in res)) throw new Error('se esperaba 2FA')
  const session = await unwrap(
    api.POST('/auth/mfa/verify', { body: { mfa_token: res.mfa_token, code: MOCK_TOTP_CODE } }),
  )
  tokens.session = session.access_token
  const scoped = await unwrap(
    api.POST('/auth/token', { body: { tenant_id: TENANTS.fibraNorte.tenant_id } }),
  )
  tokens.scoped = { token: scoped.access_token }
  return api
}

describe('API simulada: posibles comerciales', () => {
  it('lista candidatos sin tipo fijado, explica el scoring y confirmar los saca de la lista', async () => {
    const api = await client()
    const page = await unwrap(
      api.GET('/customers', { params: { query: { commercial_candidate: true, limit: 200 } } }),
    )
    expect(page.data.length).toBeGreaterThan(0)
    for (const c of page.data) {
      expect(c.kind_locked).toBe(false)
      expect(c.suggested_kind === 'commercial' || c.commercial_use_suspected).toBe(true)
      expect(c.suggested_reasons?.length).toBeGreaterThan(0)
    }
    const target = sortCandidates(page.data)[0]!
    const scoring = await unwrap(
      api.GET('/customers/{customer_id}/scoring', {
        params: { path: { customer_id: target.id }, query: { range: '90d' } },
      }),
    )
    expect(scoring.latest?.model_version).toBe('commercial-v1')
    expect(scoring.latest?.features).toHaveLength(6)
    expect(scoring.series).toHaveLength(90)

    const updated = await unwrap(
      api.POST('/customers/{customer_id}/set-kind', {
        params: {
          path: { customer_id: target.id },
          header: { 'If-Match': `"${target.version}"` },
        },
        body: { kind: 'commercial', reason: 'Confirmado como comercial' },
      }),
    )
    expect(updated).toMatchObject({ kind: 'commercial', kind_locked: true })
    const after = await unwrap(
      api.GET('/customers', { params: { query: { commercial_candidate: true, limit: 200 } } }),
    )
    expect(after.data.map((c) => c.id)).not.toContain(target.id)
  })
})
