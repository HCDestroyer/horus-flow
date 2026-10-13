/**
 * Mock de Administración › ASN propios (devices.yaml `/asn-rules`) y de la consulta
 * `GET /asn/{asn}` (analytics.yaml): reglas en memoria por ISP con las mismas validaciones
 * que el servicio (ASN 1–4294967295, uno por ASN, categoría/servicio del catálogo, categoría del
 * servicio si se elige uno) y `If-Match` al editar o borrar. Los tops por ASN y organización
 * (`tenant-routes.ts`) usan el nombre de la regla.
 */
import type { AsnRule } from '~~/types/api'
import { REMOTE_ASNS } from './base'
import { forbidden, ifMatch, json, noContent, notFound, problem } from './http'

interface Ctx {
  req: Request
  method: string
  path: string
  body: Record<string, unknown>
  tenant: { tenant_id: string }
  can: (permission: string) => boolean
  now: Date
}

/** Espejo de services/traffic/api/catalog/seed.yaml (categorías y servicios). */
export const CATALOG_CATEGORIES = [
  { slug: 'video_streaming', name: 'Streaming de video' },
  { slug: 'social', name: 'Redes sociales' },
  { slug: 'gaming', name: 'Videojuegos' },
  { slug: 'software_updates', name: 'Actualizaciones de software' },
  { slug: 'web', name: 'Navegación web' },
  { slug: 'messaging', name: 'Mensajería' },
  { slug: 'cloud', name: 'Nube y almacenamiento' },
  { slug: 'video_calls', name: 'Videollamadas' },
  { slug: 'music', name: 'Música' },
  { slug: 'ecommerce', name: 'Comercio electrónico' },
  { slug: 'cdn_infra', name: 'CDN e infraestructura' },
  { slug: 'network_services', name: 'Servicios de red (DNS, NTP)' },
]

export const CATALOG_SERVICES = [
  { slug: 'google_generic', name: 'Google (genérico)', category: 'cdn_infra' },
  { slug: 'youtube', name: 'YouTube', category: 'video_streaming' },
  { slug: 'google_dns', name: 'Google Public DNS', category: 'network_services' },
  { slug: 'meta_generic', name: 'Meta (genérico)', category: 'social' },
  { slug: 'whatsapp', name: 'WhatsApp', category: 'messaging' },
  { slug: 'netflix', name: 'Netflix', category: 'video_streaming' },
  { slug: 'cloudflare_generic', name: 'Cloudflare (genérico)', category: 'cdn_infra' },
  { slug: 'akamai_generic', name: 'Akamai (genérico)', category: 'cdn_infra' },
  { slug: 'amazon_cloudfront', name: 'Amazon CloudFront', category: 'cdn_infra' },
  { slug: 'microsoft_365', name: 'Microsoft 365', category: 'web' },
  { slug: 'windows_update', name: 'Windows Update', category: 'software_updates' },
  { slug: 'zoom', name: 'Zoom', category: 'video_calls' },
  { slug: 'spotify', name: 'Spotify', category: 'music' },
  { slug: 'tiktok', name: 'TikTok', category: 'social' },
  { slug: 'twitch', name: 'Twitch', category: 'video_streaming' },
  { slug: 'steam', name: 'Steam', category: 'gaming' },
  { slug: 'discord', name: 'Discord', category: 'messaging' },
  { slug: 'telegram', name: 'Telegram', category: 'messaging' },
  { slug: 'mercadolibre', name: 'Mercado Libre', category: 'ecommerce' },
  { slug: 'speedtest_ookla', name: 'Speedtest (Ookla)', category: 'network_services' },
  { slug: 'web_https', name: 'Web (HTTPS)', category: 'web' },
].sort((a, b) => a.name.localeCompare(b.name, 'es'))

