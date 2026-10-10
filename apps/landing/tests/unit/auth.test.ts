import { randomBytes } from 'node:crypto'
import { beforeEach, describe, expect, it } from 'vitest'
import {
  base32Decode,
  base32Encode,
  hashPassword,
  totpAt,
  totpStep,
  verifyPassword,
  verifyTotp,
} from '../../server/lib/auth/crypto'
import {
  AuthError,
  SESSION,
  createAdmin,
  enrollConfirm,
  enrollStart,
  lockMs,
  login,
  resetTotp,
  resolveSession,
  setAdminDisabled,
  verifySecondFactor,
  type AuthDeps,
} from '../../server/lib/auth/service'
import { bootstrapAdmin } from '../../server/lib/context'
import { openDatabase } from '../../server/lib/db'
import { SecretBox } from '../../server/lib/secrets'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const PASSWORD = 'correcto-caballo-bateria'
const ctx = { ip: '203.0.113.9', userAgent: 'vitest' }

function setup() {
  let now = Date.UTC(2026, 9, 10, 12, 0, 0)
  const deps: AuthDeps = {
    db: openDatabase(':memory:'),
    box: new SecretBox(randomBytes(32)),
    now: () => now,
  }
  return {
    deps,
    advance: (ms: number) => (now += ms),
    get now() {
      return now
    },
  }
}

async function expectAuthError(p: Promise<unknown> | (() => unknown), code: string) {
  try {
    await (typeof p === 'function' ? p() : p)
  } catch (err) {
    expect(err).toBeInstanceOf(AuthError)
    expect((err as AuthError).code).toBe(code)
    return err as AuthError
  }
  throw new Error(`se esperaba ${code}`)
}

/** Alta completa: login → enroll → código → sesión full. Devuelve el secreto TOTP. */
async function enrolled(t: ReturnType<typeof setup>) {
  await createAdmin(
    t.deps,
    { email: 'ana@kns.gt', password: PASSWORD },
    { id: null, email: 'test' },
  )
  const s1 = await login(t.deps, 'ana@kns.gt', PASSWORD, ctx)
  expect(s1.stage).toBe('enroll')
  const pending = resolveSession(t.deps, s1.token)!.session
  const { secret, uri } = enrollStart(t.deps, pending)
  expect(uri).toMatch(/^otpauth:\/\/totp\/Horus%20Flow%3Aana%40kns\.gt\?secret=/)
  const { session, recoveryCodes } = enrollConfirm(
    t.deps,
    pending,
    totpAt(secret, totpStep(t.now)),
    ctx,
  )
  return { secret, session, recoveryCodes, pendingToken: s1.token }
}

describe('contraseñas (scrypt)', () => {
  it('verifica la buena y rechaza la mala; cada hash lleva su sal', async () => {
    const a = await hashPassword(PASSWORD)
    const b = await hashPassword(PASSWORD)
    expect(a).toMatch(/^scrypt\$32768\$8\$1\$/)
    expect(a).not.toBe(b)
    expect(await verifyPassword(PASSWORD, a)).toBe(true)
    expect(await verifyPassword('otra-contraseña-larga', a)).toBe(false)
  })
})

describe('TOTP (RFC 6238)', () => {
  it('coincide con los vectores de prueba del RFC (SHA-1)', () => {
    const secret = base32Encode(Buffer.from('12345678901234567890'))
    expect(base32Decode(secret).toString()).toBe('12345678901234567890')
    // RFC 6238, apéndice B (8 dígitos: 94287082 → 6 dígitos: 287082).
    expect(totpAt(secret, Math.floor(59 / 30))).toBe('287082')
    expect(totpAt(secret, Math.floor(1111111109 / 30))).toBe('081804')
    expect(totpAt(secret, Math.floor(20000000000 / 30))).toBe('353130')
  })

  it('tolera ±1 paso y no acepta dos veces el mismo paso', () => {
    const secret = base32Encode(randomBytes(20))
    const now = Date.UTC(2026, 0, 1)
    const step = totpStep(now)
    expect(verifyTotp(secret, totpAt(secret, step - 1), now)).toBe(step - 1)
    expect(verifyTotp(secret, totpAt(secret, step + 1), now)).toBe(step + 1)
    expect(verifyTotp(secret, totpAt(secret, step - 2), now)).toBeNull()
    expect(verifyTotp(secret, totpAt(secret, step), now, step)).toBeNull()
    expect(verifyTotp(secret, 'abcdef', now)).toBeNull()
  })
})

