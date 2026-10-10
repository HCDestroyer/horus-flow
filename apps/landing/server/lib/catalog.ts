// Catálogo de planes y precios, ajustes del sitio y semilla inicial. Las tablas normalizadas
// (plans, plan_prices, plan_features) son el estado publicado; cada publicación guarda además
// una instantánea en pricing_versions para el historial y "restaurar".
import { pricing as seedPricing } from '../../app/config/pricing'
import {
  CURRENCIES,
  PERIODS,
  catalogSchema,
  type Catalog,
  type CatalogPlan,
  type Currency,
  type Localized,
  type Period,
  type PlanId,
  type SiteSettings,
} from '../../shared/catalog'
import { nowIso, type DB } from './db'

const toMinor = (n: number) => Math.round(n * 100)
const fromMinor = (n: number) => n / 100

// ---- Ajustes (clave → JSON) ----------------------------------------------------------------

export const DEFAULT_SETTINGS: SiteSettings = {
  contact: { email: 'info@kns.gt', phone: '', whatsapp: '' },
  support: {
    text: {
      es: 'Soporte técnico 24/7 en todos los planes.',
      en: '24/7 technical support on every plan.',
    },
    responseTime: { es: '', en: '' },
  },
  banner: { enabled: false, text: { es: '', en: '' }, url: '' },
}

export function getSetting<T>(db: DB, key: string, fallback: T): T {
  const row = db.prepare('SELECT value FROM settings WHERE key = ?').get(key) as
    { value: string } | undefined
  if (!row) return fallback
  try {
    return JSON.parse(row.value) as T
  } catch {
    return fallback
  }
}

