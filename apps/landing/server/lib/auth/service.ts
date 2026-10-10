// Administradores, sesiones, bloqueo progresivo y auditoría del panel. Independiente de h3 para
// poder probarlo: las rutas de server/api/admin solo traducen petición ↔ resultado.
//
// Flujo de acceso:
//   1. login (correo + contraseña) → sesión "mfa" (si tiene TOTP) o "enroll" (primera vez).
//   2. mfa: código TOTP o de recuperación → sesión "full" nueva (rotación).
//      enroll: QR + confirmar código → TOTP activado, códigos de recuperación → sesión "full".
//   Solo una sesión "full" da acceso a /api/admin/**.
import type { DB } from '../db'
import { nowIso } from '../db'
import type { SecretBox } from '../secrets'
import {
  dummyHash,
  hashPassword,
  hashPasswordSync,
  newRecoveryCodes,
  newTotpSecret,
  normalizeRecoveryCode,
  otpauthUri,
  passwordProblem,
  randomToken,
  sha256,
  verifyPassword,
  verifyTotp,
} from './crypto'

export const SESSION = {
  /** Paso intermedio (mfa / enroll). */
  pendingTtlMs: 10 * 60_000,
  /** Inactividad máxima de una sesión completa. */
  idleMs: 30 * 60_000,
  /** Duración máxima absoluta. */
  absoluteMs: 8 * 60 * 60_000,
  /** Cada cuánto se rota el identificador de una sesión completa. */
  rotateMs: 10 * 60_000,
  /** El identificador anterior sigue valiendo unos segundos (peticiones en vuelo). */
  graceMs: 30_000,
}

export const THROTTLE = {
  /** Fallos por correo antes del primer bloqueo; luego 1, 2, 4… min (máx. 60). */
  freeFailures: 5,
  /** Fallos por IP antes de bloquear la IP (protege frente a probar muchos correos). */
  ipFreeFailures: 20,
  maxLockMs: 60 * 60_000,
  /** Sin fallos durante este tiempo, el contador se reinicia. */
  resetAfterMs: 24 * 60 * 60_000,
}

export type SessionStage = 'mfa' | 'enroll' | 'full'

export interface AdminRow {
  id: number
  email: string
  name: string
  password_hash: string
  totp_secret: string | null
  totp_enabled: number
  totp_last_step: number
  recovery_codes: string
  disabled: number
  created_at: string
  last_login_at: string | null
}

export interface SessionInfo {
  idHash: string
  adminId: number
  email: string
  name: string
  stage: SessionStage
  csrf: string
  expiresAt: number
}

export interface AuthDeps {
  db: DB
  box: SecretBox | null
  now: () => number
}

export interface Actor {
  id: number | null
  email: string
  ip?: string
}

// ---- Auditoría -----------------------------------------------------------------------------

