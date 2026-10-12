import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { tokenForPath } from '~/composables/useAuth'
import { ApiError, createAuthFetch, createHorusClient, unwrap } from '~/utils/api-client'
import { MOCK_PASSWORD, MOCK_TOTP_CODE, TENANTS } from '~~/mocks/data'
import { createMockFetch } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'

/**
 * API simulada de nodos y routers: alta, edición con If-Match, bajas y sus errores del contrato
 * (SITE_NOT_EMPTY, ROUTER_PRIMARY_EXISTS, 412, 428), credenciales write-only con re-auth y
 * confirmación de prefijos.
 */

const ADMIN = 'ana.ruiz@fibranorte.example'

function memoryStorage() {
  const map = new Map<string, string>()
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  }
}

let local: ReturnType<typeof memoryStorage>
beforeEach(() => {
  local = memoryStorage()
  vi.stubGlobal('localStorage', local)
})
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
    api.POST('/auth/login', { body: { username: ADMIN, password: MOCK_PASSWORD } }),
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

async function fails(p: Promise<unknown>) {
  try {
    await p
  } catch (e) {
    if (e instanceof ApiError) return e
    throw e
  }
  throw new Error('se esperaba un error')
}

describe('API simulada: nodos y routers', () => {
  it('inventario vacío → alta de nodo y router; el nodo pasa a tener principal', async () => {
    local.setItem('horus.mock.emptyInventory', '1')
    const api = await client()
    expect((await unwrap(api.GET('/sites'))).data).toEqual([])
    const site = await unwrap(api.POST('/sites', { body: { name: 'Nodo Puerto', code: 'PTO' } }))
    expect(site).toMatchObject({ name: 'Nodo Puerto', primary_router_id: null, version: 1 })
    expect((await unwrap(api.GET('/sites'))).data.map((s) => s.name)).toEqual(['Nodo Puerto'])

    const router = await unwrap(
      api.POST('/routers', {
        body: { site_id: site.id, name: 'rt-puerto', routeros_version: '7.16.2' },
      }),
    )
    expect(router).toMatchObject({
      is_primary: true,
      onboarding_state: 'pending_configuration',
      routeros_version_supported: true,
      warnings: [],
    })
    const again = await unwrap(
      api.GET('/sites/{site_id}', { params: { path: { site_id: site.id } } }),
    )
    expect(again.primary_router_id).toBe(router.id)

    // Segundo principal → 409; como secundario sí.
    const dup = await fails(
      unwrap(api.POST('/routers', { body: { site_id: site.id, name: 'rt-puerto-2' } })),
    )
    expect(dup.code).toBe('ROUTER_PRIMARY_EXISTS')
    const second = await unwrap(
      api.POST('/routers', { body: { site_id: site.id, name: 'rt-puerto-2', is_primary: false } }),
    )
    expect(second.is_primary).toBe(false)
    const taken = await fails(
      unwrap(
        api.POST('/routers', { body: { site_id: site.id, name: 'rt-puerto', is_primary: false } }),
      ),
    )
    expect(taken.code).toBe('ALREADY_EXISTS')
  })

  it('RouterOS < 7.12 se registra con aviso y el script lo rechaza; fuera de 7.x es 422', async () => {
    local.setItem('horus.mock.emptyInventory', '1')
    const api = await client()
    const site = await unwrap(api.POST('/sites', { body: { name: 'Nodo Viejo' } }))
    const bad = await fails(
      unwrap(
        api.POST('/routers', {
          body: { site_id: site.id, name: 'rt-v6', routeros_version: '6.49' },
        }),
      ),
    )
    expect(bad.status).toBe(422)
    expect(bad.problem.errors?.[0]?.field).toBe('routeros_version')
    const old = await unwrap(
      api.POST('/routers', { body: { site_id: site.id, name: 'rt-v7', routeros_version: '7.11' } }),
    )
    expect(old.routeros_version_supported).toBe(false)
    expect(old.warnings).toContain('routeros_version_unsupported')
    const res = await api.POST('/routers/{router_id}/provisioning-script', {
      params: { path: { router_id: old.id }, header: { 'Idempotency-Key': crypto.randomUUID() } },
      body: {},
      parseAs: 'text',
    })
    expect(res.response.status).toBe(422)
    expect(JSON.stringify(res.error)).toContain('ROUTEROS_VERSION_UNSUPPORTED')
  })

  it('PATCH con If-Match: 428 sin cabecera, 412 con versión vieja (con current), 200 y versión nueva', async () => {
    const api = await client()
    const site = (await unwrap(api.GET('/sites'))).data[0]!
    const id = { site_id: site.id }
    const noHeader = await fails(
      unwrap(
        api.PATCH('/sites/{site_id}', {
          params: { path: id, header: { 'If-Match': '' } },
          body: { name: 'X' },
        }),
      ),
    )
    expect(noHeader.status).toBe(428)
    const updated = await unwrap(
      api.PATCH('/sites/{site_id}', {
        params: { path: id, header: { 'If-Match': `"${site.version}"` } },
        body: { name: site.name, address: 'Calle 1' },
      }),
    )
    expect(updated).toMatchObject({ address: 'Calle 1', version: site.version + 1 })
    const stale = await fails(
      unwrap(
        api.PATCH('/sites/{site_id}', {
          params: { path: id, header: { 'If-Match': `"${site.version}"` } },
          body: { name: 'Otro' },
        }),
      ),
    )
    expect(stale.status).toBe(412)
    expect(stale.problem.current).toMatchObject({ version: site.version + 1 })

    // Cambio concurrente simulado (una vez).
    local.setItem('horus.mock.conflictOnce', '1')
    const conflict = await fails(
      unwrap(
        api.PATCH('/sites/{site_id}', {
          params: { path: id, header: { 'If-Match': `"${updated.version}"` } },
          body: { name: 'Otro' },
        }),
      ),
    )
    expect(conflict.status).toBe(412)
    expect(local.getItem('horus.mock.conflictOnce')).toBeNull()
  })

  it('borrar un nodo con routers → SITE_NOT_EMPTY; sin routers → 204 y desaparece', async () => {
    const api = await client()
    const site = (await unwrap(api.GET('/sites'))).data[0]!
    const del = () =>
      api.DELETE('/sites/{site_id}', {
        params: { path: { site_id: site.id }, header: { 'If-Match': `"${site.version}"` } },
      })
    expect((await fails(unwrap(del()))).code).toBe('SITE_NOT_EMPTY')
    const routers = (await unwrap(api.GET('/routers', { params: { query: { site_id: site.id } } })))
      .data
    for (const r of routers) {
      await unwrap(
        api.DELETE('/routers/{router_id}', {
          params: { path: { router_id: r.id }, header: { 'If-Match': `"${r.version}"` } },
        }),
      )
    }
    const gone = await fails(
      unwrap(api.GET('/routers/{router_id}', { params: { path: { router_id: routers[0]!.id } } })),
    )
    expect(gone.status).toBe(404)
    expect((await del()).response.status).toBe(204)
    expect((await unwrap(api.GET('/sites'))).data.some((s) => s.id === site.id)).toBe(false)
  })

  it('credenciales: validación, re-auth y estado sin secretos', async () => {
    const api = await client()
    const router = (await unwrap(api.GET('/routers'))).data[0]!
    const path = { router_id: router.id, kind: 'routeros_api' as const }
    const short = await fails(
      unwrap(
        api.PUT('/routers/{router_id}/credentials/{kind}', {
          params: { path },
          body: { username: 'horus-ro', password: 'corta' },
        }),
      ),
    )
    expect(short.problem.errors?.[0]).toMatchObject({ field: 'password', code: 'TOO_SHORT' })

    local.setItem('horus.mock.reauthRequired', '1')
    const reauth = await fails(
      unwrap(
        api.PUT('/routers/{router_id}/credentials/{kind}', {
          params: { path: { ...path, kind: 'snmp' } },
          body: {
            username: 'horus-snmp',
            auth_protocol: 'sha256',
            auth_password: 'clave-auth-1',
            priv_protocol: 'aes128',
            priv_password: 'clave-priv-1',
          },
        }),
      ),
    )
    expect(reauth.code).toBe('REAUTH_REQUIRED')
    local.removeItem('horus.mock.reauthRequired')

    const status = await unwrap(
      api.PUT('/routers/{router_id}/credentials/{kind}', {
        params: { path },
        body: {
          username: 'horus-ro',
          password: 'una-clave-larga-16',
          tls_fingerprint_sha256: null,
        },
      }),
    )
    expect(status).toMatchObject({ kind: 'routeros_api', configured: true })
    expect(JSON.stringify(status)).not.toContain('una-clave-larga-16')
    const after = await unwrap(
      api.GET('/routers/{router_id}', { params: { path: { router_id: router.id } } }),
    )
    expect(after.credentials?.find((c) => c.kind === 'routeros_api')?.updated_at).toBe(
      status.updated_at,
    )
  })

  it('confirmar un prefijo sin confirmar (If-Match)', async () => {
    const api = await client()
    const norte = (await unwrap(api.GET('/sites'))).data.find((s) => s.name === 'Nodo Norte')!
    const list = (
      await unwrap(
        api.GET('/sites/{site_id}/client-prefixes', { params: { path: { site_id: norte.id } } }),
      )
    ).data
    const pending = list.find((p) => !p.confirmed)!
    expect(pending).toBeDefined()
    const confirmed = await unwrap(
      api.POST('/client-prefixes/{client_prefix_id}/confirm', {
        params: {
          path: { client_prefix_id: pending.id },
          header: { 'If-Match': `"${pending.version}"` },
        },
      }),
    )
    expect(confirmed).toMatchObject({ confirmed: true, version: pending.version + 1 })
  })
})
