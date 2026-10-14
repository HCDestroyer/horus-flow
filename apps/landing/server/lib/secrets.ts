// Cifrado en reposo de las credenciales de pago (PayPal) con AES-256-GCM. La clave (32 bytes)
// se lee de DATA_KEY_FILE: base64 o hex, p. ej. `openssl rand -base64 32 > data.key`. Nunca se
// guarda junto a la base de datos en producción. Formato: "v1.<kid>.<iv>.<tag>.<ct>" (base64url),
// con el identificador del dato (p. ej. "paypal") como AAD: un valor copiado a otra fila no
// descifra.
import { createCipheriv, createDecipheriv, createHash, randomBytes } from 'node:crypto'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

export class SecretBox {
  readonly kid: string
  constructor(private readonly key: Buffer) {
    if (key.length !== 32) throw new Error('La clave de datos debe tener 32 bytes')
    this.kid = createHash('sha256').update(key).digest('base64url').slice(0, 8)
  }

  seal(plaintext: string, aad: string): string {
    const iv = randomBytes(12)
    const cipher = createCipheriv('aes-256-gcm', this.key, iv)
    cipher.setAAD(Buffer.from(aad, 'utf8'))
    const ct = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()])
    const tag = cipher.getAuthTag()
    return [
      'v1',
      this.kid,
      iv.toString('base64url'),
      tag.toString('base64url'),
      ct.toString('base64url'),
    ].join('.')
  }

  open(sealed: string, aad: string): string {
    const [v, kid, iv, tag, ct] = sealed.split('.')
    if (v !== 'v1' || !iv || !tag || ct === undefined)
      throw new Error('Formato de secreto no válido')
    if (kid !== this.kid)
      throw new Error('El secreto se cifró con otra clave de datos (DATA_KEY_FILE)')
    const decipher = createDecipheriv('aes-256-gcm', this.key, Buffer.from(iv, 'base64url'))
    decipher.setAAD(Buffer.from(aad, 'utf8'))
    decipher.setAuthTag(Buffer.from(tag, 'base64url'))
    return Buffer.concat([
      decipher.update(Buffer.from(ct, 'base64url')),
      decipher.final(),
    ]).toString('utf8')
  }
}

/** Interpreta el contenido de un archivo de clave: 64 caracteres hex o base64 de 32 bytes. */
export function parseKey(raw: string): Buffer {
  const s = raw.trim()
  if (/^[0-9a-f]{64}$/i.test(s)) return Buffer.from(s, 'hex')
  const b = Buffer.from(s, 'base64')
  if (b.length === 32) return b
  throw new Error('DATA_KEY_FILE debe contener 32 bytes en base64 o 64 caracteres hex')
}

export interface KeySource {
  box: SecretBox | null
  /** De dónde salió la clave, para el registro y el panel. */
  origin: 'file' | 'dev-generated' | 'missing'
  error?: string
}

/**
 * Clave del proceso. En producción solo DATA_KEY_FILE. En desarrollo, si no hay, se genera una
 * en DATA_DIR/dev-data.key (nunca para producción: el volumen de datos y la clave irían juntos).
 */
export function loadKey(env: Record<string, string | undefined>, dir: string): KeySource {
  const file = env.DATA_KEY_FILE?.trim()
  if (file) {
    try {
      return { box: new SecretBox(parseKey(readFileSync(file, 'utf8'))), origin: 'file' }
    } catch (err) {
      return { box: null, origin: 'missing', error: String((err as Error).message ?? err) }
    }
  }
  if (env.NODE_ENV === 'production') {
    return { box: null, origin: 'missing', error: 'DATA_KEY_FILE no está definida' }
  }
  const dev = join(dir, 'dev-data.key')
  if (!existsSync(dev))
    writeFileSync(dev, randomBytes(32).toString('base64') + '\n', { mode: 0o600 })
  return { box: new SecretBox(parseKey(readFileSync(dev, 'utf8'))), origin: 'dev-generated' }
}
