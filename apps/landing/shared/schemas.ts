// Esquemas de los formularios, compartidos por el cliente (validación en vivo) y el servidor
// (validación autoritativa). Los mensajes de error son claves de i18n (`errors.<clave>`):
// el cliente las traduce y el servidor las devuelve tal cual en `fields`.
import { z } from 'zod'
import { planIds } from '../app/config/pricing'

/** Países que se ofrecen en los formularios (ISO 3166-1 alfa-2; ZZ = otro país). */
export const COUNTRY_CODES = [
  'GT',
  'SV',
  'HN',
  'NI',
  'CR',
  'PA',
  'BZ',
  'MX',
  'DO',
  'PR',
  'CO',
  'VE',
  'EC',
  'PE',
  'BO',
  'CL',
  'AR',
  'UY',
  'PY',
  'US',
  'ES',
  'ZZ',
] as const

export const CLIENT_RANGES = ['lt300', '300to2000', '2000to10000', 'gt10000'] as const
export const ROUTER_RANGES = ['1', '2to5', '6to20', 'gt20'] as const
export const LOCALES = ['es', 'en'] as const

const PHONE_RE = /^[0-9+()\-.\s]{6,30}$/
// NIT de Guatemala: dígitos y dígito verificador (0-9 o K), con o sin guion; o "CF".
const NIT_RE = /^(CF|[0-9]{3,12}-?[0-9K])$/i

const required = (max: number, min = 1) =>
  z.string({ error: 'required' }).trim().min(1, 'required').min(min, 'tooShort').max(max, 'tooLong')

const optionalText = (max: number) =>
  z.string({ error: 'invalid' }).trim().max(max, 'tooLong').optional().default('')

const email = z
  .string({ error: 'required' })
  .trim()
  .min(1, 'required')
  .max(254, 'tooLong')
  .pipe(z.email({ error: 'email' }))

const phone = optionalText(30).refine((v) => v === '' || PHONE_RE.test(v), { error: 'phone' })

const consent = z.literal(true, { error: 'consent' })

const choice = <T extends readonly [string, ...string[]]>(values: T) =>
  z.enum(values, { error: 'required' })

export const leadSchema = z.object({
  kind: z.enum(['demo', 'contact']).default('demo'),
  name: required(100, 2),
  company: required(120),
  country: choice(COUNTRY_CODES),
  clients: choice(CLIENT_RANGES),
  routers: choice(ROUTER_RANGES),
  email,
  phone,
  message: optionalText(2000),
  consent,
  locale: z.enum(LOCALES).default('es'),
})

export const purchaseSchema = z.object({
  plan: choice(planIds),
  period: z.enum(['monthly', 'annual'], { error: 'required' }),
  currency: z.enum(['USD', 'GTQ'], { error: 'required' }),
  legalName: required(160, 2),
  nit: optionalText(20).refine((v) => v === '' || NIT_RE.test(v.replace(/\s/g, '')), {
    error: 'invalid',
  }),
  country: choice(COUNTRY_CODES),
  address: optionalText(300),
  contactName: required(100, 2),
  email,
  phone,
  notes: optionalText(2000),
  consent,
  locale: z.enum(LOCALES).default('es'),
})

/** Campos antispam que acompañan a cada envío y no forman parte de los datos. */
export const antispamSchema = z.object({
  // Honeypot: campo oculto que una persona nunca rellena.
  website: z.string().optional().default(''),
  // Marca de tiempo (ms) de cuando se mostró el formulario.
  startedAt: z.number().int().nonnegative().optional(),
})

export type LeadInput = z.input<typeof leadSchema>
export type Lead = z.output<typeof leadSchema>
export type PurchaseInput = z.input<typeof purchaseSchema>
export type Purchase = z.output<typeof purchaseSchema>

/** Respuesta común de las rutas de formularios. */
export interface SubmitOk {
  ok: true
  reference: string
}

/** Respuesta de POST /api/purchase: referencia, importe calculado en el servidor y métodos. */
export interface PurchaseOk extends SubmitOk {
  /** Token para pagar esta solicitud (solo lo tiene el navegador que la creó). */
  accessToken: string
  amount: number | null
  currency: 'USD' | 'GTQ'
  /** Importe en USD del mismo plan y periodo (PayPal cobra en USD). */
  amountUsd: number | null
  methods: { paypal: boolean; neo: boolean; transfer: boolean }
}

export interface SubmitError {
  ok: false
  /** VALIDATION | TOO_FAST | RATE_LIMITED | UNAVAILABLE | SERVER */
  code: string
  /** Campo → clave de error (errors.<clave>). */
  fields?: Record<string, string>
}

/** Convierte los errores de zod en { campo: clave } (primer error de cada campo). */
export function fieldErrors(error: z.ZodError): Record<string, string> {
  const out: Record<string, string> = {}
  for (const issue of error.issues) {
    const key = String(issue.path[0] ?? '_')
    if (!(key in out)) out[key] = /^[a-zA-Z]+$/.test(issue.message) ? issue.message : 'invalid'
  }
  return out
}
