import type { Kiosk, KioskConfig, KioskInput } from '~~/types/api'
import { ALL_TENANTS, type MockTenant } from './data'
import { forbidden, json, notFound, problem } from './http'
import { hash, mockUuid } from './random'

/**
 * Pantallas NOC (kioscos) simuladas (I1-21, api.md §2.12). El kiosco es un dispositivo, no un
 * usuario: el código de 8 caracteres (un uso, 10 min) se canjea por una credencial de
 * dispositivo que el gateway pondría en una cookie HttpOnly. Aquí esa "cookie", los kioscos y
 * los códigos se guardan en `localStorage` SOLO dentro de `mocks/`, para que sobrevivan a la
 * recarga de `/kiosk` (la TV y la consola del admin pueden ser la misma pestaña en una demo).
 */

const STORE_KEY = 'horus.mock.kiosks'
const DEVICE_KEY = 'horus.mock.kioskDevice'
const ALPHABET = 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789'
const CODE_TTL = 10 * 60_000
const MAX_FAILURES = 10

interface Store {
  kiosks: Kiosk[]
  codes: { code: string; kioskId: string; expiresAt: number }[]
  failures: number
}

function storage() {
  try {
    return typeof window === 'undefined' ? null : window.localStorage
  } catch {
    return null
  }
}

let memory: Store | null = null

function seed(now: Date): Store {
  const t = now.getTime()
  const tenant = ALL_TENANTS[0]!
  const base = {
    tenant_id: tenant.tenant_id,
    created_at: '2026-09-15T13:00:00.000Z',
    updated_at: '2026-09-15T13:00:00.000Z',
    version: 1,
    expires_at: new Date(t + 150 * 86_400_000).toISOString(),
    playlist_id: mockUuid('0194a000', 1),
    dashboard_ids: [],
  }
  return {
    failures: 0,
    codes: [],
    kiosks: [
      {
        ...base,
        id: mockUuid('0194d000', 1),
        name: 'TV sala NOC',
        status: 'active',
        allowed_cidrs: ['10.20.255.0/24'],
        cidr_risk: false,
        show_personal_data: false,
        critical_finding_banner: true,
        last_seen_at: new Date(t - 12_000).toISOString(),
        last_ip: '10.20.255.40',
        frontend_version: '1.0.0',
      },
      {
        ...base,
        id: mockUuid('0194d000', 2),
        name: 'Pantalla de recepción',
        status: 'pending_enrollment',
        allowed_cidrs: [],
        cidr_risk: true,
        show_personal_data: false,
        critical_finding_banner: false,
        last_seen_at: null,
        last_ip: null,
        frontend_version: null,
      },
    ],
  }
}

function load(now: Date): Store {
  const raw = storage()?.getItem(STORE_KEY)
  if (raw) {
    try {
      return JSON.parse(raw) as Store
    } catch {
      // se regenera
    }
  }
  memory ??= seed(now)
  return memory
}

function save(store: Store) {
  memory = store
  storage()?.setItem(STORE_KEY, JSON.stringify(store))
}

export function kiosksOf(tenant: MockTenant, now: Date) {
  return load(now).kiosks.filter((k) => k.tenant_id === tenant.tenant_id)
}

function device(): { kioskId: string } | null {
  try {
    const raw = storage()?.getItem(DEVICE_KEY)
    return raw ? (JSON.parse(raw) as { kioskId: string }) : null
  } catch {
    return null
  }
}

/** Kiosco de esta "TV" si su credencial sigue siendo válida. */
export function currentKiosk(now: Date): Kiosk | null {
  const d = device()
  if (!d) return null
  const kiosk = load(now).kiosks.find((k) => k.id === d.kioskId)
  if (!kiosk || kiosk.status !== 'active' || new Date(kiosk.expires_at).getTime() < now.getTime()) {
    return null
  }
  return kiosk
}

export function touchKiosk(kioskId: string, now: Date) {
  const store = load(now)
  const kiosk = store.kiosks.find((k) => k.id === kioskId)
  if (!kiosk) return
  kiosk.last_seen_at = now.toISOString()
  kiosk.last_ip = '10.20.255.41'
  kiosk.frontend_version = '1.0.0'
  save(store)
}

export function kioskConfig(kiosk: Kiosk): KioskConfig {
  const tenant = ALL_TENANTS.find((t) => t.tenant_id === kiosk.tenant_id)
  let version: string | null
  try {
    version = storage()?.getItem('horus.mock.frontendVersion') ?? null
  } catch {
    version = null
  }
  const ids = kiosk.dashboard_ids.length
    ? kiosk.dashboard_ids
    : ['0192f000-0000-7000-8000-00000000d001', '0192f000-0000-7000-8000-00000000d002']
  return {
    kiosk_id: kiosk.id,
    tenant_id: kiosk.tenant_id,
    tenant_name: tenant?.tenant_name,
    playlist_id: kiosk.playlist_id,
    items: ids.map((dashboard_id) => ({ dashboard_id, duration_seconds: 30 })),
    transition: 'fade',
    show_personal_data: kiosk.show_personal_data,
    critical_finding_banner: kiosk.critical_finding_banner ?? false,
    frontend_min_version: version,
  }
}

