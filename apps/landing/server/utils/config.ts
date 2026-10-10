// Configuración del servidor de la landing, leída de variables de entorno (README.md ›
// Variables). Se lee una vez por proceso; los tests construyen la suya con loadConfig(env).

import { readFileSync } from 'node:fs'

/** Valor de VAR o, si hay VAR_FILE, el contenido de ese archivo (secretos de Docker). */
export function secretEnv(env: Record<string, string | undefined>, name: string): string {
  const file = env[`${name}_FILE`]?.trim()
  if (file) {
    try {
      return readFileSync(file, 'utf8').replace(/\r?\n$/, '')
    } catch {
      return ''
    }
  }
  return env[name] ?? ''
}

export type MailMode = 'smtp' | 'file' | 'log'

export interface ServerConfig {
  isProd: boolean
  mail: {
    mode: MailMode
    smtp: {
      host: string
      port: number
      secure: boolean
      user: string
      pass: string
    } | null
    from: string
    salesTo: string
    /** Carpeta del modo `file` (SMTP simulado para desarrollo y e2e). */
    outboxDir: string
    /** Enviar el correo de confirmación al cliente. */
    confirmToCustomer: boolean
  }
  webhook: { url: string; secret: string } | null
  /** CIDR o IP de los proxies cuyas cabeceras X-Forwarded-For se aceptan. */
  trustedProxies: string[]
  rateLimit: { max: number; windowMs: number }
  minFillMs: number
  /** Solo pruebas: base de un PayPal simulado (PAYPAL_API_BASE). Vacío = API real según el modo. */
  paypalApiBase: string
  /** Cookie de sesión del panel sin Secure ni prefijo __Host- (solo desarrollo por HTTP). */
  adminCookieInsecure: boolean
}

function int(value: string | undefined, fallback: number, min = 0): number {
  const n = Number.parseInt(value ?? '', 10)
  return Number.isFinite(n) && n >= min ? n : fallback
}

function bool(value: string | undefined, fallback: boolean): boolean {
  if (value === undefined || value === '') return fallback
  return ['1', 'true', 'yes', 'on'].includes(value.toLowerCase())
}

export function loadConfig(env: Record<string, string | undefined> = process.env): ServerConfig {
  const host = env.SMTP_HOST?.trim() ?? ''
  const port = int(env.SMTP_PORT, 587, 1)
  const explicitMode = env.MAIL_TRANSPORT?.trim() as MailMode | undefined
  const mode: MailMode =
    explicitMode && ['smtp', 'file', 'log'].includes(explicitMode)
      ? explicitMode
      : host
        ? 'smtp'
        : 'log'
  const salesTo = env.SALES_EMAIL?.trim() || 'info@kns.gt'
  const webhookUrl = env.LEADS_WEBHOOK_URL?.trim() ?? ''

  return {
    isProd: env.NODE_ENV === 'production',
    mail: {
      mode,
      smtp:
        mode === 'smtp' && host
          ? {
              host,
              port,
              secure: bool(env.SMTP_SECURE, port === 465),
              user: env.SMTP_USER ?? '',
              pass: secretEnv(env, 'SMTP_PASS'),
            }
          : null,
      from: env.MAIL_FROM?.trim() || `Horus Flow <${salesTo}>`,
      salesTo,
      outboxDir: env.MAIL_OUTBOX_DIR?.trim() || '.outbox',
      confirmToCustomer: bool(env.MAIL_CONFIRM_CUSTOMER, true),
    },
    webhook: webhookUrl
      ? { url: webhookUrl, secret: secretEnv(env, 'LEADS_WEBHOOK_SECRET') }
      : null,
    trustedProxies: (env.TRUSTED_PROXIES ?? '')
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean),
    rateLimit: {
      max: int(env.RATE_LIMIT_MAX, 8, 1),
      windowMs: int(env.RATE_LIMIT_WINDOW_SECONDS, 600, 1) * 1000,
    },
    minFillMs: int(env.FORM_MIN_FILL_SECONDS, 3) * 1000,
    // Nunca en producción: un PayPal simulado no puede confirmar pagos reales.
    paypalApiBase: env.NODE_ENV === 'production' ? '' : (env.PAYPAL_API_BASE?.trim() ?? ''),
    adminCookieInsecure: env.NODE_ENV !== 'production' && bool(env.ADMIN_COOKIE_INSECURE, false),
  }
}
