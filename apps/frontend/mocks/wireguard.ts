import type { WireguardHub, WireguardPool } from '~~/types/api'
import { checkPoolCidr, cidrSize, type PoolCidrError } from '~/utils/cidr'
import { ALL_TENANTS } from './data'
import { forbidden, ifMatch, json, noContent, notFound, problem } from './http'
import { routersOf } from './inventory'
import { hash, mockUuid } from './random'
import { mockState } from './state'

/**
 * Hub WireGuard simulado (platform.yaml `/platform/wireguard/hubs*`): lectura, edición
 * (`PATCH` con `If-Match`) y rangos de túnel (`POST`/`DELETE .../pools`) con las mismas reglas
 * que el backend. Los cambios se guardan en el storage de la sesión simulada
 * (`sessionStorage['horus.mock.wireguard']`) y sobreviven a recargar.
 *
 * Interruptores (`localStorage`):
 * - `horus.mock.wgHubConflict = '1'`: el primer `PATCH` de cada hub responde `412`, como si
 *   otra persona hubiera cambiado antes el keepalive (su versión sube).
 * - `horus.mock.wgUsage = '0.85'`: ocupación del primer rango (proporción 0–1) para ver el
 *   aviso de ocupación alta.
 * - `horus.mock.wgQuarantine = '1'`: los rangos vacíos tienen direcciones liberadas en
 *   cuarentena (24 h), así que quitarlos responde `409 WIREGUARD_POOL_IN_USE`.
 */

export interface StoredPool {
  id: string
  cidr: string
  created_at: string
}

export interface WireguardHubState {
  version: number
  name: string
  endpoint: string | null
  mtu: number
  persistent_keepalive_seconds: number
  pools: StoredPool[]
  /** El 412 simulado (`horus.mock.wgHubConflict`) ya se sirvió. */
  conflictServed?: boolean
}

const HUB_ID = mockUuid('0192e999', 1)
const SEED_POOL_ID = mockUuid('0192e99a', 1)
const SERVICES_CIDR = '10.254.0.0/24'
const STORAGE_KEY = 'horus.mock.wireguard'

type Storage = Pick<globalThis.Storage, 'getItem' | 'setItem'> | null | undefined
let storage: Storage

/** Recupera los cambios del hub hechos en esta sesión simulada (al recargar). */
export function restoreWireguard(s: Storage) {
  storage = s
  try {
    const saved = JSON.parse(s?.getItem(STORAGE_KEY) ?? '{}') as Record<string, WireguardHubState>
    for (const [id, hub] of Object.entries(saved)) {
      if (hub && Array.isArray(hub.pools) && !mockState.wireguard.has(id)) {
        mockState.wireguard.set(id, hub)
      }
    }
  } catch {
    // estado guardado ilegible: se ignora
  }
}

function save() {
  try {
    storage?.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(mockState.wireguard)))
  } catch {
    // sin storage: solo en memoria
  }
}

function readLocal(key: string) {
  try {
    return globalThis.localStorage?.getItem(key) ?? null
  } catch {
    return null
  }
}

function usageOverride() {
  const v = Number(readLocal('horus.mock.wgUsage'))
  return readLocal('horus.mock.wgUsage') !== null && Number.isFinite(v) && v >= 0 && v <= 1
    ? v
    : null
}

function stateOf(id: string): WireguardHubState | undefined {
  if (id !== HUB_ID) return undefined
  let s = mockState.wireguard.get(id)
  if (!s) {
    s = {
      version: 1,
      name: 'hub-principal',
      // `null`: el endpoint de la instalación (HORUS_WG_ENDPOINT).
      endpoint: null,
      mtu: 1420,
      persistent_keepalive_seconds: 25,
      pools: [{ id: SEED_POOL_ID, cidr: '10.255.0.0/16', created_at: '2026-09-01T10:00:00.000Z' }],
    }
    mockState.wireguard.set(id, s)
  }
  return s
}

function pools(s: WireguardHubState, now: Date): WireguardPool[] {
  const peers = ALL_TENANTS.flatMap((t) => routersOf(t, now)).filter((r) => r.router.tunnel_address)
  const usage = usageOverride()
  return s.pools.map((p, i) => {
    const total = cidrSize(p.cidr)
    // Los peers sintéticos tienen sus direcciones en el primer rango; el hub ocupa una más.
    const used = i === 0 ? (usage === null ? peers.length + 1 : Math.round(total * usage)) : 0
    return {
      id: p.id,
      cidr: p.cidr,
      addresses_total: total,
      addresses_used: used,
      created_at: p.created_at,
    }
  })
}

