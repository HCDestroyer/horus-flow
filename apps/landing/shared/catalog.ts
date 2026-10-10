// Tipos compartidos entre la landing, el panel y el servidor: catálogo de planes y precios,
// ajustes públicos del sitio y métodos de pago visibles. El servidor los lee de la base de
// datos (server/lib/catalog.ts) y la landing los recibe de GET /api/site.
import { z } from 'zod'

export const CURRENCIES = ['USD', 'GTQ'] as const
export const PERIODS = ['monthly', 'annual'] as const
export const PLAN_IDS = ['small', 'medium', 'large', 'enterprise'] as const

export type Currency = (typeof CURRENCIES)[number]
export type Period = (typeof PERIODS)[number]
export type PlanId = (typeof PLAN_IDS)[number]

export interface Localized {
  es: string
  en: string
}

export interface CatalogPlan {
  id: PlanId
  name: Localized
  summary: Localized
  /** Servidor recomendado; null = a medida. */
  server: Localized | null
  includes: Localized[]
  installerSize: 'small' | 'medium' | 'large' | null
  clients: number | null
  /** Importe por moneda y periodo (con hasta 2 decimales). null = a medida (sin compra web). */
  prices: Record<Currency, Record<Period, number>> | null
  highlighted: boolean
  visible: boolean
}

export interface Catalog {
  /** false = la página no muestra cifras ("solicita cotización"). */
  confirmed: boolean
  currencies: Currency[]
  defaultCurrency: Currency
  defaultPeriod: Period
  annualNote: Localized
  allPlansInclude: Localized[]
  /** En orden de presentación. Incluye los ocultos (visible: false); la web los filtra. */
  plans: CatalogPlan[]
}

export interface SiteSettings {
  contact: { email: string; phone: string; whatsapp: string }
  support: {
    /** Texto de soporte (p. ej. "Soporte técnico 24/7 en todos los planes"). */
    text: Localized
    /** Tiempo de respuesta comprometido. Vacío = no se muestra (el cliente aún no lo ha dado). */
    responseTime: Localized
  }
  banner: { enabled: boolean; text: Localized; url: string }
}

/** Lo que el navegador necesita saber de los métodos de pago (nunca secretos). */
export interface PublicPayments {
  paypal: { enabled: boolean; clientId: string; mode: 'sandbox' | 'live' }
  neo: { enabled: boolean }
  transfer: { enabled: boolean }
}

export interface PublicSite {
  catalog: Catalog
  settings: SiteSettings
  payments: PublicPayments
}

/** Importe publicable de un plan, o null (sin confirmar o a medida). */
export function catalogPrice(
  catalog: Pick<Catalog, 'confirmed'>,
  plan: Pick<CatalogPlan, 'prices'>,
  currency: Currency,
  period: Period,
): number | null {
  if (!catalog.confirmed || !plan.prices) return null
  return plan.prices[currency][period]
}

// ---- Validación del catálogo que guarda el panel -------------------------------------------

const text = (max: number) => z.string().trim().max(max)
const localized = (max: number, min = 1) =>
  z.object({ es: text(max).min(min), en: text(max).min(min) })
const optionalLocalized = (max: number) => z.object({ es: text(max), en: text(max) })
const amount = z
  .number()
  .positive()
  .max(10_000_000)
  .refine((n) => Math.round(n * 100) === Number((n * 100).toFixed(6)), 'twoDecimals')
const periodPrices = z.object({ monthly: amount, annual: amount })

export const catalogPlanSchema = z.object({
  id: z.enum(PLAN_IDS),
  name: localized(60),
  summary: localized(200),
  server: localized(120).nullable(),
  includes: z.array(localized(160)).max(12),
  installerSize: z.enum(['small', 'medium', 'large']).nullable(),
  clients: z.number().int().positive().nullable(),
  prices: z.object({ USD: periodPrices, GTQ: periodPrices }).nullable(),
  highlighted: z.boolean(),
  visible: z.boolean(),
})

export const catalogSchema = z
  .object({
    confirmed: z.boolean(),
    currencies: z.array(z.enum(CURRENCIES)).min(1),
    defaultCurrency: z.enum(CURRENCIES),
    defaultPeriod: z.enum(PERIODS),
    annualNote: optionalLocalized(200),
    allPlansInclude: z.array(localized(160)).max(12),
    plans: z.array(catalogPlanSchema).length(PLAN_IDS.length),
  })
  .superRefine((c, ctx) => {
    const ids = new Set(c.plans.map((p) => p.id))
    if (ids.size !== PLAN_IDS.length) ctx.addIssue({ code: 'custom', message: 'duplicatePlan' })
    if (c.plans.filter((p) => p.highlighted && p.visible).length > 1) {
      ctx.addIssue({ code: 'custom', message: 'oneHighlighted', path: ['plans'] })
    }
    if (!c.currencies.includes(c.defaultCurrency)) {
      ctx.addIssue({ code: 'custom', message: 'defaultCurrency', path: ['defaultCurrency'] })
    }
  })

export const siteSettingsSchema = z.object({
  contact: z.object({
    email: z.email().max(254),
    phone: text(40),
    whatsapp: text(40),
  }),
  support: z.object({ text: localized(200), responseTime: optionalLocalized(120) }),
  banner: z.object({
    enabled: z.boolean(),
    text: optionalLocalized(200),
    url: z
      .string()
      .trim()
      .max(500)
      .refine((v) => v === '' || /^(https:\/\/|\/)/.test(v), 'url'),
  }),
})
