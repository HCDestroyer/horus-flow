import { beforeEach, describe, expect, it } from 'vitest'
import type { LibreNmsIntegration, LibreNmsSyncResult, LibreNmsTestResult } from '~~/types/api'
import { TENANTS } from '~~/mocks/data'
import { routersOf } from '~~/mocks/inventory'
import { librenmsRoute, type LibreNmsRouteContext } from '~~/mocks/librenms'
import { mockState, resetMockState } from '~~/mocks/state'

/**
 * API simulada de la integración con LibreNMS (D17): aprovisionamiento idempotente, prueba de
 * extremo a extremo con el paso que falla y configuración de routers (IP de gestión y modo).
 */
const NOW = new Date('2026-10-14T12:00:00Z')
const tenant = TENANTS.fibraNorte
const CHANNEL = '0194c999-0000-7000-8000-000000000001'

function ctx(
  method: string,
  path: string,
  body: Record<string, unknown> = {},
): LibreNmsRouteContext {
  return {
    req: new Request(`http://mock/api/v1${path}`, { method }),
    path,
    method,
    body,
    tenant,
    can: () => true,
    now: NOW,
  }
}

async function call<T>(method: string, path: string, body: Record<string, unknown> = {}) {
  const res = librenmsRoute(ctx(method, path, body))!
  return { status: res.status, body: (res.status === 204 ? null : await res.json()) as T }
}

function seedChannel(baseUrl: string, hasCredentials = true) {
  mockState.channels.set(tenant.tenant_id, [
    {
      id: CHANNEL,
      tenant_id: tenant.tenant_id,
      created_at: NOW.toISOString(),
      updated_at: NOW.toISOString(),
      version: 1,
      name: 'LibreNMS',
      kind: 'librenms',
      enabled: true,
      include_personal_data: false,
      config: { base_url: baseUrl, username: 'horus' },
      subscription: { event_types: ['finding_opened'], site_ids: [] },
      has_credentials: hasCredentials,
      status: 'ok',
      last_delivery_at: null,
      last_error: null,
    },
  ])
}

beforeEach(() => resetMockState())

describe('integración con LibreNMS (mock)', () => {
  it('sincroniza de forma idempotente e incluye los routers con IP de gestión', async () => {
    seedChannel('https://librenms.fibranorte.example')
    const router = routersOf(tenant, NOW)[0]!.router
    expect(
      (await call('PUT', `/librenms/routers/${router.id}`, { mgmt_ip: '192.0.2.10', mode: 'ping' }))
        .status,
    ).toBe(200)
    const first = await call<LibreNmsSyncResult>(
      'POST',
      `/notification-channels/${CHANNEL}/librenms/sync`,
    )
    expect(first.body.ok).toBe(true)
    expect(first.body.integration.objects.some((o) => o.key === router.id)).toBe(true)
    const second = await call<LibreNmsSyncResult>(
      'POST',
      `/notification-channels/${CHANNEL}/librenms/sync`,
    )
    expect(second.body.steps.filter((s) => s.action === 'created')).toEqual([])
    const ids = (r: LibreNmsSyncResult) => r.integration.objects.map((o) => o.remote_id).sort()
    expect(ids(second.body)).toEqual(ids(first.body))
    const got = await call<LibreNmsIntegration>('GET', `/notification-channels/${CHANNEL}/librenms`)
    expect(got.body.status).toBe('ok')
  })

  it('informa el paso exacto que falla en la prueba de extremo a extremo', async () => {
    seedChannel('https://nosyslog.librenms.example')
    expect((await call('POST', `/notification-channels/${CHANNEL}/librenms/test`)).status).toBe(409)
    await call('POST', `/notification-channels/${CHANNEL}/librenms/sync`)
    const test = await call<LibreNmsTestResult>(
      'POST',
      `/notification-channels/${CHANNEL}/librenms/test`,
    )
    expect(test.body.ok).toBe(false)
    expect(test.body.failed_step).toBe('syslog_stored')
    expect(test.body.error_code).toBe('syslog_not_stored')
  })

  it('sin credenciales o con token rechazado no aprovisiona', async () => {
    seedChannel('https://librenms.fibranorte.example', false)
    const r = await call<LibreNmsSyncResult>(
      'POST',
      `/notification-channels/${CHANNEL}/librenms/sync`,
    )
    expect(r.body.error_code).toBe('missing_credentials')
    seedChannel('https://fail.librenms.example')
    const r2 = await call<LibreNmsSyncResult>(
      'POST',
      `/notification-channels/${CHANNEL}/librenms/sync`,
    )
    expect(r2.body.error_code).toBe('auth_failed')
  })

  it('exige consentimiento para reutilizar el SNMPv3 de Horus', async () => {
    const router = routersOf(tenant, NOW)[0]!.router
    const path = `/librenms/routers/${router.id}`
    expect((await call('PUT', path, { mgmt_ip: '192.0.2.10', mode: 'horus_snmp_v3' })).status).toBe(
      422,
    )
    const ok = await call<{ horus_snmp_reuse_consent: boolean }>('PUT', path, {
      mgmt_ip: '192.0.2.10',
      mode: 'horus_snmp_v3',
      reuse_horus_snmp_consent: true,
    })
    expect(ok.body.horus_snmp_reuse_consent).toBe(true)
    expect((await call('PUT', path, { mgmt_ip: 'no-es-ip', mode: 'ping' })).status).toBe(422)
  })
})