export function putSetting(db: DB, key: string, value: unknown, now = Date.now()): void {
  db.prepare(
    `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
     ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
  ).run(key, JSON.stringify(value), nowIso(now))
}

export function readSiteSettings(db: DB): SiteSettings {
  return {
    contact: getSetting(db, 'contact', DEFAULT_SETTINGS.contact),
    support: getSetting(db, 'support', DEFAULT_SETTINGS.support),
    banner: getSetting(db, 'banner', DEFAULT_SETTINGS.banner),
  }
}

export function writeSiteSettings(db: DB, s: SiteSettings, now = Date.now()): void {
  db.transaction(() => {
    putSetting(db, 'contact', s.contact, now)
    putSetting(db, 'support', s.support, now)
    putSetting(db, 'banner', s.banner, now)
  })()
}

// ---- Catálogo ------------------------------------------------------------------------------

type PricingMeta = Omit<Catalog, 'plans'>

interface PlanRow {
  id: PlanId
  sort: number
  visible: number
  highlighted: number
  installer_size: CatalogPlan['installerSize']
  clients: number | null
  name_es: string
  name_en: string
  summary_es: string
  summary_en: string
  server_es: string | null
  server_en: string | null
}

export function readCatalog(db: DB): Catalog {
  const meta = getSetting<PricingMeta | null>(db, 'pricing', null)
  if (!meta) throw new Error('Catálogo sin sembrar')
  const rows = db.prepare('SELECT * FROM plans ORDER BY sort, id').all() as PlanRow[]
  const prices = db
    .prepare('SELECT plan_id, currency, period, amount_minor FROM plan_prices')
    .all() as { plan_id: string; currency: Currency; period: Period; amount_minor: number }[]
  const features = db
    .prepare('SELECT plan_id, text_es, text_en FROM plan_features ORDER BY plan_id, sort, id')
    .all() as { plan_id: string; text_es: string; text_en: string }[]

  const plans: CatalogPlan[] = rows.map((r) => {
    const own = prices.filter((p) => p.plan_id === r.id)
    let planPrices: CatalogPlan['prices'] = null
    if (own.length === CURRENCIES.length * PERIODS.length) {
      planPrices = { USD: { monthly: 0, annual: 0 }, GTQ: { monthly: 0, annual: 0 } }
      for (const p of own) planPrices[p.currency][p.period] = fromMinor(p.amount_minor)
    }
    return {
      id: r.id,
      name: { es: r.name_es, en: r.name_en },
      summary: { es: r.summary_es, en: r.summary_en },
      server: r.server_es !== null ? { es: r.server_es, en: r.server_en ?? '' } : null,
      includes: features
        .filter((f) => f.plan_id === r.id)
        .map((f) => ({ es: f.text_es, en: f.text_en })),
      installerSize: r.installer_size,
      clients: r.clients,
      prices: planPrices,
      highlighted: r.highlighted === 1,
      visible: r.visible === 1,
    }
  })
  return { ...meta, plans }
}

/** Sustituye el catálogo publicado (en una transacción). No valida: usar publishCatalog. */
function replaceCatalog(db: DB, c: Catalog, now: number): void {
  const { plans, ...meta } = c
  putSetting(db, 'pricing', meta, now)
  db.prepare('DELETE FROM plan_features').run()
  db.prepare('DELETE FROM plan_prices').run()
  db.prepare('DELETE FROM plans').run()
  const insPlan = db.prepare(
    `INSERT INTO plans (id, sort, visible, highlighted, installer_size, clients, name_es, name_en,
       summary_es, summary_en, server_es, server_en, updated_at)
     VALUES (@id, @sort, @visible, @highlighted, @installer_size, @clients, @name_es, @name_en,
       @summary_es, @summary_en, @server_es, @server_en, @updated_at)`,
  )
  const insPrice = db.prepare(
    'INSERT INTO plan_prices (plan_id, currency, period, amount_minor) VALUES (?, ?, ?, ?)',
  )
  const insFeature = db.prepare(
    'INSERT INTO plan_features (plan_id, sort, text_es, text_en) VALUES (?, ?, ?, ?)',
  )
  plans.forEach((p, i) => {
    insPlan.run({
      id: p.id,
      sort: i,
      visible: p.visible ? 1 : 0,
      highlighted: p.highlighted ? 1 : 0,
      installer_size: p.installerSize,
      clients: p.clients,
      name_es: p.name.es,
      name_en: p.name.en,
      summary_es: p.summary.es,
      summary_en: p.summary.en,
      server_es: p.server?.es ?? null,
      server_en: p.server?.en ?? null,
      updated_at: nowIso(now),
    })
    if (p.prices) {
      for (const cur of CURRENCIES) {
        for (const per of PERIODS) insPrice.run(p.id, cur, per, toMinor(p.prices[cur][per]))
      }
    }
    p.includes.forEach((f, j) => insFeature.run(p.id, j, f.es, f.en))
  })
}

export interface PublishActor {
  id: number | null
  email: string
}

/**
 * Valida y publica un catálogo: tablas normalizadas + instantánea en pricing_versions.
 * Devuelve el id de la versión y el catálogo anterior (para la auditoría).
 */
export function publishCatalog(
  db: DB,
  input: unknown,
  actor: PublishActor,
  opts: { note?: string; restoredFrom?: number; now?: number } = {},
): { version: number; before: Catalog | null; after: Catalog } {
  const parsed = catalogSchema.parse(input) as Catalog
  const now = opts.now ?? Date.now()
  return db.transaction(() => {
    let before: Catalog | null
    try {
      before = readCatalog(db)
    } catch {
      before = null
    }
    replaceCatalog(db, parsed, now)
    const after = readCatalog(db)
    const info = db
      .prepare(
        `INSERT INTO pricing_versions (created_at, admin_id, admin_email, note, restored_from, snapshot)
         VALUES (?, ?, ?, ?, ?, ?)`,
      )
      .run(
        nowIso(now),
        actor.id,
        actor.email,
        (opts.note ?? '').slice(0, 200),
        opts.restoredFrom ?? null,
        JSON.stringify(after),
      )
    return { version: Number(info.lastInsertRowid), before, after }
  })()
}

export interface PricingVersionSummary {
  id: number
  createdAt: string
  adminEmail: string | null
  note: string
  restoredFrom: number | null
}

export function listPricingVersions(db: DB, limit = 50): PricingVersionSummary[] {
  return (
    db
      .prepare(
        `SELECT id, created_at, admin_email, note, restored_from FROM pricing_versions
         ORDER BY id DESC LIMIT ?`,
      )
      .all(limit) as {
      id: number
      created_at: string
      admin_email: string | null
      note: string
      restored_from: number | null
    }[]
  ).map((r) => ({
    id: r.id,
    createdAt: r.created_at,
    adminEmail: r.admin_email,
    note: r.note,
    restoredFrom: r.restored_from,
  }))
}

export function getPricingVersion(db: DB, id: number): Catalog | null {
  const row = db.prepare('SELECT snapshot FROM pricing_versions WHERE id = ?').get(id) as
    { snapshot: string } | undefined
  return row ? (JSON.parse(row.snapshot) as Catalog) : null
}

// ---- Semilla -------------------------------------------------------------------------------

/** Catálogo inicial a partir de app/config/pricing.ts. */
export function seedCatalog(): Catalog {
  return {
    confirmed: seedPricing.confirmed,
    currencies: [...seedPricing.currencies],
    defaultCurrency: seedPricing.defaultCurrency,
    defaultPeriod: seedPricing.defaultPeriod,
    annualNote: { ...seedPricing.annualNote },
    allPlansInclude: seedPricing.allPlansInclude.map((x) => ({ ...x })),
    plans: seedPricing.plans.map((p) => ({
      id: p.id,
      name: { ...p.name },
      summary: { ...p.summary },
      server: p.server ? { ...p.server } : null,
      includes: p.includes.map((x: Localized) => ({ ...x })),
      installerSize: p.installerSize,
      clients: p.clients,
      prices: p.prices
        ? {
            USD: { ...p.prices.USD },
            GTQ: { ...p.prices.GTQ },
          }
        : null,
      highlighted: Boolean(p.highlighted),
      visible: true,
    })),
  }
}

/**
 * Primera arrancada: siembra el catálogo, los ajustes y los métodos de pago (desactivados salvo
 * la transferencia, que no necesita credenciales pero sí datos bancarios: también desactivada).
 * No toca nada si ya hay planes.
 */
export function seedIfEmpty(db: DB, now = Date.now()): boolean {
  const count = (db.prepare('SELECT COUNT(*) AS n FROM plans').get() as { n: number }).n
  if (count > 0) return false
  publishCatalog(
    db,
    seedCatalog(),
    { id: null, email: 'sistema' },
    { note: 'Semilla inicial (app/config/pricing.ts)', now },
  )
  db.transaction(() => {
    for (const [key, value] of Object.entries(DEFAULT_SETTINGS)) {
      db.prepare('INSERT OR IGNORE INTO settings (key, value, updated_at) VALUES (?, ?, ?)').run(
        key,
        JSON.stringify(value),
        nowIso(now),
      )
    }
    const ins = db.prepare(
      'INSERT OR IGNORE INTO payment_methods (id, enabled, config, updated_at) VALUES (?, 0, ?, ?)',
    )
    ins.run('paypal', JSON.stringify({ mode: 'sandbox', webhookId: '' }), nowIso(now))
    ins.run('neo', JSON.stringify({ links: {} }), nowIso(now))
    ins.run(
      'transfer',
      JSON.stringify({ accounts: [], instructions: { es: '', en: '' } }),
      nowIso(now),
    )
  })()
  return true
}
