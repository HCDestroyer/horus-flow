import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ArgosConnectionLink, ArgosIntegration, SsoLink } from '~~/types/api'
import { TENANTS, USERS } from '~~/mocks/data'
import { integrationsRoute, resetIntegrationsMock } from '~~/mocks/integrations'
import { createLink, redeemTicket, resetSsoMock, tenantSsoLinksRoute } from '~~/mocks/sso'
import type { TenantRouteContext } from '~~/mocks/tenant-routes'
import {
  clearPendingLink,
  isSafeArgosEntryUrl,
  looksLikeArgosLink,
  readPendingLink,
  savePendingLink,
  ssoTargetPath,
  ticketFromHash,
} from '~/utils/sso'
import { safeRedirect } from '~/utils/redirect'

/**
 * Enlace de conexión (contrato ArgosISP §1.4) y autologin (§1.5) en la consola: el ticket solo
 * se toma del fragmento, la ruta de destino la construye la consola (sin open redirect), la URL
 * de ArgosISP se valida antes de abrirla y la API simulada respeta un uso, vinculación tras
 * iniciar sesión y revocación.
 */
const NOW = new Date('2026-10-17T12:00:00Z')
const CODE = 'A'.repeat(43)

describe('utilidades del autologin', () => {
  it('toma el ticket del fragmento solo con la forma esperada', () => {
    expect(ticketFromHash('#demo-ticket-de-prueba-0001')).toBe('demo-ticket-de-prueba-0001')
    expect(ticketFromHash('')).toBeNull()
    expect(ticketFromHash('#corto')).toBeNull()
    expect(ticketFromHash('#<script>alert(1)</script>-xxxxxxxxxxxx')).toBeNull()
  })

  it('construye rutas internas a partir del recurso resuelto (sin open redirect)', () => {
    const id = '0192f000-0000-7000-8000-00000000c001'
    expect(ssoTargetPath('fibra-norte', { kind: 'customer', id })).toBe(
      `/t/fibra-norte/clients/${id}`,
    )
    expect(ssoTargetPath('fibra-norte', { kind: 'router', id })).toBe(
      `/t/fibra-norte/routers/${id}`,
    )
    expect(ssoTargetPath('fibra-norte', { kind: 'finding', id })).toBe(
      `/t/fibra-norte/security/findings/${id}`,
    )
    // Un slug o id manipulados no escapan de la consola.
    expect(ssoTargetPath('//evil.example', { kind: 'customer', id: '../x' })).toBe(
      '/t/%2F%2Fevil.example/clients/..%2Fx',
    )
    for (const bad of [
      '//evil.example',
      'https://evil.example',
      '/\\evil',
      'javascript:alert(1)',
    ]) {
      expect(safeRedirect(bad)).toBe('/')
    }
    expect(safeRedirect('/sso/vincular')).toBe('/sso/vincular')
  })

  it('valida la URL de ArgosISP antes de abrirla', () => {
    const tk = 't'.repeat(43)
    expect(isSafeArgosEntryUrl(`https://panel.argos.example/sso/entrar#${tk}`)).toBe(true)
    expect(isSafeArgosEntryUrl(`https://panel.argos.example/sso/entrar?ticket=${tk}`)).toBe(false)
    expect(isSafeArgosEntryUrl(`http://panel.argos.example/sso/entrar#${tk}`)).toBe(false)
    expect(isSafeArgosEntryUrl(`https://u:p@panel.argos.example/sso/entrar#${tk}`)).toBe(false)
    expect(isSafeArgosEntryUrl(`https://evil.example/otra#${tk}`)).toBe(false)
    expect(isSafeArgosEntryUrl('javascript:alert(1)')).toBe(false)
  })

  it('reconoce un enlace de conexión de ArgosISP', () => {
    expect(looksLikeArgosLink(`https://panel.isp.example/conectar#${CODE}`)).toBe(true)
    expect(looksLikeArgosLink(`https://panel.isp.example/conectar?api=https://api.x#${CODE}`)).toBe(
      true,
    )
    expect(looksLikeArgosLink(`https://panel.isp.example/otra#${CODE}`)).toBe(false)
    expect(looksLikeArgosLink(`https://panel.isp.example/conectar?codigo=${CODE}`)).toBe(false)
  })

  it('guarda la vinculación pendiente solo mientras vale', () => {
    const mem = new Map<string, string>()
    vi.stubGlobal('sessionStorage', {
      getItem: (k: string) => mem.get(k) ?? null,
      setItem: (k: string, v: string) => mem.set(k, v),
      removeItem: (k: string) => mem.delete(k),
    })
    const target = {
      tenant_id: TENANTS.fibraNorte.tenant_id,
      tenant_slug: 'fibra-norte',
      type: 'servicio' as const,
      external_id: '812',
    }
    savePendingLink({
      linkToken: 'lt.x',
      linkExpiresAt: new Date(Date.now() + 60_000).toISOString(),
      externalUser: { id: 'u-17', email: null, name: 'Ana' },
      target,
    })
    expect(readPendingLink()?.linkToken).toBe('lt.x')
    expect(readPendingLink(Date.now() + 120_000)).toBeNull()
    clearPendingLink()
    expect(readPendingLink()).toBeNull()
    vi.unstubAllGlobals()
  })
})

