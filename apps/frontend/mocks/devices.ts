import type { ClientPrefix, CredentialStatus } from '~~/types/api'
import { forbidden, ifMatch, json, noContent, notFound, problem } from './http'
import {
  devicesOf,
  findSite,
  prefixesOf,
  routersOf,
  sitesOf,
  siteRouters,
  toSite,
} from './inventory'
import { hash, mockUuid } from './random'
import { mockState, type RouterFields, type SiteFields } from './state'
import type { TenantRouteContext } from './tenant-routes'

/**
 * Alta, edición y baja de nodos y routers, credenciales write-only y confirmación de prefijos
 * (contrato v0, `devices.yaml`). Mismas reglas y errores que el servicio `devices`:
 *
 * - `PATCH`/`DELETE` con `If-Match` (falta → 428; no coincide → 412 con `current`).
 * - Nodo con routers → `409 SITE_NOT_EMPTY`; segundo principal → `409 ROUTER_PRIMARY_EXISTS`;
 *   código de nodo o nombre de router repetidos → `409 ALREADY_EXISTS`.
 * - RouterOS < 7.12 se acepta con aviso `routeros_version_unsupported` (el script de alta lo
 *   rechaza con `422 ROUTEROS_VERSION_UNSUPPORTED`); fuera de 7.x → 422.
 * - `PUT /routers/{id}/credentials/{kind}` es `x-reauth` y nunca devuelve el secreto.
 *
 * Simulaciones para pruebas (localStorage):
 * - `horus.mock.conflictOnce = '1'`: el siguiente `PATCH` de un nodo o router encuentra un
 *   cambio de otra sesión (412) y la marca se borra.
 * - `horus.mock.reauthRequired = '1'`: las rutas `x-reauth` piden re-autenticarse.
 */

type Params = Record<string, string>

function match(ctx: TenantRouteContext, method: string, pattern: string): Params | null {
  if (ctx.method !== method) return null
  const names: string[] = []
  const re = new RegExp(
    `^${pattern.replace(/\{(\w+)\}/g, (_, n: string) => {
      names.push(n)
      return '([^/]+)'
    })}$`,
  )
  const m = ctx.path.match(re)
  if (!m) return null
  return Object.fromEntries(names.map((n, i) => [n, decodeURIComponent(m[i + 1]!)]))
}

const etag = (version: number) => ({ ETag: `"${version}"` })

function validation(errors: { field: string; code: string; message: string }[]) {
  return problem(422, 'VALIDATION_FAILED', 'Revisa los campos marcados', {}, { errors })
}

function preconditionFailed(current: unknown) {
  return problem(
    412,
    'PRECONDITION_FAILED',
    'El recurso cambió mientras lo editabas',
    {},
    { current: current as Record<string, unknown> },
  )
}

const preconditionRequired = () => problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')

/** ¿Toca simular un cambio concurrente? Se consume al leerla. */
function takeConflict() {
  try {
    if (globalThis.localStorage?.getItem('horus.mock.conflictOnce') !== '1') return false
    globalThis.localStorage.removeItem('horus.mock.conflictOnce')
    return true
  } catch {
    return false
  }
}

const ROUTEROS = /^7\.[0-9]+(\.[0-9]+)?$/
const SITE_KINDS = ['node', 'region', 'datacenter', 'other']
const ADMIN_STATES = ['active', 'maintenance', 'decommissioned']

type Errors = { field: string; code: string; message: string }[]

