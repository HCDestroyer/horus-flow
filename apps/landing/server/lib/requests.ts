// Solicitudes de demo, contacto y compra guardadas en la base de datos, con su estado e
// historial. Estados: new (nueva), pending_payment (pendiente de pago), contacted (contactado),
// paid (pagada), cancelled (cancelada).
import type { Currency, Period, PlanId } from '../../shared/catalog'
import { randomToken, safeEqual, sha256 } from './auth/crypto'
import { nowIso, type DB } from './db'

export const REQUEST_STATUSES = [
  'new',
  'pending_payment',
  'contacted',
  'paid',
  'cancelled',
] as const
export type RequestStatus = (typeof REQUEST_STATUSES)[number]
export type RequestKind = 'demo' | 'contact' | 'purchase'
export type PaymentMethod = 'paypal' | 'neo' | 'transfer'

export const STATUS_LABEL: Record<RequestStatus, string> = {
  new: 'Nueva',
  pending_payment: 'Pendiente de pago',
  contacted: 'Contactado',
  paid: 'Pagada',
  cancelled: 'Cancelada',
}

export interface RequestRow {
  id: number
  reference: string
  kind: RequestKind
  status: RequestStatus
  created_at: string
  updated_at: string
  locale: 'es' | 'en'
  name: string
  company: string
  email: string
  country: string
  data: string
  plan: PlanId | null
  period: Period | null
  currency: Currency | null
  amount_minor: number | null
  amount_usd_minor: number | null
  payment_method: PaymentMethod | null
  access_token_hash: string | null
  paypal_order_id: string | null
  paypal_capture_id: string | null
  paid_at: string | null
  paid_amount_minor: number | null
  paid_currency: string | null
}

export interface NewRequest {
  reference: string
  kind: RequestKind
  locale: 'es' | 'en'
  name: string
  company: string
  email: string
  country: string
  data: Record<string, unknown>
  plan?: PlanId
  period?: Period
  currency?: Currency
  amountMinor?: number | null
  amountUsdMinor?: number | null
}