function present(
  id: string,
  s: WireguardHubState,
  now: Date,
  defaultEndpoint: string,
): WireguardHub {
  const peers = ALL_TENANTS.flatMap((t) => routersOf(t, now)).filter((r) => r.router.tunnel_address)
  const list = pools(s, now)
  return {
    id,
    name: s.name,
    endpoint: s.endpoint ?? defaultEndpoint,
    listen_port: 51820,
    public_key: 'HoRuSHubPublicKeyExampleBase64xxxxxxxxxxxxx=',
    tunnel_cidr: list[0]!.cidr,
    services_cidr: SERVICES_CIDR,
    addresses_total: list.reduce((a, p) => a + p.addresses_total, 0),
    addresses_used: list.reduce((a, p) => a + p.addresses_used, 0),
    peers_active: peers.filter((r) => r.peer.handshake_state === 'ok').length,
    tenants: ALL_TENANTS.length,
    status: 'up',
    version: s.version,
    mtu: s.mtu,
    persistent_keepalive_seconds: s.persistent_keepalive_seconds,
    pools: list,
  }
}

export function wireguardHubs(now: Date, defaultEndpoint: string): WireguardHub[] {
  return [present(HUB_ID, stateOf(HUB_ID)!, now, defaultEndpoint)]
}

export interface WireguardRouteContext {
  req: Request
  path: string
  method: string
  body: Record<string, unknown>
  can: (permission: string) => boolean
  now: Date
  reauthRequired?: boolean
  defaultEndpoint: string
}

function invalid(code: string, errors: { field: string; code: string; message: string }[]) {
  return problem(422, code, 'Revisa los campos marcados', {}, { errors })
}

const HOSTNAME =
  /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/i
const IPV4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/

function endpointOk(value: string) {
  const v4 = value.match(IPV4)
  if (v4) return v4.slice(1).every((n) => Number(n) <= 255)
  if (value.includes(':')) return /^[0-9a-f:]+$/i.test(value) && value.split('::').length <= 2
  return HOSTNAME.test(value)
}

const POOL_MESSAGES: Record<PoolCidrError, string> = {
  required: 'Escribe un rango en notación CIDR',
  syntax: 'No es un rango CIDR válido',
  ipv6: 'Los túneles de los routers usan direcciones IPv4',
  prefix: 'Entre /16 y /28 (IPv4) o /64 y /120 (IPv6)',
  network: 'Escribe la dirección de red del rango',
  cgnat: '100.64.0.0/10 (CGNAT) no puede usarse para túneles',
  overlapPool: 'Se solapa con otro rango del hub',
  overlapServices: 'Se solapa con el rango de servicios',
}

function updateHub(ctx: WireguardRouteContext, id: string, s: WireguardHubState) {
  const version = ifMatch(ctx.req)
  if (version === null) return problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')
  if (readLocal('horus.mock.wgHubConflict') === '1' && !s.conflictServed) {
    // Otra persona editó el hub justo antes.
    s.conflictServed = true
    s.version += 1
    s.persistent_keepalive_seconds = 30
    save()
  }
  if (version !== s.version) {
    return problem(
      412,
      'PRECONDITION_FAILED',
      'El hub cambió',
      {},
      {
        current: present(id, s, ctx.now, ctx.defaultEndpoint) as unknown as Record<string, unknown>,
      },
    )
  }
  const body = ctx.body
  const allowed = ['name', 'endpoint', 'mtu', 'persistent_keepalive_seconds']
  const keys = Object.keys(body)
  const unknown = keys.find((k) => !allowed.includes(k))
  if (!keys.length || unknown) {
    return invalid('VALIDATION_FAILED', [
      { field: unknown ?? '_', code: 'INVALID', message: 'Campo no editable' },
    ])
  }
  const errors: { field: string; code: string; message: string }[] = []
  const next = { ...s }
  if ('name' in body) {
    const name = typeof body.name === 'string' ? body.name.trim() : ''
    if (!name || name.length > 64) {
      errors.push({ field: 'name', code: 'INVALID', message: 'Entre 1 y 64 caracteres' })
    } else next.name = name
  }
  if ('endpoint' in body) {
    const endpoint = typeof body.endpoint === 'string' ? body.endpoint.trim() : ''
    if (!endpoint || !endpointOk(endpoint)) {
      errors.push({
        field: 'endpoint',
        code: 'INVALID',
        message: 'Escribe un nombre de host o una IP pública, sin https:// ni puerto',
      })
    } else next.endpoint = endpoint
  }
  if ('mtu' in body) {
    const mtu = body.mtu
    if (typeof mtu !== 'number' || !Number.isInteger(mtu) || mtu < 1280 || mtu > 1500) {
      errors.push({ field: 'mtu', code: 'INVALID', message: 'Entre 1280 y 1500' })
    } else next.mtu = mtu
  }
  if ('persistent_keepalive_seconds' in body) {
    const k = body.persistent_keepalive_seconds
    if (typeof k !== 'number' || !Number.isInteger(k) || k < 0 || k > 120) {
      errors.push({
        field: 'persistent_keepalive_seconds',
        code: 'INVALID',
        message: 'Entre 0 y 120 segundos',
      })
    } else next.persistent_keepalive_seconds = k
  }
  if (errors.length) return invalid('VALIDATION_FAILED', errors)
  Object.assign(s, next, { version: s.version + 1 })
  save()
  const hub = present(id, s, ctx.now, ctx.defaultEndpoint)
  return json(hub, 200, { ETag: `"${s.version}"` })
}