export function audit(
  deps: AuthDeps,
  actor: Actor,
  action: string,
  entity: string,
  entityId: string | number = '',
  before: unknown = null,
  after: unknown = null,
): void {
  deps.db
    .prepare(
      `INSERT INTO audit_log (at, admin_id, admin_email, action, entity, entity_id, before, after, ip)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    )
    .run(
      nowIso(deps.now()),
      actor.id,
      actor.email,
      action,
      entity,
      String(entityId),
      before === null ? null : JSON.stringify(before),
      after === null ? null : JSON.stringify(after),
      actor.ip ?? '',
    )
}

// ---- Secreto TOTP (cifrado si hay clave de datos) ------------------------------------------

function sealTotp(deps: AuthDeps, adminId: number, secret: string): string {
  return deps.box ? `enc:${deps.box.seal(secret, `totp:${adminId}`)}` : `plain:${secret}`
}

function openTotp(deps: AuthDeps, adminId: number, stored: string | null): string | null {
  if (!stored) return null
  if (stored.startsWith('plain:')) return stored.slice(6)
  if (stored.startsWith('enc:') && deps.box) {
    try {
      return deps.box.open(stored.slice(4), `totp:${adminId}`)
    } catch {
      return null
    }
  }
  return null
}

// ---- Administradores -----------------------------------------------------------------------

export function findAdminByEmail(db: DB, email: string): AdminRow | undefined {
  return db.prepare('SELECT * FROM admins WHERE email = ?').get(email.trim()) as
    AdminRow | undefined
}

export function getAdmin(db: DB, id: number): AdminRow | undefined {
  return db.prepare('SELECT * FROM admins WHERE id = ?').get(id) as AdminRow | undefined
}

export function countAdmins(db: DB): number {
  return (db.prepare('SELECT COUNT(*) AS n FROM admins').get() as { n: number }).n
}

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export class AuthError extends Error {
  constructor(
    readonly code: string,
    readonly status = 400,
    readonly retryAfterSec?: number,
  ) {
    super(code)
  }
}

function insertAdmin(deps: AuthDeps, email: string, name: string, hash: string): number {
  const info = deps.db
    .prepare('INSERT INTO admins (email, name, password_hash, created_at) VALUES (?, ?, ?, ?)')
    .run(email.trim().toLowerCase(), name.trim(), hash, nowIso(deps.now()))
  return Number(info.lastInsertRowid)
}

function checkNewAdmin(db: DB, email: string, password: string) {
  if (!EMAIL_RE.test(email.trim())) throw new AuthError('EMAIL_INVALID')
  const problem = passwordProblem(password)
  if (problem) throw new AuthError(problem)
  if (findAdminByEmail(db, email)) throw new AuthError('EMAIL_EXISTS', 409)
}

export async function createAdmin(
  deps: AuthDeps,
  input: { email: string; name?: string; password: string },
  actor: Actor,
): Promise<number> {
  checkNewAdmin(deps.db, input.email, input.password)
  const id = insertAdmin(deps, input.email, input.name ?? '', await hashPassword(input.password))
  audit(deps, actor, 'admin.create', 'admin', id, null, { email: input.email.trim().toLowerCase() })
  return id
}

/** Para la línea de órdenes y el arranque (síncrono). */
export function createAdminSync(
  deps: AuthDeps,
  input: { email: string; name?: string; password: string },
  actor: Actor,
): number {
  checkNewAdmin(deps.db, input.email, input.password)
  const id = insertAdmin(deps, input.email, input.name ?? '', hashPasswordSync(input.password))
  audit(deps, actor, 'admin.create', 'admin', id, null, { email: input.email.trim().toLowerCase() })
  return id
}

export function revokeSessions(db: DB, adminId: number): void {
  db.prepare('DELETE FROM sessions WHERE admin_id = ?').run(adminId)
}

/** Obliga a dar de alta el TOTP otra vez en el próximo acceso (y cierra sus sesiones). */
export function resetTotp(deps: AuthDeps, adminId: number, actor: Actor): void {
  const admin = getAdmin(deps.db, adminId)
  if (!admin) throw new AuthError('NOT_FOUND', 404)
  deps.db
    .prepare(
      `UPDATE admins SET totp_secret = NULL, totp_enabled = 0, totp_last_step = 0,
         recovery_codes = '[]' WHERE id = ?`,
    )
    .run(adminId)
  revokeSessions(deps.db, adminId)
  audit(
    deps,
    actor,
    'admin.reset_totp',
    'admin',
    adminId,
    { totp: Boolean(admin.totp_enabled) },
    { totp: false },
  )
}

export function setAdminDisabled(
  deps: AuthDeps,
  adminId: number,
  disabled: boolean,
  actor: Actor,
): void {
  const admin = getAdmin(deps.db, adminId)
  if (!admin) throw new AuthError('NOT_FOUND', 404)
  if (disabled && actor.id === adminId) throw new AuthError('CANNOT_DISABLE_SELF', 409)
  if (disabled) {
    const active = (
      deps.db
        .prepare('SELECT COUNT(*) AS n FROM admins WHERE disabled = 0 AND id != ?')
        .get(adminId) as {
        n: number
      }
    ).n
    if (active === 0) throw new AuthError('LAST_ADMIN', 409)
  }
  deps.db.prepare('UPDATE admins SET disabled = ? WHERE id = ?').run(disabled ? 1 : 0, adminId)
  if (disabled) revokeSessions(deps.db, adminId)
  audit(
    deps,
    actor,
    disabled ? 'admin.disable' : 'admin.enable',
    'admin',
    adminId,
    { disabled: Boolean(admin.disabled) },
    { disabled },
  )
}

export function deleteAdmin(deps: AuthDeps, adminId: number, actor: Actor): void {
  const admin = getAdmin(deps.db, adminId)
  if (!admin) throw new AuthError('NOT_FOUND', 404)
  if (actor.id === adminId) throw new AuthError('CANNOT_DELETE_SELF', 409)
  if (countAdmins(deps.db) <= 1) throw new AuthError('LAST_ADMIN', 409)
  deps.db.prepare('DELETE FROM admins WHERE id = ?').run(adminId)
  audit(deps, actor, 'admin.delete', 'admin', adminId, { email: admin.email }, null)
}

export async function changePassword(
  deps: AuthDeps,
  adminId: number,
  current: string,
  next: string,
  actor: Actor,
  keepSession?: string,
): Promise<void> {
  const admin = getAdmin(deps.db, adminId)
  if (!admin || !(await verifyPassword(current, admin.password_hash))) {
    throw new AuthError('INVALID_CREDENTIALS', 400)
  }
  const problem = passwordProblem(next)
  if (problem) throw new AuthError(problem)
  deps.db
    .prepare('UPDATE admins SET password_hash = ? WHERE id = ?')
    .run(await hashPassword(next), adminId)
  deps.db
    .prepare('DELETE FROM sessions WHERE admin_id = ? AND id_hash != ?')
    .run(adminId, keepSession ?? '')
  audit(deps, actor, 'admin.change_password', 'admin', adminId)
}

export interface AdminSummary {
  id: number
  email: string
  name: string
  totpEnabled: boolean
  disabled: boolean
  createdAt: string
  lastLoginAt: string | null
}

export function listAdmins(db: DB): AdminSummary[] {
  return (db.prepare('SELECT * FROM admins ORDER BY id').all() as AdminRow[]).map((a) => ({
    id: a.id,
    email: a.email,
    name: a.name,
    totpEnabled: a.totp_enabled === 1,
    disabled: a.disabled === 1,
    createdAt: a.created_at,
    lastLoginAt: a.last_login_at,
  }))
}

// ---- Bloqueo progresivo --------------------------------------------------------------------

interface ThrottleRow {
  key: string
  failures: number
  locked_until: number
  updated_at: number
}

function throttleRow(deps: AuthDeps, key: string): ThrottleRow | undefined {
  const row = deps.db.prepare('SELECT * FROM login_throttle WHERE key = ?').get(key) as
    ThrottleRow | undefined
  if (row && deps.now() - row.updated_at > THROTTLE.resetAfterMs) {
    deps.db.prepare('DELETE FROM login_throttle WHERE key = ?').run(key)
    return undefined
  }
  return row
}

/** Segundos de bloqueo restantes para alguna de las claves, o 0. */
export function lockedFor(deps: AuthDeps, keys: string[]): number {
  let max = 0
  for (const key of keys) {
    const row = throttleRow(deps, key)
    if (row && row.locked_until > deps.now()) {
      max = Math.max(max, Math.ceil((row.locked_until - deps.now()) / 1000))
    }
  }
  return max
}

export function lockMs(failures: number, free: number): number {
  if (failures < free) return 0
  return Math.min(THROTTLE.maxLockMs, 60_000 * 2 ** (failures - free))
}

export function recordFailure(deps: AuthDeps, key: string, free = THROTTLE.freeFailures): void {
  const now = deps.now()
  const row = throttleRow(deps, key)
  const failures = (row?.failures ?? 0) + 1
  const lock = lockMs(failures, free)
  deps.db
    .prepare(
      `INSERT INTO login_throttle (key, failures, locked_until, updated_at) VALUES (?, ?, ?, ?)
       ON CONFLICT(key) DO UPDATE SET failures = excluded.failures,
         locked_until = excluded.locked_until, updated_at = excluded.updated_at`,
    )
    .run(key, failures, lock ? now + lock : 0, now)
}

export function clearFailures(deps: AuthDeps, key: string): void {
  deps.db.prepare('DELETE FROM login_throttle WHERE key = ?').run(key)
}

// ---- Sesiones ------------------------------------------------------------------------------

export interface NewSession {
  token: string
  csrf: string
  stage: SessionStage
  expiresAt: number
}

function createSession(
  deps: AuthDeps,
  adminId: number,
  stage: SessionStage,
  ctx: { ip?: string; userAgent?: string },
  csrf = randomToken(24),
): NewSession {
  const now = deps.now()
  const token = randomToken(32)
  const expiresAt = now + (stage === 'full' ? SESSION.absoluteMs : SESSION.pendingTtlMs)
  deps.db
    .prepare(
      `INSERT INTO sessions (id_hash, admin_id, stage, csrf, created_at, rotated_at, last_seen,
         expires_at, ip, user_agent)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    )
    .run(
      sha256(token),
      adminId,
      stage,
      csrf,
      now,
      now,
      now,
      expiresAt,
      ctx.ip ?? '',
      (ctx.userAgent ?? '').slice(0, 200),
    )
  return { token, csrf, stage, expiresAt }
}