function text(
  errors: Errors,
  body: Record<string, unknown>,
  key: string,
  opts: { required?: boolean; max?: number } = {},
) {
  const v = body[key]
  if (v === undefined) {
    if (opts.required) errors.push({ field: key, code: 'REQUIRED', message: 'Es obligatorio.' })
    return undefined
  }
  if (v === null) {
    if (opts.required) errors.push({ field: key, code: 'REQUIRED', message: 'Es obligatorio.' })
    return null
  }
  if (typeof v !== 'string') {
    errors.push({ field: key, code: 'INVALID_TYPE', message: 'Debe ser texto.' })
    return undefined
  }
  const s = v.trim()
  if (opts.required && !s) {
    errors.push({ field: key, code: 'REQUIRED', message: 'Es obligatorio.' })
    return undefined
  }
  if (opts.max && s.length > opts.max) {
    errors.push({ field: key, code: 'TOO_LONG', message: `Máximo ${opts.max} caracteres.` })
    return undefined
  }
  return s || null
}

function siteFields(body: Record<string, unknown>, create: boolean) {
  const errors: Errors = []
  const out: Partial<SiteFields> = {}
  const name = text(errors, body, 'name', { required: create || 'name' in body, max: 120 })
  if (name) out.name = name
  for (const [key, max] of [
    ['code', 32],
    ['address', 300],
    ['timezone', 64],
  ] as const) {
    const v = text(errors, body, key, { max })
    if (v !== undefined) out[key] = v
  }
  if (body.kind !== undefined) {
    if (!SITE_KINDS.includes(String(body.kind))) {
      errors.push({ field: 'kind', code: 'INVALID_VALUE', message: 'Tipo de nodo no válido.' })
    } else out.kind = body.kind as SiteFields['kind']
  }
  for (const [key, lim] of [
    ['latitude', 90],
    ['longitude', 180],
  ] as const) {
    const v = body[key]
    if (v === undefined) continue
    if (v === null) out[key] = null
    else if (typeof v !== 'number' || !Number.isFinite(v) || Math.abs(v) > lim) {
      errors.push({ field: key, code: 'OUT_OF_RANGE', message: `Entre -${lim} y ${lim}.` })
    } else out[key] = v
  }
  if (Array.isArray(body.tags)) out.tags = body.tags.map(String)
  return { errors, fields: out }
}

function routerFields(body: Record<string, unknown>, create: boolean) {
  const errors: Errors = []
  const out: Partial<RouterFields> = {}
  const name = text(errors, body, 'name', { required: create || 'name' in body, max: 120 })
  if (name) out.name = name
  for (const key of ['display_name', 'model'] as const) {
    const v = text(errors, body, key, { max: 120 })
    if (v !== undefined) out[key] = v
  }
  const version = text(errors, body, 'routeros_version', { max: 32 })
  if (version !== undefined) {
    if (version && !ROUTEROS.test(version)) {
      errors.push({
        field: 'routeros_version',
        code: 'ROUTEROS_VERSION_INVALID',
        message: 'Solo RouterOS 7.x (D15), p. ej. 7.16.1.',
      })
    } else out.routeros_version = version
  }
  if (create && body.vendor !== undefined) {
    if (body.vendor !== 'mikrotik' && body.vendor !== 'huawei') {
      errors.push({ field: 'vendor', code: 'INVALID_VALUE', message: 'mikrotik o huawei.' })
    } else out.vendor = body.vendor
  }
  if (body.management_address !== undefined) {
    const v = body.management_address
    if (v !== null && (typeof v !== 'string' || !isManagementAddress(v))) {
      errors.push({
        field: 'management_address',
        code: 'INVALID_VALUE',
        message: 'IP unicast del router tal y como la ve Horus.',
      })
    } else out.management_address = v
  }
  if (create && body.is_primary !== undefined) {
    if (typeof body.is_primary !== 'boolean') {
      errors.push({ field: 'is_primary', code: 'INVALID_TYPE', message: 'Debe ser booleano.' })
    } else out.is_primary = body.is_primary
  }
  if (!create && body.admin_state !== undefined) {
    if (!ADMIN_STATES.includes(String(body.admin_state))) {
      errors.push({ field: 'admin_state', code: 'INVALID_VALUE', message: 'Estado no válido.' })
    } else out.admin_state = body.admin_state as RouterFields['admin_state']
  }
  if (Array.isArray(body.tags)) out.tags = body.tags.map(String)
  return { errors, fields: out }
}

