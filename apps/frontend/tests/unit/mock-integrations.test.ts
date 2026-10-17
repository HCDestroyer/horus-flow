import { beforeEach, describe, expect, it } from 'vitest'
import type {
  ApiTokenIssued,
  ArgosIntegration,
  CustomerExternalIdentity,
  IpLookupResponse,
} from '~~/types/api'
import { TENANTS } from '~~/mocks/data'
import { customersOf } from '~~/mocks/inventory'
import { integrationsRoute, resetIntegrationsMock } from '~~/mocks/integrations'
import type { TenantRouteContext } from '~~/mocks/tenant-routes'

/**
 * API simulada de las integraciones (ADR-0030): API tokens hf_pat_ con plantilla ArgosISP,
 * conector con token write-only, identidad externa y consulta 360 por IP (IP en el cuerpo).
 */
const NOW = new Date('2026-10-17T12:00:00Z')
const tenant = TENANTS.fibraNorte

function ctx(method: string, path: string, body: Record<string, unknown> = {}, perms?: string[]) {
  return {
    req: new Request(`http://mock/api/v1${path}`, { method }),
    url: new URL(`http://mock/api/v1${path}`),
    path,
    method,
    body,
    tenant,
    userId: '01926b3e-1111-7000-8000-000000000001',
    can: (p: string) => !perms || perms.includes(p),
    now: NOW,
    degraded: false,
    scenario: 'normal',
    empty: false,
    stepMs: 0,
  } satisfies TenantRouteContext
}

async function call<T>(
  method: string,
  path: string,
  body: Record<string, unknown> = {},
  perms?: string[],
) {
  const res = integrationsRoute(ctx(method, path, body, perms))!
  return { status: res.status, body: (res.status === 204 ? null : await res.json()) as T }
}

beforeEach(() => resetIntegrationsMock())

describe('mock de integraciones', () => {
  it('crea un código de conexión ArgosISP que se muestra una vez y se puede revocar', async () => {
    const created = await call<ApiTokenIssued>('POST', '/api-tokens', {
      name: 'ArgosISP',
      template: 'argosisp',
    })
    expect(created.status).toBe(201)
    expect(created.body.token).toMatch(/^hf_pat_/)
    expect(created.body.permissions).toContain('traffic.customer.read')
    const list = await call<{ data: { id: string; token?: string }[] }>('GET', '/api-tokens')
    expect(list.body.data.some((t) => 'token' in t)).toBe(false)
    expect((await call('DELETE', `/api-tokens/${created.body.id}`)).status).toBe(204)
    expect((await call('DELETE', `/api-tokens/${created.body.id}`)).status).toBe(404)
    expect((await call('GET', '/api-tokens', {}, ['settings.read'])).status).toBe(403)
  })

  it('conector: token write-only, identidad con vigencia y consulta 360', async () => {
    const c = customersOf(tenant, NOW)[0]!
    const before = await call('POST', `/customers/${c.id}/external-identity/refresh`)
    expect(before.status).toBe(409)
    const conn = await call<ArgosIntegration>('PUT', '/integrations/argosisp', {
      base_url: 'https://argos.example/api/v1',
      enabled: true,
      token: 'atk_prueba_123456',
    })
    expect(conn.body.has_token).toBe(true)
    expect(JSON.stringify(conn.body)).not.toContain('atk_prueba')
    const id = await call<CustomerExternalIdentity>(
      'POST',
      `/customers/${c.id}/external-identity/refresh`,
    )
    expect(id.body.current?.display_name).toBeTruthy()
    const ip = c.address.split('/')[0]!
    const look = await call<IpLookupResponse>('POST', '/integrations/ip-lookup', { address: ip })
    expect(look.status).toBe(200)
    expect(look.body.customer.id).toBe(c.id)
    expect(look.body.external?.service_id).toBe(id.body.current?.service_id)
    const limited = await call<IpLookupResponse>(
      'POST',
      '/integrations/ip-lookup',
      { address: ip },
      ['customers.read'],
    )
    expect(limited.body.consumption).toBeNull()
    expect(limited.body.meta.omitted.map((o) => o.reason)).toContain('permission')
    expect(
      (await call('POST', '/integrations/ip-lookup', { address: '198.51.100.250' })).status,
    ).toBe(404)
  })
})