interface SessionRow {
  id_hash: string
  admin_id: number
  stage: SessionStage
  csrf: string
  created_at: number
  rotated_at: number
  last_seen: number
  expires_at: number
  replaced_by: string | null
  grace_until: number | null
}

export interface ResolvedSession {
  session: SessionInfo
  /** Si la sesión se ha rotado en esta petición: el token nuevo para la cookie. */
  rotatedToken?: string
}

/**
 * Busca la sesión del token de la cookie, comprueba caducidad e inactividad y la rota si toca.
 * Un token ya rotado sigue valiendo durante SESSION.graceMs (peticiones en vuelo).
 */
export function resolveSession(deps: AuthDeps, token: string | undefined): ResolvedSession | null {
  if (!token || token.length > 100) return null
  const now = deps.now()
  let row = deps.db.prepare('SELECT * FROM sessions WHERE id_hash = ?').get(sha256(token)) as
    SessionRow | undefined
  if (!row) return null
  if (row.replaced_by) {
    if (!row.grace_until || row.grace_until < now) {
      deps.db.prepare('DELETE FROM sessions WHERE id_hash = ?').run(row.id_hash)
      return null
    }
    row = deps.db.prepare('SELECT * FROM sessions WHERE id_hash = ?').get(row.replaced_by) as
      SessionRow | undefined
    if (!row) return null
  }
  const idle = row.stage === 'full' ? SESSION.idleMs : SESSION.pendingTtlMs
  if (row.expires_at <= now || now - row.last_seen > idle) {
    deps.db.prepare('DELETE FROM sessions WHERE id_hash = ?').run(row.id_hash)
    return null
  }
  const admin = getAdmin(deps.db, row.admin_id)
  if (!admin || admin.disabled) {
    revokeSessions(deps.db, row.admin_id)
    return null
  }

  let rotatedToken: string | undefined
  let idHash = row.id_hash
  if (row.stage === 'full' && now - row.rotated_at >= SESSION.rotateMs) {
    rotatedToken = randomToken(32)
    idHash = sha256(rotatedToken)
    deps.db.transaction(() => {
      deps.db
        .prepare(
          `INSERT INTO sessions (id_hash, admin_id, stage, csrf, created_at, rotated_at, last_seen,
             expires_at, ip, user_agent)
           SELECT ?, admin_id, stage, csrf, created_at, ?, ?, expires_at, ip, user_agent
           FROM sessions WHERE id_hash = ?`,
        )
        .run(idHash, now, now, row!.id_hash)
      deps.db
        .prepare('UPDATE sessions SET replaced_by = ?, grace_until = ? WHERE id_hash = ?')
        .run(idHash, now + SESSION.graceMs, row!.id_hash)
    })()
  } else {
    deps.db.prepare('UPDATE sessions SET last_seen = ? WHERE id_hash = ?').run(now, row.id_hash)
  }
  // Limpieza oportunista de sesiones caducadas.
  if (Math.random() < 0.05) {
    deps.db
      .prepare(
        'DELETE FROM sessions WHERE expires_at <= ? OR (grace_until IS NOT NULL AND grace_until < ?)',
      )
      .run(now, now)
  }
  return {
    session: {
      idHash,
      adminId: admin.id,
      email: admin.email,
      name: admin.name,
      stage: row.stage,
      csrf: row.csrf,
      expiresAt: row.expires_at,
    },
    rotatedToken,
  }
}