function addPool(ctx: WireguardRouteContext, id: string, s: WireguardHubState) {
  const raw = typeof ctx.body.cidr === 'string' ? ctx.body.cidr : ''
  const check = checkPoolCidr(raw, { pools: s.pools.map((p) => p.cidr), services: SERVICES_CIDR })
  if (!check.ok) {
    const error = {
      field: 'cidr',
      code: check.error.toUpperCase(),
      message: POOL_MESSAGES[check.error],
    }
    if (check.error === 'overlapPool' || check.error === 'overlapServices') {
      return problem(
        409,
        'WIREGUARD_POOL_OVERLAP',
        error.message,
        {},
        { errors: [error], detail: check.detail },
      )
    }
    return invalid('WIREGUARD_POOL_INVALID', [error])
  }
  const pool: StoredPool = {
    id: mockUuid('0192e99a', hash(check.cidr)),
    cidr: check.cidr,
    created_at: ctx.now.toISOString(),
  }
  s.pools.push(pool)
  s.version += 1
  save()
  return json(
    pools(s, ctx.now).find((p) => p.id === pool.id),
    201,
  )
}

function deletePool(ctx: WireguardRouteContext, s: WireguardHubState, poolId: string) {
  const index = s.pools.findIndex((p) => p.id === poolId)
  if (index < 0) return notFound('WIREGUARD_POOL_NOT_FOUND')
  const pool = pools(s, ctx.now)[index]!
  if (s.pools.length === 1) {
    return problem(409, 'WIREGUARD_POOL_IN_USE', 'Es el único rango del hub: no se puede quitar')
  }
  if (pool.addresses_used > 0) {
    return problem(
      409,
      'WIREGUARD_POOL_IN_USE',
      `El rango tiene ${pool.addresses_used} direcciones asignadas`,
    )
  }
  if (readLocal('horus.mock.wgQuarantine') === '1') {
    return problem(
      409,
      'WIREGUARD_POOL_IN_USE',
      'El rango tiene direcciones liberadas en cuarentena (24 h)',
    )
  }
  s.pools.splice(index, 1)
  s.version += 1
  save()
  return noContent()
}

/** `PATCH /platform/wireguard/hubs/{id}`, `POST .../pools`, `DELETE .../pools/{pool_id}`. */
export function wireguardRoute(ctx: WireguardRouteContext): Response | undefined {
  const m = ctx.path.match(/^\/platform\/wireguard\/hubs\/([^/]+)(?:\/pools(?:\/([^/]+))?)?$/)
  if (!m) return
  const isPools = ctx.path.includes('/pools')
  const poolId = m[2]
  const route =
    !isPools && ctx.method === 'PATCH'
      ? 'patch'
      : isPools && !poolId && ctx.method === 'POST'
        ? 'add'
        : isPools && poolId && ctx.method === 'DELETE'
          ? 'delete'
          : null
  if (!route) return
  if (!ctx.can('platform.wireguard.manage')) return forbidden()
  const s = stateOf(m[1]!)
  if (!s) return notFound('WIREGUARD_HUB_NOT_FOUND')
  if (ctx.reauthRequired) {
    return problem(403, 'REAUTH_REQUIRED', 'Vuelve a identificarte para administrar el hub')
  }
  if (route === 'patch') return updateHub(ctx, m[1]!, s)
  if (route === 'add') return addPool(ctx, m[1]!, s)
  return deletePool(ctx, s, poolId!)
}