/** Organizaciones del catálogo por ASN (las de `REMOTE_ASNS` con `org`). */
const CATALOG_ORGS: Record<number, { slug: string; name: string }> = {
  15169: { slug: 'google', name: 'Google' },
  36040: { slug: 'google', name: 'Google' },
  2906: { slug: 'netflix', name: 'Netflix' },
  32934: { slug: 'meta', name: 'Meta' },
  20940: { slug: 'akamai', name: 'Akamai' },
  16509: { slug: 'amazon', name: 'Amazon' },
  14618: { slug: 'amazon', name: 'Amazon' },
  13335: { slug: 'cloudflare', name: 'Cloudflare' },
  8075: { slug: 'microsoft', name: 'Microsoft' },
  396986: { slug: 'bytedance', name: 'ByteDance (TikTok)' },
  32590: { slug: 'valve', name: 'Valve (Steam)' },
}

const rules = new Map<string, AsnRule[]>()

// Las reglas se guardan en el storage de la sesión simulada (sobreviven a una recarga).
const STORAGE_KEY = 'horus.mock.asn-rules'
type Storage = Pick<globalThis.Storage, 'getItem' | 'setItem'> | null | undefined
let storage: Storage

/** Recupera las reglas guardadas en esta sesión simulada (al recargar). */
export function restoreAsnRules(s: Storage) {
  storage = s
  try {
    const saved = JSON.parse(s?.getItem(STORAGE_KEY) ?? '{}') as Record<string, AsnRule[]>
    for (const [tenantId, list] of Object.entries(saved)) {
      if (Array.isArray(list) && !rules.has(tenantId)) rules.set(tenantId, list)
    }
  } catch {
    // estado guardado ilegible: se ignora
  }
}

function save() {
  try {
    storage?.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(rules)))
  } catch {
    // sin storage: solo en memoria
  }
}

/** Reglas ASN del ISP (vacías al arrancar). */
export function asnRulesOf(tenantId: string) {
  let list = rules.get(tenantId)
  if (!list) {
    list = []
    rules.set(tenantId, list)
  }
  return list
}

const etag = (version: number) => ({ ETag: `"${version}"` })

function invalid(errors: { field: string; code: string; message: string }[]) {
  return problem(422, 'VALIDATION_FAILED', 'Revisa los campos marcados', {}, { errors })
}

/** Valida y aplica un alta o un merge patch; devuelve los errores de campo. */
function apply(rule: AsnRule, body: Record<string, unknown>, create: boolean, list: AsnRule[]) {
  const errors: { field: string; code: string; message: string }[] = []
  const add = (field: string, code: string, message: string) =>
    errors.push({ field, code, message })
  if (create) {
    const asn = Number(body.asn)
    if (body.asn === undefined || body.asn === null)
      add('asn', 'REQUIRED', 'Indica el número de ASN.')
    else if (!Number.isInteger(asn) || asn < 1 || asn > 4294967295)
      add('asn', 'OUT_OF_RANGE', 'El ASN es un número entre 1 y 4294967295.')
    else if (list.some((r) => r.asn === asn))
      add('asn', 'ALREADY_EXISTS', 'Ya hay una regla para este ASN: edítala en la lista.')
    else rule.asn = asn
    if (!('name' in body)) add('name', 'REQUIRED', 'Campo obligatorio.')
    if (!('category' in body) && !body.service)
      add('category', 'REQUIRED', 'Elige una categoría (o un servicio del catálogo).')
  } else if ('asn' in body) add('asn', 'UNKNOWN_FIELD', 'Campo no permitido.')
  if ('name' in body) {
    const name = typeof body.name === 'string' ? body.name.trim() : ''
    if (!name) add('name', 'REQUIRED', 'El nombre no puede quedar vacío.')
    else if (name.length > 80) add('name', 'TOO_LONG', 'Máximo 80 caracteres.')
    else rule.name = name
  }
  if ('category' in body) {
    if (!CATALOG_CATEGORIES.some((c) => c.slug === body.category))
      add('category', 'INVALID_VALUE', 'Categoría desconocida en el catálogo.')
    else rule.category = String(body.category)
  }
  if ('service' in body) {
    if (!body.service) rule.service = null
    else if (!CATALOG_SERVICES.some((s) => s.slug === body.service))
      add('service', 'INVALID_VALUE', 'Servicio desconocido en el catálogo.')
    else rule.service = String(body.service)
  }
  const svc = CATALOG_SERVICES.find((s) => s.slug === rule.service)
  if (svc) {
    if (
      'category' in body &&
      body.category !== svc.category &&
      !errors.some((e) => e.field === 'category')
    )
      add(
        'category',
        'INVALID_VALUE',
        `Con un servicio del catálogo la categoría es la del servicio (${svc.category}).`,
      )
    rule.category = svc.category
  }
  if ('note' in body) {
    const note = typeof body.note === 'string' ? body.note.trim() : ''
    rule.note = note || null
  }
  return errors
}