/** Guarda una solicitud. En compras devuelve un token de acceso para el pago (solo se guarda su hash). */
export function insertRequest(
  db: DB,
  r: NewRequest,
  now = Date.now(),
): { id: number; accessToken: string | null } {
  const accessToken = r.kind === 'purchase' ? randomToken(24) : null
  const info = db
    .prepare(
      `INSERT INTO requests (reference, kind, status, created_at, updated_at, locale, name, company,
         email, country, data, plan, period, currency, amount_minor, amount_usd_minor, access_token_hash)
       VALUES (?, ?, 'new', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    )
    .run(
      r.reference,
      r.kind,
      nowIso(now),
      nowIso(now),
      r.locale,
      r.name,
      r.company,
      r.email,
      r.country,
      JSON.stringify(r.data),
      r.plan ?? null,
      r.period ?? null,
      r.currency ?? null,
      r.amountMinor ?? null,
      r.amountUsdMinor ?? null,
      accessToken ? sha256(accessToken) : null,
    )
  const id = Number(info.lastInsertRowid)
  addEvent(db, id, 'sistema', null, 'new', 'Solicitud recibida desde la web', now)
  return { id, accessToken }
}

export function addEvent(
  db: DB,
  requestId: number,
  actor: string,
  from: RequestStatus | null,
  to: RequestStatus | null,
  note: string,
  now = Date.now(),
): void {
  db.prepare(
    'INSERT INTO request_events (request_id, at, actor, from_status, to_status, note) VALUES (?, ?, ?, ?, ?, ?)',
  ).run(requestId, nowIso(now), actor, from, to, note.slice(0, 1000))
}

export function getRequestByReference(db: DB, reference: string): RequestRow | undefined {
  return db.prepare('SELECT * FROM requests WHERE reference = ?').get(reference) as
    RequestRow | undefined
}

export function getRequest(db: DB, id: number): RequestRow | undefined {
  return db.prepare('SELECT * FROM requests WHERE id = ?').get(id) as RequestRow | undefined
}

/** Compra + token de acceso válido (el que recibió el navegador al crear la solicitud). */
export function authorizePurchase(db: DB, reference: string, token: string): RequestRow | null {
  const row = getRequestByReference(db, reference)
  if (!row || row.kind !== 'purchase' || !row.access_token_hash || !token) return null
  return safeEqual(sha256(token), row.access_token_hash) ? row : null
}

export function setStatus(
  db: DB,
  id: number,
  to: RequestStatus,
  actor: string,
  note = '',
  now = Date.now(),
): { before: RequestStatus; after: RequestStatus } {
  const row = getRequest(db, id)
  if (!row) throw new Error('NOT_FOUND')
  db.transaction(() => {
    db.prepare('UPDATE requests SET status = ?, updated_at = ? WHERE id = ?').run(
      to,
      nowIso(now),
      id,
    )
    if (to === 'paid' && !row.paid_at) {
      db.prepare('UPDATE requests SET paid_at = ? WHERE id = ?').run(nowIso(now), id)
    }
    addEvent(db, id, actor, row.status, to, note, now)
  })()
  return { before: row.status, after: to }
}

/** El cliente elige método de pago: pasa a "pendiente de pago" (si no estaba pagada). */
export function choosePaymentMethod(
  db: DB,
  row: RequestRow,
  method: PaymentMethod,
  now = Date.now(),
): void {
  if (row.status === 'paid' || row.status === 'cancelled') return
  db.transaction(() => {
    db.prepare(
      `UPDATE requests SET payment_method = ?, status = 'pending_payment', updated_at = ? WHERE id = ?`,
    ).run(method, nowIso(now), row.id)
    if (row.payment_method !== method || row.status !== 'pending_payment') {
      addEvent(
        db,
        row.id,
        'cliente',
        row.status,
        'pending_payment',
        `Método de pago elegido: ${method}`,
        now,
      )
    }
  })()
}

/**
 * Marca una compra como pagada por PayPal. Idempotente: si ya estaba pagada devuelve false y no
 * cambia nada (el aviso por correo solo se envía la primera vez).
 */
export function markPaidByPaypal(
  db: DB,
  reference: string,
  capture: {
    captureId: string
    orderId?: string
    amountMinor: number
    currency: string
    source: string
  },
  now = Date.now(),
): { changed: boolean; row: RequestRow | undefined } {
  return db.transaction(() => {
    const row = getRequestByReference(db, reference)
    if (!row || row.kind !== 'purchase') return { changed: false, row }
    if (row.status === 'paid') return { changed: false, row }
    db.prepare(
      `UPDATE requests SET status = 'paid', payment_method = 'paypal', paypal_capture_id = ?,
         paypal_order_id = COALESCE(paypal_order_id, ?), paid_at = ?, paid_amount_minor = ?,
         paid_currency = ?, updated_at = ? WHERE id = ?`,
    ).run(
      capture.captureId,
      capture.orderId ?? null,
      nowIso(now),
      capture.amountMinor,
      capture.currency,
      nowIso(now),
      row.id,
    )
    addEvent(
      db,
      row.id,
      'paypal',
      row.status,
      'paid',
      `Pago PayPal confirmado (${capture.source}, captura ${capture.captureId})`,
      now,
    )
    return { changed: true, row: getRequest(db, row.id) }
  })()
}

export interface RequestListItem {
  id: number
  reference: string
  kind: RequestKind
  status: RequestStatus
  createdAt: string
  name: string
  company: string
  email: string
  country: string
  plan: PlanId | null
  period: Period | null
  currency: Currency | null
  amount: number | null
  paymentMethod: PaymentMethod | null
}

function toItem(r: RequestRow): RequestListItem {
  return {
    id: r.id,
    reference: r.reference,
    kind: r.kind,
    status: r.status,
    createdAt: r.created_at,
    name: r.name,
    company: r.company,
    email: r.email,
    country: r.country,
    plan: r.plan,
    period: r.period,
    currency: r.currency,
    amount: r.amount_minor === null ? null : r.amount_minor / 100,
    paymentMethod: r.payment_method,
  }
}

export interface RequestFilter {
  kind?: 'demo' | 'purchase' | 'all'
  status?: RequestStatus | 'all'
  q?: string
  limit?: number
  offset?: number
}

function where(f: RequestFilter): { sql: string; params: unknown[] } {
  const parts: string[] = []
  const params: unknown[] = []
  if (f.kind === 'demo') parts.push(`kind IN ('demo', 'contact')`)
  else if (f.kind === 'purchase') parts.push(`kind = 'purchase'`)
  if (f.status && f.status !== 'all') {
    parts.push('status = ?')
    params.push(f.status)
  }
  if (f.q) {
    parts.push('(reference LIKE ? OR name LIKE ? OR company LIKE ? OR email LIKE ?)')
    const like = `%${f.q.replace(/[%_]/g, '')}%`
    params.push(like, like, like, like)
  }
  return { sql: parts.length ? `WHERE ${parts.join(' AND ')}` : '', params }
}

export function listRequests(
  db: DB,
  f: RequestFilter,
): { items: RequestListItem[]; total: number } {
  const w = where(f)
  const total = (
    db.prepare(`SELECT COUNT(*) AS n FROM requests ${w.sql}`).get(...w.params) as { n: number }
  ).n
  const rows = db
    .prepare(`SELECT * FROM requests ${w.sql} ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`)
    .all(...w.params, Math.min(f.limit ?? 50, 500), f.offset ?? 0) as RequestRow[]
  return { items: rows.map(toItem), total }
}

export function requestDetail(db: DB, id: number) {
  const row = getRequest(db, id)
  if (!row) return null
  const events = db
    .prepare(
      'SELECT at, actor, from_status, to_status, note FROM request_events WHERE request_id = ? ORDER BY id',
    )
    .all(id) as {
    at: string
    actor: string
    from_status: string | null
    to_status: string | null
    note: string
  }[]
  return {
    ...toItem(row),
    locale: row.locale,
    data: JSON.parse(row.data) as Record<string, unknown>,
    amountUsd: row.amount_usd_minor === null ? null : row.amount_usd_minor / 100,
    paypalOrderId: row.paypal_order_id,
    paypalCaptureId: row.paypal_capture_id,
    paidAt: row.paid_at,
    paidAmount: row.paid_amount_minor === null ? null : row.paid_amount_minor / 100,
    paidCurrency: row.paid_currency,
    events: events.map((e) => ({
      at: e.at,
      actor: e.actor,
      from: e.from_status,
      to: e.to_status,
      note: e.note,
    })),
  }
}

// ---- CSV -----------------------------------------------------------------------------------

function csvCell(v: unknown): string {
  let s = v === null || v === undefined ? '' : String(v)
  // Evita la inyección de fórmulas al abrir el CSV en una hoja de cálculo.
  if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`
  return /[",\n\r;]/.test(s) ? `"${s.replaceAll('"', '""')}"` : s
}

export function requestsCsv(db: DB, f: RequestFilter): string {
  const w = where(f)
  const rows = db
    .prepare(`SELECT * FROM requests ${w.sql} ORDER BY created_at DESC, id DESC`)
    .all(...w.params) as RequestRow[]
  const header = [
    'referencia',
    'tipo',
    'estado',
    'fecha',
    'nombre',
    'empresa',
    'correo',
    'telefono',
    'pais',
    'plan',
    'periodo',
    'moneda',
    'importe',
    'metodo_pago',
    'pagada_el',
    'nit',
    'clientes',
    'routers',
    'mensaje',
  ]
  const lines = rows.map((r) => {
    const d = JSON.parse(r.data) as Record<string, unknown>
    return [
      r.reference,
      r.kind,
      STATUS_LABEL[r.status],
      r.created_at,
      r.name,
      r.company,
      r.email,
      d.phone ?? '',
      r.country,
      r.plan ?? '',
      r.period ?? '',
      r.currency ?? '',
      r.amount_minor === null ? '' : (r.amount_minor / 100).toFixed(2),
      r.payment_method ?? '',
      r.paid_at ?? '',
      d.nit ?? '',
      d.clients ?? '',
      d.routers ?? '',
      d.message ?? d.notes ?? '',
    ]
      .map(csvCell)
      .join(',')
  })
  // BOM para que Excel abra bien los acentos.
  return '﻿' + [header.join(','), ...lines].join('\r\n') + '\r\n'
}