export function destroySession(deps: AuthDeps, idHash: string): void {
  deps.db.prepare('DELETE FROM sessions WHERE id_hash = ? OR replaced_by = ?').run(idHash, idHash)
}

/** Sube de nivel una sesión: borra la anterior y crea otra (identificador y CSRF nuevos). */
function upgrade(
  deps: AuthDeps,
  session: SessionInfo,
  stage: SessionStage,
  ctx: { ip?: string; userAgent?: string },
) {
  destroySession(deps, session.idHash)
  return createSession(deps, session.adminId, stage, ctx)
}

// ---- Login ---------------------------------------------------------------------------------

export interface LoginContext {
  ip: string
  userAgent?: string
}

export async function login(
  deps: AuthDeps,
  email: string,
  password: string,
  ctx: LoginContext,
): Promise<NewSession> {
  const emailKey = `email:${email.trim().toLowerCase()}`
  const ipKey = `ip:${ctx.ip}`
  const locked = lockedFor(deps, [emailKey, ipKey])
  if (locked > 0) throw new AuthError('LOCKED', 429, locked)

  const admin = findAdminByEmail(deps.db, email)
  const ok = await verifyPassword(password, admin?.password_hash ?? (await dummyHash()))
  if (!admin || !ok || admin.disabled) {
    recordFailure(deps, emailKey)
    recordFailure(deps, ipKey, THROTTLE.ipFreeFailures)
    audit(
      deps,
      { id: admin?.id ?? null, email: email.trim().toLowerCase().slice(0, 254), ip: ctx.ip },
      'auth.login_failed',
      'admin',
      admin?.id ?? '',
    )
    const after = lockedFor(deps, [emailKey, ipKey])
    if (after > 0) throw new AuthError('LOCKED', 429, after)
    throw new AuthError('INVALID_CREDENTIALS', 401)
  }
  clearFailures(deps, emailKey)
  return createSession(deps, admin.id, admin.totp_enabled ? 'mfa' : 'enroll', ctx)
}

