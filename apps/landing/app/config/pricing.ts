/**
 * PRECIOS Y PLANES DE HORUS FLOW — único archivo que hay que editar para cambiar precios,
 * monedas, periodos o lo que incluye cada plan (README.md › "Editar precios").
 *
 * ⚠️ `confirmed: false` → la página NO muestra cifras: enseña "Precio de lanzamiento:
 * solicita cotización", el JSON-LD no publica importes y `nuxt build` avisa en producción.
 * Cuando C&S Company apruebe los importes, cámbialos aquí y pon `confirmed: true`.
 *
 * Los tamaños coinciden con los del instalador (`install.sh --size small|medium|large`,
 * docs/install-debian.md §1): ~300, ~2 000 y ~10 000 clientes.
 */

export type Currency = 'USD' | 'GTQ'
export type Period = 'monthly' | 'annual'
export type PlanId = 'small' | 'medium' | 'large' | 'enterprise'

/** Texto en los dos idiomas de la página. */
export interface Localized {
  es: string
  en: string
}

export interface Plan {
  id: PlanId
  name: Localized
  /** Tamaño del instalador (`--size`) o null para Enterprise. */
  installerSize: 'small' | 'medium' | 'large' | null
  /** Clientes (IPs) aproximados del ISP; null = a medida. */
  clients: number | null
  /** Importe por periodo y moneda. null = a medida (Enterprise). */
  prices: Record<Currency, Record<Period, number>> | null
  /** Plan destacado en la tabla (uno como mucho). */
  highlighted?: boolean
  summary: Localized
  /** Servidor recomendado (README.md › Requisitos); null = a medida. */
  server: Localized | null
  includes: Localized[]
}

export interface PricingConfig {
  /** false mientras C&S Company no apruebe los importes. */
  confirmed: boolean
  currencies: Currency[]
  defaultCurrency: Currency
  defaultPeriod: Period
  /** Texto de ahorro del pago anual (se muestra junto al selector). */
  annualNote: Localized
  /** Lo que incluye toda licencia, sea cual sea el plan. */
  allPlansInclude: Localized[]
  plans: Plan[]
}

export const pricing: PricingConfig = {
  confirmed: false,

  currencies: ['USD', 'GTQ'],
  defaultCurrency: 'USD',
  defaultPeriod: 'annual',

  annualNote: {
    es: 'El pago anual equivale a 10 meses.',
    en: 'Annual billing equals 10 months.',
  },

  allPlansInclude: [
    { es: 'Licencia propietaria por instalación', en: 'Proprietary license per installation' },
    {
      es: 'Actualizaciones firmadas mientras la licencia esté vigente',
      en: 'Signed updates while the license is active',
    },
    { es: 'Soporte técnico por correo', en: 'Technical support by email' },
    {
      es: 'Instalador de un comando para Debian 12/13',
      en: 'One-command installer for Debian 12/13',
    },
  ],

  // PROPUESTA (pendiente de aprobación de C&S Company; importes no publicados mientras
  // confirmed=false). Lo que incluye cada plan también es propuesta comercial.
  // Importes orientativos para un ISP de la región. GTQ redondeado a ~7,75 GTQ por USD.
  // Anual = 10 × mensual.
  plans: [
    {
      id: 'small',
      name: { es: 'Pequeño', en: 'Small' },
      installerSize: 'small',
      clients: 300,
      prices: {
        USD: { monthly: 149, annual: 1490 },
        GTQ: { monthly: 1150, annual: 11500 },
      },
      summary: {
        es: 'Para un nodo con hasta ~300 clientes.',
        en: 'For one node with up to ~300 subscribers.',
      },
      server: { es: '4 núcleos · 8 GB RAM · 100 GB SSD', en: '4 cores · 8 GB RAM · 100 GB SSD' },
      includes: [
        { es: 'Hasta ~300 clientes (IPs)', en: 'Up to ~300 subscribers (IPs)' },
        { es: '1 ISP, routers MikroTik ilimitados', en: '1 ISP, unlimited MikroTik routers' },
        { es: 'Detección de botnets y alertas', en: 'Botnet detection and alerts' },
        { es: 'Dashboards y 1 pantalla de kiosco', en: 'Dashboards and 1 kiosk screen' },
      ],
    },
    {
      id: 'medium',
      name: { es: 'Mediano', en: 'Medium' },
      installerSize: 'medium',
      clients: 2000,
      highlighted: true,
      prices: {
        USD: { monthly: 399, annual: 3990 },
        GTQ: { monthly: 3090, annual: 30900 },
      },
      summary: {
        es: 'Para un ISP con varios nodos y ~2 000 clientes.',
        en: 'For an ISP with several nodes and ~2,000 subscribers.',
      },
      server: { es: '8 núcleos · 16 GB RAM · 500 GB SSD', en: '8 cores · 16 GB RAM · 500 GB SSD' },
      includes: [
        { es: 'Hasta ~2 000 clientes (IPs)', en: 'Up to ~2,000 subscribers (IPs)' },
        { es: '1 ISP, routers MikroTik ilimitados', en: '1 ISP, unlimited MikroTik routers' },
        {
          es: 'Alertas por correo, Telegram y LibreNMS',
          en: 'Email, Telegram and LibreNMS alerts',
        },
        { es: 'Pantallas de kiosco ilimitadas', en: 'Unlimited kiosk screens' },
        { es: 'Ayuda con la instalación', en: 'Installation assistance' },
      ],
    },
    {
      id: 'large',
      name: { es: 'Grande', en: 'Large' },
      installerSize: 'large',
      clients: 10000,
      prices: {
        USD: { monthly: 990, annual: 9900 },
        GTQ: { monthly: 7650, annual: 76500 },
      },
      summary: {
        es: 'Para un ISP de ~10 000 clientes.',
        en: 'For an ISP with ~10,000 subscribers.',
      },
      server: {
        es: '8 núcleos · 32 GB RAM · 500 GB NVMe',
        en: '8 cores · 32 GB RAM · 500 GB NVMe',
      },
      includes: [
        { es: 'Hasta ~10 000 clientes (IPs)', en: 'Up to ~10,000 subscribers (IPs)' },
        { es: 'Todo lo del plan Mediano', en: 'Everything in Medium' },
        { es: 'Soporte prioritario', en: 'Priority support' },
        {
          es: 'Revisión del dimensionado del servidor',
          en: 'Server sizing review',
        },
      ],
    },
    {
      id: 'enterprise',
      name: { es: 'Enterprise / multi-ISP', en: 'Enterprise / multi-ISP' },
      installerSize: null,
      clients: null,
      prices: null,
      summary: {
        es: 'Varios ISP aislados en una instalación, o más de 10 000 clientes.',
        en: 'Several isolated ISPs on one installation, or more than 10,000 subscribers.',
      },
      server: null,
      includes: [
        { es: 'Multi-ISP con aislamiento completo', en: 'Multi-ISP with full isolation' },
        { es: 'Dimensionado a medida', en: 'Custom sizing' },
        { es: 'Condiciones de soporte a medida', en: 'Custom support terms' },
      ],
    },
  ],
}

/** Importe de un plan, o null si no hay cifra publicable (sin confirmar o a medida). */
export function planPrice(
  plan: Plan,
  currency: Currency,
  period: Period,
  config: PricingConfig = pricing,
): number | null {
  if (!config.confirmed || !plan.prices) return null
  return plan.prices[currency][period]
}

export const planIds = pricing.plans.map((p) => p.id) as [PlanId, ...PlanId[]]
