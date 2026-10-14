import { readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'
import { checkAdminRequest, type AdminRequest } from '../../server/lib/auth/guard'
import type { SessionInfo } from '../../server/lib/auth/service'

const ROOT = join(__dirname, '../../server/api/admin')

/** Todas las rutas de server/api/admin, a partir de los nombres de archivo de Nitro. */
function adminRoutes(dir = ROOT): { method: string; path: string }[] {
  const out: { method: string; path: string }[] = []
  for (const name of readdirSync(dir)) {
    const full = join(dir, name)
    if (statSync(full).isDirectory()) {
      out.push(...adminRoutes(full))
      continue
    }
    const m = /^(.+?)(?:\.(get|post|put|patch|delete))?\.ts$/.exec(name)
    if (!m) continue
    const rel = relative(ROOT, join(dir, m[1]!)).replace(/\\/g, '/')
    const path = ('/api/admin/' + rel)
      .replace(/\/index$/, '')
      .replace(/\[\.\.\.[^\]]+\]/g, 'x/y')
      .replace(/\[[^\]]+\]/g, '1')
    out.push({ method: (m[2] ?? 'get').toUpperCase(), path })
  }
  return out
}

const session = (stage: SessionInfo['stage']): SessionInfo => ({
  idHash: 'h',
  adminId: 1,
  email: 'ana@kns.gt',
  name: '',
  stage,
  csrf: 'csrf-de-la-sesion',
  expiresAt: Date.now() + 1000,
})

const req = (over: Partial<AdminRequest>): AdminRequest => ({
  method: 'GET',
  path: '/api/admin/requests',
  host: 'horusflow.kns.gt',
  ...over,
})

const OPEN = ['/api/admin/auth/login', '/api/admin/auth/csrf', '/api/admin/auth/session']

describe('permisos de /api/admin', () => {
  const routes = adminRoutes()

  it('hay rutas que comprobar', () => {
    expect(routes.length).toBeGreaterThan(10)
  })

  it('ninguna ruta de datos responde sin sesión completa', () => {
    for (const r of routes.filter((r) => !OPEN.includes(r.path))) {
      const base = { method: r.method, path: r.path, origin: 'https://horusflow.kns.gt' }
      const anon = checkAdminRequest(req({ ...base, csrfHeader: 'x', preCsrfCookie: 'x' }), null)
      expect(anon, `${r.method} ${r.path}`).toMatchObject({ ok: false })
      expect([401, 403]).toContain((anon as { status: number }).status)
      if (r.path.startsWith('/api/admin/auth/')) continue
      for (const stage of ['mfa', 'enroll'] as const) {
        const half = checkAdminRequest(
          req({ ...base, csrfHeader: 'csrf-de-la-sesion' }),
          session(stage),
        )
        expect(half, `${r.method} ${r.path} (${stage})`).toMatchObject({ ok: false, status: 401 })
      }
      const full = checkAdminRequest(
        req({ ...base, csrfHeader: 'csrf-de-la-sesion' }),
        session('full'),
      )
      expect(full, `${r.method} ${r.path} (full)`).toEqual({ ok: true })
    }
  })

  it('variantes de la ruta (mayúsculas, barras, codificación) no se saltan el control', () => {
    for (const path of [
      '/API/Admin/requests',
      '/api/admin//requests/',
      '/api/%61dmin/requests',
      '/api/admin',
    ]) {
      expect(checkAdminRequest(req({ path }), null), path).toMatchObject({ ok: false, status: 401 })
    }
  })

  it('CSRF: toda petición que cambia algo exige el token de la sesión', () => {
    const post = { method: 'POST', path: '/api/admin/pricing', origin: 'https://horusflow.kns.gt' }
    expect(checkAdminRequest(req(post), session('full'))).toMatchObject({
      status: 403,
      code: 'CSRF',
    })
    expect(checkAdminRequest(req({ ...post, csrfHeader: 'otro' }), session('full'))).toMatchObject({
      code: 'CSRF',
    })
    expect(
      checkAdminRequest(req({ ...post, csrfHeader: 'csrf-de-la-sesion' }), session('full')),
    ).toEqual({ ok: true })
  })

  it('login: doble envío del token CSRF y mismo origen', () => {
    const login = { method: 'POST', path: '/api/admin/auth/login' }
    expect(checkAdminRequest(req(login), null)).toMatchObject({ code: 'CSRF' })
    expect(
      checkAdminRequest(req({ ...login, csrfHeader: 'a', preCsrfCookie: 'b' }), null),
    ).toMatchObject({ code: 'CSRF' })
    expect(checkAdminRequest(req({ ...login, csrfHeader: 'a', preCsrfCookie: 'a' }), null)).toEqual(
      { ok: true },
    )
    expect(
      checkAdminRequest(
        req({ ...login, csrfHeader: 'a', preCsrfCookie: 'a', origin: 'https://evil.example' }),
        null,
      ),
    ).toMatchObject({ code: 'BAD_ORIGIN' })
    expect(
      checkAdminRequest(
        req({ ...login, csrfHeader: 'a', preCsrfCookie: 'a', fetchSite: 'cross-site' }),
        null,
      ),
    ).toMatchObject({
      code: 'CROSS_SITE',
    })
  })

  it('etapas: el segundo factor solo con sesión "mfa" y el alta solo con "enroll"', () => {
    const mfa = { method: 'POST', path: '/api/admin/auth/mfa', csrfHeader: 'csrf-de-la-sesion' }
    expect(checkAdminRequest(req(mfa), session('mfa'))).toEqual({ ok: true })
    expect(checkAdminRequest(req(mfa), session('enroll'))).toMatchObject({ status: 409 })
    expect(checkAdminRequest(req({ path: '/api/admin/auth/enroll' }), session('enroll'))).toEqual({
      ok: true,
    })
    expect(
      checkAdminRequest(req({ path: '/api/admin/auth/enroll' }), session('mfa')),
    ).toMatchObject({ status: 409 })
  })
})