function finishLogin(
  deps: AuthDeps,
  session: SessionInfo,
  ctx: LoginContext,
  how: string,
): NewSession {
  deps.db
    .prepare('UPDATE admins SET last_login_at = ? WHERE id = ?')
    .run(nowIso(deps.now()), session.adminId)
  clearFailures(deps, `mfa:${session.adminId}`)
  clearFailures(deps, `ip:${ctx.ip}`)
  audit(
    deps,
    { id: session.adminId, email: session.email, ip: ctx.ip },
    'auth.login',
    'admin',
    session.adminId,
    null,
    { method: how },
  )
  return upgrade(deps, session, 'full', ctx)
}

/** Segundo factor: código TOTP de 6 dígitos o código de recuperación (un solo uso). */
export function verifySecondFactor(
  deps: AuthDeps,
  session: SessionInfo,
  input: { code?: string; recoveryCode?: string },
  ctx: LoginContext,
): NewSession {
  if (session.stage !== 'mfa') throw new AuthError('WRONG_STAGE', 409)
  const key = `mfa:${session.adminId}`
  const locked = lockedFor(deps, [key])
  if (locked > 0) throw new AuthError('LOCKED', 429, locked)
  const admin = getAdmin(deps.db, session.adminId)!

  if (input.recoveryCode) {
    const hashes = JSON.parse(admin.recovery_codes) as string[]
    const h = sha256(normalizeRecoveryCode(input.recoveryCode))
    const idx = hashes.indexOf(h)
    if (idx >= 0) {
      hashes.splice(idx, 1)
      deps.db
        .prepare('UPDATE admins SET recovery_codes = ? WHERE id = ?')
        .run(JSON.stringify(hashes), admin.id)
      return finishLogin(deps, session, ctx, 'recovery_code')
    }
  } else if (input.code) {
    const secret = openTotp(deps, admin.id, admin.totp_secret)
    const step = secret ? verifyTotp(secret, input.code, deps.now(), admin.totp_last_step) : null
    if (step !== null) {
      deps.db.prepare('UPDATE admins SET totp_last_step = ? WHERE id = ?').run(step, admin.id)
      return finishLogin(deps, session, ctx, 'totp')
    }
  }
  recordFailure(deps, key)
  audit(
    deps,
    { id: admin.id, email: admin.email, ip: ctx.ip },
    'auth.mfa_failed',
    'admin',
    admin.id,
  )
  const after = lockedFor(deps, [key])
  if (after > 0) throw new AuthError('LOCKED', 429, after)
  throw new AuthError('INVALID_CODE', 401)
}

