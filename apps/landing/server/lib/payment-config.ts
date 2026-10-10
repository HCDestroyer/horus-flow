// Configuración de los tres métodos de pago (tabla payment_methods):
//   paypal   — modo sandbox/live, webhook id; client id y secret cifrados (solo escritura).
//   neo      — links de pago Neo por plan y periodo (URL externa configurable; sin API).
//   transfer — cuentas bancarias e instrucciones en ES/EN.
import { z } from 'zod'
import {
  CURRENCIES,
  PERIODS,
  PLAN_IDS,
  type Currency,
  type Localized,
  type Period,
  type PlanId,
  type PublicPayments,
} from '../../shared/catalog'
import { nowIso, type DB } from './db'
import type { SecretBox } from './secrets'

export type PaymentMethodId = 'paypal' | 'neo' | 'transfer'
export type PaypalMode = 'sandbox' | 'live'

export interface TransferAccount {
  bank: string
  type: Localized
  number: string
  holder: string
  currency: Currency
}

export interface PaymentsConfig {
  paypal: {
    enabled: boolean
    mode: PaypalMode
    webhookId: string
    /** Hay client id y secret guardados (y se pueden descifrar con la clave actual). */
    hasCredentials: boolean
    /** Últimos 4 caracteres del client id, para reconocerlo sin mostrarlo. */
    clientIdHint: string
    /** Error al descifrar (p. ej. DATA_KEY_FILE distinta), si lo hay. */
    credentialsError: string
  }
  neo: { enabled: boolean; links: Partial<Record<PlanId, Partial<Record<Period, string>>>> }
  transfer: { enabled: boolean; accounts: TransferAccount[]; instructions: Localized }
}

export interface PaypalCredentials {
  clientId: string
  clientSecret: string
}

interface Row {
  id: PaymentMethodId
  enabled: number
  config: string
  secrets: string | null
}

function rows(db: DB): Record<PaymentMethodId, Row | undefined> {
  const all = db.prepare('SELECT id, enabled, config, secrets FROM payment_methods').all() as Row[]
  return {
    paypal: all.find((r) => r.id === 'paypal'),
    neo: all.find((r) => r.id === 'neo'),
    transfer: all.find((r) => r.id === 'transfer'),
  }
}

function json<T>(s: string | undefined, fallback: T): T {
  try {
    return s ? (JSON.parse(s) as T) : fallback
  } catch {
    return fallback
  }
}

export function readPaypalCredentials(db: DB, box: SecretBox | null): PaypalCredentials | null {
  const row = rows(db).paypal
  if (!row?.secrets || !box) return null
  try {
    return JSON.parse(box.open(row.secrets, 'payment:paypal')) as PaypalCredentials
  } catch {
    return null
  }
}

export function readPayments(db: DB, box: SecretBox | null): PaymentsConfig {
  const r = rows(db)
  const pp = json<{ mode?: PaypalMode; webhookId?: string }>(r.paypal?.config, {})
  let creds: PaypalCredentials | null = null
  let credentialsError = ''
  if (r.paypal?.secrets) {
    if (!box) credentialsError = 'Falta la clave de datos (DATA_KEY_FILE)'
    else {
      try {
        creds = JSON.parse(box.open(r.paypal.secrets, 'payment:paypal')) as PaypalCredentials
      } catch (err) {
        credentialsError = String((err as Error).message ?? err)
      }
    }
  }
  const neo = json<{ links?: PaymentsConfig['neo']['links'] }>(r.neo?.config, {})
  const tr = json<{ accounts?: TransferAccount[]; instructions?: Localized }>(
    r.transfer?.config,
    {},
  )
  return {
    paypal: {
      enabled: r.paypal?.enabled === 1,
      mode: pp.mode === 'live' ? 'live' : 'sandbox',
      webhookId: pp.webhookId ?? '',
      hasCredentials: Boolean(creds?.clientId && creds.clientSecret),
      clientIdHint: creds?.clientId ? creds.clientId.slice(-4) : '',
      credentialsError,
    },
    neo: { enabled: r.neo?.enabled === 1, links: neo.links ?? {} },
    transfer: {
      enabled: r.transfer?.enabled === 1,
      accounts: tr.accounts ?? [],
      instructions: tr.instructions ?? { es: '', en: '' },
    },
  }
}

/** Link Neo de un plan y periodo, o null. */
export function neoLink(cfg: PaymentsConfig, plan: PlanId, period: Period): string | null {
  return cfg.neo.links[plan]?.[period] || null
}