function ctx(method: string, path: string, body: Record<string, unknown> = {}, perms?: string[]) {
  return {
    req: new Request(`http://mock/api/v1${path}`, { method }),
    url: new URL(`http://mock/api/v1${path}`),
    path,
    method,
    body,
    tenant: TENANTS.fibraNorte,
    userId: USERS[0]!.me.id,
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

describe('mock del enlace de conexión y el autologin', () => {
  beforeEach(() => {
    resetIntegrationsMock()
    resetSsoMock()
  })

  it('genera un enlace de 15 min y canjea el de Argos dejando el conector apagado', async () => {
    const link = await call<ArgosConnectionLink>('POST', '/integrations/argosisp/connection-links')
    expect(link.status).toBe(201)
    expect(link.body.url).toMatch(/\/conectar#[A-Za-z0-9_-]{43}$/)
    expect(link.body.ttl_seconds).toBe(900)
    const denied = await call('POST', '/integrations/argosisp/connection-links', {}, [
      'settings.manage',
    ])
    expect(denied.status).toBe(403)
    const bad = await call('POST', '/integrations/argosisp/connection-links/redeem', { link: 'x' })
    expect(bad.status).toBe(422)
    const ok = await call<ArgosIntegration>(
      'POST',
      '/integrations/argosisp/connection-links/redeem',
      {
        link: `https://panel.argos.example/conectar?api=https%3A%2F%2Fapi.argos.example#${CODE}`,
      },
    )
    expect(ok.status).toBe(200)
    expect(ok.body.enabled).toBe(false)
    expect(ok.body.base_url).toBe('https://api.argos.example/api/v1')
    expect(ok.body.connection?.method).toBe('link_redeemed')
  })

  it('canjea un ticket una vez y vincula solo tras iniciar sesión', async () => {
    const user = USERS[0]!
    const first = redeemTicket('demo-ticket-de-prueba-0001', NOW)
    expect(first.kind).toBe('link')
    expect(redeemTicket('demo-ticket-de-prueba-0001', NOW).kind).toBe('gone')
    expect(redeemTicket('vencido-ticket-de-prueba-01', NOW).kind).toBe('gone')
    const token = first.kind === 'link' ? String(first.body.link_token) : ''
    // Sesión anterior al canje → re-autenticación.
    const stale = createLink(user, token, NOW.getTime() - 1, NOW)
    expect(stale.status).toBe(403)
    const linked = createLink(user, token, NOW.getTime() + 1000, NOW)
    expect(linked.status).toBe(201)
    expect(createLink(user, token, NOW.getTime() + 1000, NOW).status).toBe(410)
    // Ya vinculado: el siguiente ticket entra como ese usuario.
    const again = redeemTicket('demo-ticket-de-prueba-0002', NOW)
    expect(again.kind).toBe('linked')
    // El administrador revoca todas.
    const list = tenantSsoLinksRoute({
      path: '/integrations/argosisp/sso/links',
      method: 'GET',
      tenantId: TENANTS.fibraNorte.tenant_id,
      can: () => true,
      reauthRequired: false,
    })!
    expect(((await list.json()) as { data: SsoLink[] }).data).toHaveLength(1)
    const all = tenantSsoLinksRoute({
      path: '/integrations/argosisp/sso/links',
      method: 'DELETE',
      tenantId: TENANTS.fibraNorte.tenant_id,
      can: () => true,
      reauthRequired: false,
    })!
    expect(await all.json()).toEqual({ revoked: 1 })
    expect(redeemTicket('demo-ticket-de-prueba-0003', NOW).kind).toBe('link')
  })
})