/** `POST /kiosk/enroll`: canjea el código y deja la credencial del dispositivo. */
export function enroll(req: Request, body: Record<string, unknown>, now: Date) {
  if (req.headers.get('X-Requested-With') !== 'horus') {
    return problem(403, 'ORIGIN_NOT_ALLOWED', 'Origen no permitido')
  }
  const store = load(now)
  const code = String(body.code ?? '')
    .toUpperCase()
    .replace(/[^A-Z0-9]/g, '')
  const entry = store.codes.find((c) => c.code === code && c.expiresAt > now.getTime())
  if (!entry) {
    store.failures++
    if (store.failures >= MAX_FAILURES) store.codes = []
    save(store)
    return problem(
      422,
      'KIOSK_ENROLLMENT_CODE_INVALID',
      'Código no válido o caducado',
      {},
      {
        errors: [{ field: 'code', code: 'INVALID', message: 'Código no válido o caducado' }],
      },
    )
  }
  store.codes = store.codes.filter((c) => c !== entry)
  store.failures = 0
  const kiosk = store.kiosks.find((k) => k.id === entry.kioskId)!
  kiosk.status = 'active'
  kiosk.last_seen_at = now.toISOString()
  save(store)
  storage()?.setItem(DEVICE_KEY, JSON.stringify({ kioskId: kiosk.id }))
  return new Response(null, { status: 204 })
}

export interface KioskAdminContext {
  req: Request
  path: string
  method: string
  body: Record<string, unknown>
  tenant: MockTenant
  can: (permission: string) => boolean
  now: Date
}

export function kioskAdminRoute(ctx: KioskAdminContext): Response | undefined {
  if (!ctx.path.startsWith('/kiosks')) return
  if (!ctx.can('kiosks.manage')) return forbidden()
  const store = load(ctx.now)
  const mine = store.kiosks.filter((k) => k.tenant_id === ctx.tenant.tenant_id)

  if (ctx.path === '/kiosks' && ctx.method === 'GET') {
    return json({
      data: mine,
      page: { limit: 50, has_more: false, next_cursor: null, prev_cursor: null },
    })
  }
  if (ctx.path === '/kiosks' && ctx.method === 'POST') {
    const input = ctx.body as KioskInput
    if (!input.name?.trim()) {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Revisa los campos marcados',
        {},
        {
          errors: [
            { field: 'name', code: 'REQUIRED', message: 'Escribe un nombre para la pantalla' },
          ],
        },
      )
    }
    if (input.show_personal_data && !input.show_personal_data_reason?.trim()) {
      return problem(
        422,
        'VALIDATION_FAILED',
        'Revisa los campos marcados',
        {},
        {
          errors: [
            {
              field: 'show_personal_data_reason',
              code: 'REQUIRED',
              message: 'Explica por qué esta pantalla debe mostrar datos de clientes',
            },
          ],
        },
      )
    }
    const now = ctx.now.toISOString()
    const kiosk: Kiosk = {
      id: mockUuid('0194d100', hash(input.name + now)),
      tenant_id: ctx.tenant.tenant_id,
      created_at: now,
      updated_at: now,
      version: 1,
      name: input.name.trim(),
      status: 'pending_enrollment',
      allowed_cidrs: input.allowed_cidrs ?? [],
      cidr_risk: !input.allowed_cidrs?.length,
      dashboard_ids: input.dashboard_ids ?? [],
      playlist_id: input.playlist_id ?? null,
      show_personal_data: input.show_personal_data ?? false,
      critical_finding_banner: input.critical_finding_banner ?? false,
      expires_at: input.expires_at ?? new Date(ctx.now.getTime() + 180 * 86_400_000).toISOString(),
      last_seen_at: null,
      last_ip: null,
      frontend_version: null,
    }
    store.kiosks.push(kiosk)
    save(store)
    return json(kiosk, 201, { ETag: '"1"' })
  }
  const m = ctx.path.match(/^\/kiosks\/([^/]+)(?:\/(enrollment-codes|revoke))?$/)
  const kiosk = m && mine.find((k) => k.id === m[1])
  if (!m || !kiosk) return notFound()
  if (!m[2] && ctx.method === 'GET') return json(kiosk, 200, { ETag: `"${kiosk.version}"` })
  if (m[2] === 'enrollment-codes' && ctx.method === 'POST') {
    if (!ctx.req.headers.get('Idempotency-Key')) {
      return problem(428, 'PRECONDITION_REQUIRED', 'Falta Idempotency-Key')
    }
    const bytes = new Uint8Array(8)
    crypto.getRandomValues(bytes)
    const code = Array.from(bytes, (b) => ALPHABET[b % ALPHABET.length]).join('')
    const expiresAt = ctx.now.getTime() + CODE_TTL
    // Un código nuevo invalida el anterior del mismo kiosco.
    store.codes = [
      ...store.codes.filter((c) => c.kioskId !== kiosk.id),
      { code, kioskId: kiosk.id, expiresAt },
    ]
    if (kiosk.status !== 'active') kiosk.status = 'pending_enrollment'
    save(store)
    return json(
      {
        code,
        expires_at: new Date(expiresAt).toISOString(),
        // El código va en el fragmento: no viaja al servidor ni a los logs (frontend.md §7.2).
        qr_url: `${globalThis.location?.origin ?? 'https://horus.example'}/kiosk#code=${code}`,
      },
      201,
      { 'Cache-Control': 'no-store' },
    )
  }
  if (m[2] === 'revoke' && ctx.method === 'POST') {
    kiosk.status = 'revoked'
    kiosk.version++
    store.codes = store.codes.filter((c) => c.kioskId !== kiosk.id)
    save(store)
    return json(kiosk)
  }
  if (!m[2] && ctx.method === 'PATCH') {
    Object.assign(kiosk, ctx.body, {
      version: kiosk.version + 1,
      updated_at: ctx.now.toISOString(),
    })
    kiosk.cidr_risk = !kiosk.allowed_cidrs.length
    save(store)
    return json(kiosk, 200, { ETag: `"${kiosk.version}"` })
  }
}