/** Alta del TOTP: genera (o reutiliza) el secreto pendiente y devuelve la URI otpauth. */
export function enrollStart(deps: AuthDeps, session: SessionInfo): { secret: string; uri: string } {
  if (session.stage !== 'enroll') throw new AuthError('WRONG_STAGE', 409)
  const admin = getAdmin(deps.db, session.adminId)!
  let secret = openTotp(deps, admin.id, admin.totp_secret)
  if (!secret) {
    secret = newTotpSecret()
    deps.db
      .prepare('UPDATE admins SET totp_secret = ? WHERE id = ?')
      .run(sealTotp(deps, admin.id, secret), admin.id)
  }
  return { secret, uri: otpauthUri(secret, admin.email) }
}

/** Confirma el alta con un código válido: activa el TOTP y entrega los códigos de recuperación. */
export function enrollConfirm(
  deps: AuthDeps,
  session: SessionInfo,
  code: string,
  ctx: LoginContext,
): { session: NewSession; recoveryCodes: string[] } {
  if (session.stage !== 'enroll') throw new AuthError('WRONG_STAGE', 409)
  const key = `mfa:${session.adminId}`
  const locked = lockedFor(deps, [key])
  if (locked > 0) throw new AuthError('LOCKED', 429, locked)
  const admin = getAdmin(deps.db, session.adminId)!
  const secret = openTotp(deps, admin.id, admin.totp_secret)
  const step = secret ? verifyTotp(secret, code, deps.now(), 0) : null
  if (step === null) {
    recordFailure(deps, key)
    throw new AuthError('INVALID_CODE', 401)
  }
  const codes = newRecoveryCodes()
  deps.db
    .prepare(
      'UPDATE admins SET totp_enabled = 1, totp_last_step = ?, recovery_codes = ? WHERE id = ?',
    )
    .run(step, JSON.stringify(codes.map((c) => sha256(normalizeRecoveryCode(c)))), admin.id)
  audit(
    deps,
    { id: admin.id, email: admin.email, ip: ctx.ip },
    'admin.totp_enabled',
    'admin',
    admin.id,
  )
  return { session: finishLogin(deps, session, ctx, 'totp_enroll'), recoveryCodes: codes }
}

/** Códigos de recuperación nuevos (invalida los anteriores). */
export function regenerateRecoveryCodes(deps: AuthDeps, adminId: number, actor: Actor): string[] {
  const codes = newRecoveryCodes()
  deps.db
    .prepare('UPDATE admins SET recovery_codes = ? WHERE id = ?')
    .run(JSON.stringify(codes.map((c) => sha256(normalizeRecoveryCode(c)))), adminId)
  audit(deps, actor, 'admin.recovery_codes', 'admin', adminId)
  return codes
}

export function remainingRecoveryCodes(db: DB, adminId: number): number {
  const admin = getAdmin(db, adminId)
  return admin ? (JSON.parse(admin.recovery_codes) as string[]).length : 0
}