/** IP unicast aceptable como dirección de gestión (modo directo, sin loopback ni link-local). */
export function isManagementAddress(v: string) {
  const s = v.trim()
  if (/^(127\.|0\.|169\.254\.|22[4-9]\.|23\d\.|255\.)/.test(s)) return false
  if (/^(::1?|fe80:.*)$/i.test(s)) return false
  return /^(\d{1,3}\.){3}\d{1,3}$/.test(s) || /^[0-9a-f:]+$/i.test(s)
}

function credentialErrors(kind: string, body: Record<string, unknown>): Errors {
  const errors: Errors = []
  const str = (key: string, min: number) => {
    const v = body[key]
    if (typeof v !== 'string' || !v) {
      errors.push({ field: key, code: 'REQUIRED', message: `${key} es obligatorio.` })
    } else if (v.length < min) {
      errors.push({ field: key, code: 'TOO_SHORT', message: `Mínimo ${min} caracteres.` })
    }
  }
  const allowed = kind === 'snmp' ? SNMP_FIELDS : API_FIELDS
  for (const k of Object.keys(body)) {
    if (!allowed.includes(k)) {
      errors.push({ field: k, code: 'UNKNOWN_FIELD', message: 'Campo no admitido.' })
    }
  }
  if (kind === 'snmp') {
    str('username', 1)
    if (!['sha1', 'sha256'].includes(String(body.auth_protocol))) {
      errors.push({ field: 'auth_protocol', code: 'INVALID_VALUE', message: 'sha1 o sha256.' })
    }
    str('auth_password', 8)
    if (body.priv_protocol !== 'aes128') {
      errors.push({ field: 'priv_protocol', code: 'INVALID_VALUE', message: 'aes128.' })
    }
    str('priv_password', 8)
  } else {
    str('username', 1)
    str('password', 16)
    const fp = body.tls_fingerprint_sha256
    if (fp !== undefined && fp !== null) {
      const hex = String(fp).replaceAll(':', '').trim()
      if (!/^[0-9a-fA-F]{64}$/.test(hex)) {
        errors.push({
          field: 'tls_fingerprint_sha256',
          code: 'INVALID_FORMAT',
          message: "SHA-256 en hexadecimal (64 dígitos, con o sin ':').",
        })
      }
    }
  }
  return errors
}

const SNMP_FIELDS = ['username', 'auth_protocol', 'auth_password', 'priv_protocol', 'priv_password']
const API_FIELDS = ['username', 'password', 'tls_fingerprint_sha256']