describe('acceso al panel', () => {
  let t: ReturnType<typeof setup>
  beforeEach(() => {
    t = setup()
  })

  it('primer acceso: alta del TOTP obligatoria antes de la sesión completa', async () => {
    const { session, recoveryCodes, pendingToken, secret } = await enrolled(t)
    expect(session.stage).toBe('full')
    expect(recoveryCodes).toHaveLength(10)
    // La sesión intermedia ya no vale (rotación al subir de nivel).
    expect(resolveSession(t.deps, pendingToken)).toBeNull()
    expect(resolveSession(t.deps, session.token)!.session.stage).toBe('full')
    // El secreto se guarda cifrado.
    const row = t.deps.db.prepare('SELECT totp_secret FROM admins').get() as { totp_secret: string }
    expect(row.totp_secret.startsWith('enc:v1.')).toBe(true)
    expect(row.totp_secret).not.toContain(secret)
  })

  it('accesos siguientes: contraseña y después TOTP; el código no se puede reutilizar', async () => {
    const { secret } = await enrolled(t)
    t.advance(60_000)
    const s = await login(t.deps, 'ana@kns.gt', PASSWORD, ctx)
    expect(s.stage).toBe('mfa')
    const pending = resolveSession(t.deps, s.token)!.session
    await expectAuthError(
      () => verifySecondFactor(t.deps, pending, { code: '000000' }, ctx),
      'INVALID_CODE',
    )
    const code = totpAt(secret, totpStep(t.now))
    const full = verifySecondFactor(t.deps, pending, { code }, ctx)
    expect(full.stage).toBe('full')
    expect(full.csrf).not.toBe(s.csrf)

    // Mismo código en otro login dentro del mismo paso: rechazado.
    const s2 = await login(t.deps, 'ana@kns.gt', PASSWORD, ctx)
    const p2 = resolveSession(t.deps, s2.token)!.session
    await expectAuthError(() => verifySecondFactor(t.deps, p2, { code }, ctx), 'INVALID_CODE')
  })

  it('los códigos de recuperación sirven una sola vez', async () => {
    const { recoveryCodes } = await enrolled(t)
    const s = await login(t.deps, 'ana@kns.gt', PASSWORD, ctx)
    const p = resolveSession(t.deps, s.token)!.session
    expect(
      verifySecondFactor(t.deps, p, { recoveryCode: recoveryCodes[0]!.toLowerCase() }, ctx).stage,
    ).toBe('full')
    const s2 = await login(t.deps, 'ana@kns.gt', PASSWORD, ctx)
    const p2 = resolveSession(t.deps, s2.token)!.session
    await expectAuthError(
      () => verifySecondFactor(t.deps, p2, { recoveryCode: recoveryCodes[0] }, ctx),
      'INVALID_CODE',
    )
  })

  it('una sesión a medias no puede saltarse el segundo factor', async () => {
    await enrolled(t)
    const s = await login(t.deps, 'ana@kns.gt', PASSWORD, ctx)
    const p = resolveSession(t.deps, s.token)!.session
    await expectAuthError(() => enrollStart(t.deps, p), 'WRONG_STAGE')
    // Y caduca a los 10 minutos.
    t.advance(SESSION.pendingTtlMs + 1)
    expect(resolveSession(t.deps, s.token)).toBeNull()
  })

  it('bloqueo progresivo por correo tras 5 fallos (1, 2, 4… min) y registro en la auditoría', async () => {
    await enrolled(t)
    for (let i = 0; i < 4; i++) {
      await expectAuthError(
        login(t.deps, 'ana@kns.gt', 'mala-contraseña-x', ctx),
        'INVALID_CREDENTIALS',
      )
    }
    const e = await expectAuthError(login(t.deps, 'ana@kns.gt', 'mala-contraseña-x', ctx), 'LOCKED')
    expect(e.status).toBe(429)
    expect(e.retryAfterSec).toBe(60)
    // Bloqueado incluso con la contraseña buena.
    await expectAuthError(login(t.deps, 'ana@kns.gt', PASSWORD, ctx), 'LOCKED')
    t.advance(61_000)
    await expectAuthError(login(t.deps, 'ana@kns.gt', 'mala-contraseña-x', ctx), 'LOCKED')
    expect(lockMs(6, 5)).toBe(120_000)
    expect(lockMs(20, 5)).toBe(60 * 60_000)
    t.advance(121_000)
    expect((await login(t.deps, 'ana@kns.gt', PASSWORD, ctx)).stage).toBe('mfa')
    const fails = t.deps.db
      .prepare(`SELECT COUNT(*) AS n FROM audit_log WHERE action = 'auth.login_failed'`)
      .get() as { n: number }
    expect(fails.n).toBe(6)
  })

  it('un correo inexistente responde igual que una contraseña mala', async () => {
    await expectAuthError(login(t.deps, 'nadie@kns.gt', PASSWORD, ctx), 'INVALID_CREDENTIALS')
  })

  it('sesión: caduca por inactividad y rota el identificador con un margen para peticiones en vuelo', async () => {
    const { session } = await enrolled(t)
    t.advance(SESSION.rotateMs)
    const r = resolveSession(t.deps, session.token)!
    expect(r.rotatedToken).toBeTruthy()
    expect(r.session.csrf).toBe(session.csrf)
    // El token viejo vale durante el margen…
    expect(resolveSession(t.deps, session.token)!.session.adminId).toBe(r.session.adminId)
    t.advance(SESSION.graceMs + 1)
    // …y después no.
    expect(resolveSession(t.deps, session.token)).toBeNull()
    expect(resolveSession(t.deps, r.rotatedToken!)).not.toBeNull()
    t.advance(SESSION.idleMs + 1)
    expect(resolveSession(t.deps, r.rotatedToken!)).toBeNull()
  })

  it('caducidad absoluta aunque haya actividad', async () => {
    const { session } = await enrolled(t)
    let token = session.token
    for (let elapsed = 0; elapsed < SESSION.absoluteMs; elapsed += 5 * 60_000) {
      t.advance(5 * 60_000)
      const r = resolveSession(t.deps, token)
      if (!r) break
      token = r.rotatedToken ?? token
    }
    expect(resolveSession(t.deps, token)).toBeNull()
  })

  it('restablecer el TOTP o desactivar un admin cierra sus sesiones', async () => {
    const { session } = await enrolled(t)
    const id = resolveSession(t.deps, session.token)!.session.adminId
    resetTotp(t.deps, id, { id: null, email: 'test' })
    expect(resolveSession(t.deps, session.token)).toBeNull()
    expect((await login(t.deps, 'ana@kns.gt', PASSWORD, ctx)).stage).toBe('enroll')

    const other = await createAdmin(
      t.deps,
      { email: 'luis@kns.gt', password: PASSWORD },
      { id: null, email: 'test' },
    )
    setAdminDisabled(t.deps, other, true, { id, email: 'ana@kns.gt' })
    await expectAuthError(login(t.deps, 'luis@kns.gt', PASSWORD, ctx), 'INVALID_CREDENTIALS')
    await expectAuthError(
      () => setAdminDisabled(t.deps, id, true, { id, email: 'ana@kns.gt' }),
      'CANNOT_DISABLE_SELF',
    )
  })

  it('contraseña mínima de 12 caracteres', async () => {
    await expectAuthError(
      createAdmin(t.deps, { email: 'x@kns.gt', password: 'corta' }, { id: null, email: 't' }),
      'PASSWORD_TOO_SHORT',
    )
  })
})

describe('primer administrador desde variables de entorno', () => {
  it('lo crea solo si no hay ninguno', () => {
    const t = setup()
    const dir = mkdtempSync(join(tmpdir(), 'hf-admin-'))
    const file = join(dir, 'pw')
    writeFileSync(file, PASSWORD + '\n')
    expect(bootstrapAdmin(t.deps, {})).toBe('skipped')
    expect(bootstrapAdmin(t.deps, { ADMIN_EMAIL: 'ana@kns.gt', ADMIN_PASSWORD_FILE: file })).toBe(
      'created',
    )
    expect(bootstrapAdmin(t.deps, { ADMIN_EMAIL: 'otro@kns.gt', ADMIN_PASSWORD_FILE: file })).toBe(
      'exists',
    )
    const row = t.deps.db.prepare('SELECT email, password_hash FROM admins').get() as {
      email: string
      password_hash: string
    }
    expect(row.email).toBe('ana@kns.gt')
    expect(row.password_hash).not.toContain(PASSWORD)
  })
})
