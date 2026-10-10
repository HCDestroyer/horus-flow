// Primitivas de autenticación del panel: contraseñas con scrypt, TOTP (RFC 6238) y códigos de
// recuperación. Sin dependencias: solo node:crypto.
import {
  createHash,
  createHmac,
  randomBytes,
  scrypt as scryptCb,
  scryptSync,
  timingSafeEqual,
  type ScryptOptions,
} from 'node:crypto'

// ---- Contraseñas (scrypt) ------------------------------------------------------------------
// N = 2^15, r = 8, p = 1 → 32 MiB por cálculo (recomendación OWASP para scrypt).
const SCRYPT = { N: 2 ** 15, r: 8, p: 1, keylen: 32, maxmem: 64 * 1024 * 1024 }
export const MIN_PASSWORD_LENGTH = 12

function scrypt(
  password: string,
  salt: Buffer,
  keylen: number,
  opts: ScryptOptions,
): Promise<Buffer> {
  return new Promise((resolve, reject) =>
    scryptCb(password, salt, keylen, opts, (err, key) => (err ? reject(err) : resolve(key))),
  )
}

function encode(salt: Buffer, key: Buffer, p = SCRYPT): string {
  return `scrypt$${p.N}$${p.r}$${p.p}$${salt.toString('base64')}$${key.toString('base64')}`
}

export async function hashPassword(password: string): Promise<string> {
  const salt = randomBytes(16)
  const key = await scrypt(password.normalize('NFKC'), salt, SCRYPT.keylen, SCRYPT)
  return encode(salt, key)
}

/** Versión síncrona para la línea de órdenes (admin create). */
export function hashPasswordSync(password: string): string {
  const salt = randomBytes(16)
  return encode(salt, scryptSync(password.normalize('NFKC'), salt, SCRYPT.keylen, SCRYPT))
}

export async function verifyPassword(password: string, stored: string): Promise<boolean> {
  const [alg, n, r, p, saltB64, keyB64] = stored.split('$')
  if (alg !== 'scrypt' || !saltB64 || !keyB64) return false
  const expected = Buffer.from(keyB64, 'base64')
  const key = await scrypt(
    password.normalize('NFKC'),
    Buffer.from(saltB64, 'base64'),
    expected.length,
    {
      N: Number(n),
      r: Number(r),
      p: Number(p),
      maxmem: SCRYPT.maxmem,
    },
  )
  return key.length === expected.length && timingSafeEqual(key, expected)
}

/** Hash válido de una contraseña aleatoria: se verifica contra él si el correo no existe. */
let dummy: string | null = null
export async function dummyHash(): Promise<string> {
  dummy ??= await hashPassword(randomBytes(16).toString('hex'))
  return dummy
}

export function passwordProblem(password: string): string | null {
  if (password.length < MIN_PASSWORD_LENGTH) return 'PASSWORD_TOO_SHORT'
  if (password.length > 256) return 'PASSWORD_TOO_LONG'
  return null
}

// ---- TOTP (RFC 6238: SHA-1, 6 dígitos, 30 s) -----------------------------------------------
const B32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
export const TOTP_STEP_SECONDS = 30

export function base32Encode(buf: Buffer): string {
  let bits = 0
  let value = 0
  let out = ''
  for (const byte of buf) {
    value = (value << 8) | byte
    bits += 8
    while (bits >= 5) {
      out += B32[(value >>> (bits - 5)) & 31]
      bits -= 5
    }
  }
  if (bits > 0) out += B32[(value << (5 - bits)) & 31]
  return out
}

export function base32Decode(s: string): Buffer {
  const clean = s.toUpperCase().replace(/[\s=-]/g, '')
  let bits = 0
  let value = 0
  const out: number[] = []
  for (const ch of clean) {
    const idx = B32.indexOf(ch)
    if (idx < 0) throw new Error('base32 no válido')
    value = (value << 5) | idx
    bits += 5
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 255)
      bits -= 8
    }
  }
  return Buffer.from(out)
}

export function newTotpSecret(): string {
  return base32Encode(randomBytes(20))
}

export function totpAt(secret: string, step: number): string {
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(step))
  const mac = createHmac('sha1', base32Decode(secret)).update(counter).digest()
  const offset = mac[mac.length - 1]! & 0x0f
  const code = (mac.readUInt32BE(offset) & 0x7fffffff) % 1_000_000
  return code.toString().padStart(6, '0')
}

export const totpStep = (nowMs: number) => Math.floor(nowMs / 1000 / TOTP_STEP_SECONDS)

/**
 * Comprueba un código con ±1 paso de tolerancia. Devuelve el paso aceptado (para impedir que el
 * mismo código se use dos veces) o null.
 */
export function verifyTotp(
  secret: string,
  code: string,
  nowMs: number,
  lastStep = 0,
): number | null {
  const clean = code.replace(/\s/g, '')
  if (!/^\d{6}$/.test(clean)) return null
  const current = totpStep(nowMs)
  for (const delta of [0, -1, 1]) {
    const step = current + delta
    if (step <= lastStep) continue
    const expected = Buffer.from(totpAt(secret, step))
    if (timingSafeEqual(expected, Buffer.from(clean))) return step
  }
  return null
}

export function otpauthUri(secret: string, account: string, issuer = 'Horus Flow'): string {
  const label = encodeURIComponent(`${issuer}:${account}`)
  const params = new URLSearchParams({
    secret,
    issuer,
    algorithm: 'SHA1',
    digits: '6',
    period: '30',
  })
  return `otpauth://totp/${label}?${params.toString()}`
}

// ---- Códigos de recuperación y tokens ------------------------------------------------------
const CROCKFORD = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'

export function newRecoveryCodes(n = 10): string[] {
  return Array.from({ length: n }, () => {
    let s = ''
    for (const b of randomBytes(10)) s += CROCKFORD[b % 32]
    return `${s.slice(0, 5)}-${s.slice(5)}`
  })
}

export function normalizeRecoveryCode(code: string): string {
  return code.toUpperCase().replace(/[^0-9A-Z]/g, '')
}

export const sha256 = (s: string) => createHash('sha256').update(s).digest('hex')

export const randomToken = (bytes = 32) => randomBytes(bytes).toString('base64url')

export function safeEqual(a: string, b: string): boolean {
  const x = Buffer.from(a)
  const y = Buffer.from(b)
  return x.length === y.length && timingSafeEqual(x, y)
}
