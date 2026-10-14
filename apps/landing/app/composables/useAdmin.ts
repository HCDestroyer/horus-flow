// Estado y llamadas del panel de administración. Toda petición que cambia algo lleva el token
// CSRF de la sesión en X-CSRF-Token; un 401 devuelve al login.

export interface AdminSessionState {
  authenticated: boolean
  stage?: 'mfa' | 'enroll' | 'full'
  email?: string
  name?: string
  csrf?: string
  recoveryCodesLeft?: number | null
}

export interface AdminApiError {
  status: number
  code: string
  retryAfterSec?: number
  issues?: { path: string; message: string }[]
}

export function useAdminSession() {
  return useState<AdminSessionState | null>('admin-session', () => null)
}

export async function refreshAdminSession(): Promise<AdminSessionState> {
  const s = await $fetch<AdminSessionState>('/api/admin/auth/session', {
    credentials: 'same-origin',
  })
  useAdminSession().value = s
  return s
}

export function toAdminError(err: unknown): AdminApiError {
  const e = err as {
    statusCode?: number
    status?: number
    data?: { data?: Record<string, unknown> } & Record<string, unknown>
  }
  const data = (e.data?.data ?? e.data ?? {}) as Record<string, unknown>
  return {
    status: e.statusCode ?? e.status ?? 0,
    code: String(data.code ?? 'ERROR'),
    retryAfterSec: typeof data.retryAfterSec === 'number' ? data.retryAfterSec : undefined,
    issues: Array.isArray(data.issues) ? (data.issues as AdminApiError['issues']) : undefined,
  }
}

/** Llamada a /api/admin/** con CSRF. */
export async function adminApi<T>(
  path: string,
  opts: {
    method?: 'GET' | 'POST' | 'DELETE'
    body?: unknown
    query?: Record<string, unknown>
  } = {},
): Promise<T> {
  const session = useAdminSession()
  const method = opts.method ?? 'GET'
  try {
    return (await $fetch(`/api/admin${path}`, {
      method,
      body: opts.body as Record<string, unknown> | undefined,
      query: opts.query,
      credentials: 'same-origin',
      headers: method === 'GET' ? {} : { 'x-csrf-token': session.value?.csrf ?? '' },
    })) as T
  } catch (err) {
    const e = toAdminError(err)
    if (e.status === 401 && !path.startsWith('/auth/')) {
      session.value = { authenticated: false }
      await navigateTo('/admin/login')
    }
    throw err
  }
}

const MESSAGES: Record<string, string> = {
  INVALID_CREDENTIALS: 'Correo o contraseña incorrectos.',
  INVALID_CODE: 'El código no es válido. Usa el código actual de tu app de autenticación.',
  LOCKED: 'Demasiados intentos fallidos. Espera {s} antes de volver a intentarlo.',
  RATE_LIMITED: 'Demasiados intentos. Espera {s} y vuelve a intentarlo.',
  CSRF: 'La sesión del formulario caducó. Recarga la página.',
  VALIDATION: 'Revisa los campos marcados.',
  NO_DATA_KEY: 'Falta la clave de datos (DATA_KEY_FILE): no se pueden guardar credenciales.',
  PASSWORD_TOO_SHORT: 'La contraseña debe tener al menos 12 caracteres.',
  EMAIL_INVALID: 'El correo no es válido.',
  EMAIL_EXISTS: 'Ya hay un administrador con ese correo.',
  CANNOT_DISABLE_SELF: 'No puedes desactivar tu propia cuenta.',
  CANNOT_DELETE_SELF: 'No puedes eliminar tu propia cuenta.',
  LAST_ADMIN: 'Debe quedar al menos un administrador activo.',
  NOT_A_PURCHASE: 'Solo una compra puede marcarse como pagada.',
  WRONG_STAGE: 'La sesión cambió. Vuelve a iniciar sesión.',
}

export function adminErrorMessage(err: unknown): string {
  const e = toAdminError(err)
  const wait = e.retryAfterSec
    ? e.retryAfterSec >= 60
      ? `${Math.ceil(e.retryAfterSec / 60)} min`
      : `${e.retryAfterSec} s`
    : 'unos minutos'
  return (MESSAGES[e.code] ?? `No se pudo completar la acción (${e.code}).`).replace('{s}', wait)
}

export const STATUS_LABELS: Record<string, string> = {
  new: 'Nueva',
  pending_payment: 'Pendiente de pago',
  contacted: 'Contactado',
  paid: 'Pagada',
  cancelled: 'Cancelada',
}

export const METHOD_LABELS: Record<string, string> = {
  paypal: 'PayPal',
  neo: 'Link Neo',
  transfer: 'Transferencia',
}

export function formatDate(iso: string | null | undefined, short = false): string {
  if (!iso) return '—'
  return new Intl.DateTimeFormat('es-GT', {
    dateStyle: short ? 'short' : 'medium',
    timeStyle: 'short',
    timeZone: 'America/Guatemala',
  }).format(new Date(iso))
}

export function formatMoney(
  amount: number | null | undefined,
  currency: string | null | undefined,
) {
  if (amount === null || amount === undefined || !currency) return '—'
  return new Intl.NumberFormat('es-GT', {
    style: 'currency',
    currency,
    minimumFractionDigits: Number.isInteger(amount) ? 0 : 2,
    maximumFractionDigits: 2,
  }).format(amount)
}