export function deviceRoutes(ctx: TenantRouteContext): Response | undefined {
  const { tenant, now } = ctx
  const dev = devicesOf(tenant)
  const stamp = now.toISOString()
  let p: Params | null

  // --- Nodos ---------------------------------------------------------------------------------
  if (match(ctx, 'POST', '/sites')) {
    if (!ctx.can('sites.create')) return forbidden()
    const { errors, fields } = siteFields(ctx.body, true)
    if (errors.length) return validation(errors)
    if (fields.code && sitesOf(tenant).some((s) => toSite(tenant, s).code === fields.code)) {
      return problem(409, 'ALREADY_EXISTS', 'Ya existe un nodo con ese código.')
    }
    const index = 40 + dev.sites.length
    const id = mockUuid('0192e11a', hash(`${tenant.tenant_id}:site:${index}:${stamp}`))
    dev.sites.push({ id, index, created_at: stamp })
    dev.siteEdits.set(id, {
      version: 1,
      updated_at: stamp,
      fields: { kind: 'node', tags: [], ...fields },
    })
    mockState.prefixes.set(id, [])
    const site = toSite(tenant, findSite(tenant, id)!)
    return json(site, 201, { ...etag(1), Location: `/api/v1/sites/${id}` })
  }
  if ((p = match(ctx, 'PATCH', '/sites/{id}')) || (p = match(ctx, 'DELETE', '/sites/{id}'))) {
    const del = ctx.method === 'DELETE'
    if (!ctx.can(del ? 'sites.delete' : 'sites.update')) return forbidden()
    const site = findSite(tenant, p.id!)
    if (!site) return notFound('SITE_NOT_FOUND')
    const current = toSite(tenant, site)
    const expected = ifMatch(ctx.req)
    if (expected === null) return preconditionRequired()
    if (!del && takeConflict()) {
      const edit = dev.siteEdits.get(site.id)
      dev.siteEdits.set(site.id, {
        version: current.version + 1,
        updated_at: stamp,
        fields: { ...edit?.fields, address: 'Cra. 7 # 12-40 (editado en otra sesión)' },
      })
      return preconditionFailed(toSite(tenant, site))
    }
    if (expected !== current.version) return preconditionFailed(current)
    if (del) {
      const routers = siteRouters(tenant, site.id)
      if (routers.length) {
        return problem(
          409,
          'SITE_NOT_EMPTY',
          'El nodo tiene routers: délos de baja primero.',
          {},
          { detail: `Routers en el nodo: ${routers.length}` },
        )
      }
      dev.deletedSites.add(site.id)
      return noContent()
    }
    const { errors, fields } = siteFields(ctx.body, false)
    if (errors.length) return validation(errors)
    if (
      fields.code &&
      sitesOf(tenant).some((s) => s.id !== site.id && toSite(tenant, s).code === fields.code)
    ) {
      return problem(409, 'ALREADY_EXISTS', 'Ya existe un nodo con ese código.')
    }
    const edit = dev.siteEdits.get(site.id)
    dev.siteEdits.set(site.id, {
      version: current.version + 1,
      updated_at: stamp,
      fields: { ...edit?.fields, ...fields },
    })
    const next = toSite(tenant, findSite(tenant, site.id)!)
    return json(next, 200, etag(next.version))
  }

  // --- Routers -------------------------------------------------------------------------------
  const routers = () => routersOf(tenant, now, { degraded: ctx.degraded, stepMs: ctx.stepMs })

  if (match(ctx, 'POST', '/routers')) {
    if (!ctx.can('devices.create')) return forbidden()
    const { errors, fields } = routerFields(ctx.body, true)
    const siteId = typeof ctx.body.site_id === 'string' ? ctx.body.site_id : ''
    if (!siteId) {
      errors.unshift({
        field: 'site_id',
        code: 'REQUIRED',
        message: 'site_id (UUID) es obligatorio.',
      })
    } else if (!findSite(tenant, siteId)) {
      errors.unshift({ field: 'site_id', code: 'NOT_FOUND', message: 'El nodo no existe.' })
    }
    if (errors.length) return validation(errors)
    const all = routers()
    if (all.some((r) => r.router.name === fields.name)) {
      return problem(409, 'ALREADY_EXISTS', 'Ya existe un router con ese nombre en el ISP.')
    }
    const primary = fields.is_primary !== false
    if (primary && siteRouters(tenant, siteId).some((r) => r.is_primary)) {
      return problem(
        409,
        'ROUTER_PRIMARY_EXISTS',
        'El nodo ya tiene un router principal (D6: uno por nodo). Registre este como no principal o dé de baja el actual.',
      )
    }
    const index = 40 + dev.routers.length
    const id = mockUuid('0192e33a', hash(`${tenant.tenant_id}:router:${index}:${stamp}`))
    dev.routers.push({ id, site_id: siteId, index, created_at: stamp })
    dev.routerEdits.set(id, {
      version: 1,
      updated_at: stamp,
      fields: { ...fields, is_primary: primary },
    })
    const created = routers().find((r) => r.router.id === id)!.router
    return json(created, 201, { ...etag(1), Location: `/api/v1/routers/${id}` })
  }

  // /routers/thresholds es del rol snmp (mocks/snmp.ts).
  const routerMatch = ctx.path.match(
    /^\/routers\/(?!thresholds$)([^/]+)(?:\/credentials\/([^/]+))?$/,
  )
  const credentialPut = ctx.method === 'PUT' && !!routerMatch?.[2]
  if (routerMatch && (ctx.method === 'PATCH' || ctx.method === 'DELETE' || credentialPut)) {
    const permission = credentialPut
      ? 'devices.credentials.write'
      : ctx.method === 'PATCH'
        ? 'devices.update'
        : 'devices.delete'
    if (!ctx.can(permission)) return forbidden()
    const item = routers().find((r) => r.router.id === routerMatch[1])
    if (!item) return notFound('ROUTER_NOT_FOUND')
    const current = item.router

    if (credentialPut) {
      const kind = routerMatch[2]!
      if (ctx.reauthRequired) {
        return problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para guardar la credencial')
      }
      if (kind !== 'snmp' && kind !== 'routeros_api') {
        return validation([
          {
            field: 'kind',
            code: 'INVALID_VALUE',
            message: 'Valores permitidos: snmp, routeros_api.',
          },
        ])
      }
      const errors = credentialErrors(kind, ctx.body)
      if (errors.length) return validation(errors)
      const status: CredentialStatus = {
        kind,
        configured: true,
        updated_at: stamp,
        last_used_at: null,
        last_result: null,
      }
      const list = (dev.credentials.get(current.id) ?? []).filter((c) => c.kind !== kind)
      dev.credentials.set(current.id, [...list, status])
      return json(status, 200, { 'Cache-Control': 'no-store' })
    }

    const expected = ifMatch(ctx.req)
    if (expected === null) return preconditionRequired()
    if (ctx.method === 'PATCH' && takeConflict()) {
      const edit = dev.routerEdits.get(current.id)
      dev.routerEdits.set(current.id, {
        version: current.version + 1,
        updated_at: stamp,
        fields: { ...edit?.fields, admin_state: 'maintenance' },
      })
      return preconditionFailed(routers().find((r) => r.router.id === current.id)!.router)
    }
    if (expected !== current.version) return preconditionFailed(current)
    if (ctx.method === 'DELETE') {
      dev.deletedRouters.add(current.id)
      mockState.onboarding.delete(current.id)
      return noContent()
    }
    const { errors, fields } = routerFields(ctx.body, false)
    if (errors.length) return validation(errors)
    if (
      fields.name &&
      routers().some((r) => r.router.id !== current.id && r.router.name === fields.name)
    ) {
      return problem(409, 'ALREADY_EXISTS', 'Ya existe un router con ese nombre en el ISP.')
    }
    const edit = dev.routerEdits.get(current.id)
    dev.routerEdits.set(current.id, {
      version: current.version + 1,
      updated_at: stamp,
      fields: { ...edit?.fields, ...fields },
    })
    const next = routers().find((r) => r.router.id === current.id)!.router
    return json(next, 200, etag(next.version))
  }

  // --- Confirmar un prefijo ------------------------------------------------------------------
  if ((p = match(ctx, 'POST', '/client-prefixes/{id}/confirm'))) {
    if (!ctx.can('sites.update')) return forbidden()
    for (const site of sitesOf(tenant)) {
      const list = prefixesOf(tenant, site)
      const i = list.findIndex((x) => x.id === p!.id)
      if (i < 0) continue
      const current = list[i]!
      const expected = ifMatch(ctx.req)
      if (expected === null) return preconditionRequired()
      if (expected !== current.version) return preconditionFailed(current)
      if (current.confirmed) return json(current, 200, etag(current.version))
      const next: ClientPrefix = {
        ...current,
        confirmed: true,
        version: current.version + 1,
        updated_at: stamp,
      }
      list[i] = next
      return json(next, 200, etag(next.version))
    }
    return notFound('CLIENT_PREFIX_NOT_FOUND')
  }
  return undefined
}