export function asnRulesRoute(ctx: Ctx): Response | undefined {
  if (ctx.path === '/asn-rules/catalog' && ctx.method === 'GET') {
    if (!ctx.can('settings.read')) return forbidden()
    return json({ categories: CATALOG_CATEGORIES, services: CATALOG_SERVICES })
  }
  if (ctx.path === '/asn-rules') {
    const list = asnRulesOf(ctx.tenant.tenant_id)
    if (ctx.method === 'GET') {
      if (!ctx.can('settings.read')) return forbidden()
      return json({ data: [...list].sort((a, b) => a.asn - b.asn) })
    }
    if (ctx.method === 'POST') {
      if (!ctx.can('settings.manage')) return forbidden()
      const now = ctx.now.toISOString()
      const rule: AsnRule = {
        id: crypto.randomUUID(),
        tenant_id: ctx.tenant.tenant_id,
        version: 1,
        created_at: now,
        updated_at: now,
        asn: 0,
        name: '',
        category: '',
        service: null,
        note: null,
      }
      const errors = apply(rule, ctx.body, true, list)
      if (errors.length) return invalid(errors)
      list.push(rule)
      save()
      return json(rule, 201, { ...etag(1), Location: `/api/v1/asn-rules/${rule.id}` })
    }
  }
  const one = ctx.path.match(/^\/asn-rules\/([^/]+)$/)
  if (one && (ctx.method === 'PATCH' || ctx.method === 'DELETE')) {
    if (!ctx.can('settings.manage')) return forbidden()
    const list = asnRulesOf(ctx.tenant.tenant_id)
    const rule = list.find((r) => r.id === one[1])
    if (!rule) return notFound('ASN_RULE_NOT_FOUND')
    const expected = ifMatch(ctx.req)
    if (expected === null) return problem(428, 'PRECONDITION_REQUIRED', 'Falta If-Match')
    if (expected !== rule.version)
      return problem(
        412,
        'PRECONDITION_FAILED',
        'El recurso cambió mientras lo editabas',
        {},
        {
          current: rule as unknown as Record<string, unknown>,
        },
      )
    if (ctx.method === 'DELETE') {
      list.splice(list.indexOf(rule), 1)
      save()
      return noContent()
    }
    const draft = { ...rule }
    const errors = apply(draft, ctx.body, false, list)
    if (errors.length) return invalid(errors)
    Object.assign(rule, draft, { version: rule.version + 1, updated_at: ctx.now.toISOString() })
    save()
    return json(rule, 200, etag(rule.version))
  }
  const lookup = ctx.path.match(/^\/asn\/(\d+)$/)
  if (lookup && ctx.method === 'GET') {
    if (!ctx.can('traffic.read')) return forbidden()
    const asn = Number(lookup[1])
    if (!asn || asn > 4294967295) return notFound()
    const known = REMOTE_ASNS.find((a) => a.asn === asn)
    const rule = asnRulesOf(ctx.tenant.tenant_id).find((r) => r.asn === asn)
    return json({
      asn,
      name: known?.name ?? null,
      country: known?.name ? 'US' : null,
      catalog_organization: CATALOG_ORGS[asn] ?? null,
      tenant_rule: rule
        ? { name: rule.name, category: rule.category, service: rule.service ?? null }
        : null,
    })
  }
}