/** Lo que ve el navegador: cada método solo cuenta como activo si está completo. */
export function publicPayments(db: DB, box: SecretBox | null): PublicPayments {
  const cfg = readPayments(db, box)
  const creds = cfg.paypal.enabled ? readPaypalCredentials(db, box) : null
  const anyNeo = Object.values(cfg.neo.links).some((p) => p && Object.values(p).some(Boolean))
  return {
    paypal: {
      enabled: cfg.paypal.enabled && Boolean(creds?.clientId && creds.clientSecret),
      clientId: creds?.clientId ?? '',
      mode: cfg.paypal.mode,
    },
    neo: { enabled: cfg.neo.enabled && anyNeo },
    transfer: { enabled: cfg.transfer.enabled && cfg.transfer.accounts.length > 0 },
  }
}

// ---- Escritura (panel) ---------------------------------------------------------------------

const httpsUrl = z
  .string()
  .trim()
  .max(500)
  .refine((v) => v === '' || /^https:\/\/[^\s]+$/.test(v), 'url')

export const paypalUpdateSchema = z.object({
  enabled: z.boolean(),
  mode: z.enum(['sandbox', 'live']),
  webhookId: z.string().trim().max(64),
  /** Vacío = conservar el guardado. */
  clientId: z.string().trim().max(200).optional().default(''),
  clientSecret: z.string().trim().max(200).optional().default(''),
  /** true = borrar las credenciales guardadas. */
  clearCredentials: z.boolean().optional().default(false),
})

const periodLinks = z.object(
  Object.fromEntries(PERIODS.map((p) => [p, httpsUrl.optional().default('')])) as Record<
    Period,
    z.ZodDefault<z.ZodOptional<typeof httpsUrl>>
  >,
)

export const neoUpdateSchema = z.object({
  enabled: z.boolean(),
  links: z
    .object(
      Object.fromEntries(PLAN_IDS.map((p) => [p, periodLinks.optional()])) as Record<
        PlanId,
        z.ZodOptional<typeof periodLinks>
      >,
    )
    .partial(),
})

const loc = (max: number) =>
  z.object({ es: z.string().trim().max(max), en: z.string().trim().max(max) })

export const transferUpdateSchema = z.object({
  enabled: z.boolean(),
  accounts: z
    .array(
      z.object({
        bank: z.string().trim().min(1).max(80),
        type: loc(60),
        number: z.string().trim().min(1).max(60),
        holder: z.string().trim().min(1).max(160),
        currency: z.enum(CURRENCIES),
      }),
    )
    .max(6),
  instructions: loc(1000),
})

function upsert(db: DB, id: PaymentMethodId, enabled: boolean, config: unknown, now: number) {
  db.prepare(
    `INSERT INTO payment_methods (id, enabled, config, updated_at) VALUES (?, ?, ?, ?)
     ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled, config = excluded.config,
       updated_at = excluded.updated_at`,
  ).run(id, enabled ? 1 : 0, JSON.stringify(config), nowIso(now))
}

export function writePaypal(
  db: DB,
  box: SecretBox | null,
  input: z.input<typeof paypalUpdateSchema>,
  now = Date.now(),
): void {
  const v = paypalUpdateSchema.parse(input)
  db.transaction(() => {
    upsert(db, 'paypal', v.enabled, { mode: v.mode, webhookId: v.webhookId }, now)
    if (v.clearCredentials) {
      db.prepare('UPDATE payment_methods SET secrets = NULL WHERE id = ?').run('paypal')
    } else if (v.clientId || v.clientSecret) {
      if (!box) throw new Error('NO_DATA_KEY')
      const current = readPaypalCredentials(db, box)
      const next: PaypalCredentials = {
        clientId: v.clientId || current?.clientId || '',
        clientSecret: v.clientSecret || current?.clientSecret || '',
      }
      db.prepare('UPDATE payment_methods SET secrets = ? WHERE id = ?').run(
        box.seal(JSON.stringify(next), 'payment:paypal'),
        'paypal',
      )
    }
  })()
}

export function writeNeo(db: DB, input: z.input<typeof neoUpdateSchema>, now = Date.now()): void {
  const v = neoUpdateSchema.parse(input)
  upsert(db, 'neo', v.enabled, { links: v.links }, now)
}

export function writeTransfer(
  db: DB,
  input: z.input<typeof transferUpdateSchema>,
  now = Date.now(),
): void {
  const v = transferUpdateSchema.parse(input)
  upsert(db, 'transfer', v.enabled, { accounts: v.accounts, instructions: v.instructions }, now)
}
