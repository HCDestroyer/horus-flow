import { randomBytes } from 'node:crypto'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { seedIfEmpty } from '../../server/lib/catalog'
import { openDatabase } from '../../server/lib/db'
import {
  publicPayments,
  readPaypalCredentials,
  readPayments,
  writeNeo,
  writePaypal,
  writeTransfer,
} from '../../server/lib/payment-config'
import { SecretBox, loadKey, parseKey } from '../../server/lib/secrets'

describe('cifrado de credenciales (AES-256-GCM)', () => {
  it('cifra y descifra; el texto cifrado no contiene el secreto y cambia en cada cifrado', () => {
    const box = new SecretBox(randomBytes(32))
    const a = box.seal('EOd-secreto-paypal', 'payment:paypal')
    const b = box.seal('EOd-secreto-paypal', 'payment:paypal')
    expect(a).not.toBe(b)
    expect(a).not.toContain('secreto')
    expect(box.open(a, 'payment:paypal')).toBe('EOd-secreto-paypal')
  })

  it('no descifra con otra clave, con otro AAD ni si se altera', () => {
    const box = new SecretBox(randomBytes(32))
    const sealed = box.seal('x', 'payment:paypal')
    expect(() => new SecretBox(randomBytes(32)).open(sealed, 'payment:paypal')).toThrow(
      /otra clave/,
    )
    expect(() => box.open(sealed, 'totp:1')).toThrow()
    const parts = sealed.split('.')
    parts[4] = Buffer.from('y').toString('base64url')
    expect(() => box.open(parts.join('.'), 'payment:paypal')).toThrow()
  })

  it('lee la clave en base64 o hex; en producción exige DATA_KEY_FILE', () => {
    const key = randomBytes(32)
    expect(parseKey(key.toString('base64'))).toEqual(key)
    expect(parseKey(key.toString('hex') + '\n')).toEqual(key)
    expect(() => parseKey('corta')).toThrow()
    const dir = mkdtempSync(join(tmpdir(), 'hf-key-'))
    writeFileSync(join(dir, 'k'), key.toString('base64'))
    expect(loadKey({ DATA_KEY_FILE: join(dir, 'k') }, dir).origin).toBe('file')
    expect(loadKey({ NODE_ENV: 'production' }, dir)).toMatchObject({ box: null, origin: 'missing' })
    expect(loadKey({}, dir).origin).toBe('dev-generated')
  })
})

describe('configuración de pagos', () => {
  it('PayPal: credenciales de solo escritura, cifradas en reposo y conservadas si no se envían', () => {
    const db = openDatabase(':memory:')
    seedIfEmpty(db)
    const box = new SecretBox(randomBytes(32))
    writePaypal(db, box, {
      enabled: true,
      mode: 'sandbox',
      webhookId: 'WH-1',
      clientId: 'AbC-client-1234',
      clientSecret: 's3cr3t-valor',
    })
    const raw = db
      .prepare(`SELECT config, secrets FROM payment_methods WHERE id = 'paypal'`)
      .get() as { config: string; secrets: string }
    expect(raw.secrets).not.toContain('s3cr3t')
    expect(raw.secrets).not.toContain('AbC-client')
    expect(raw.config).not.toContain('s3cr3t')

    const cfg = readPayments(db, box)
    expect(cfg.paypal).toMatchObject({
      enabled: true,
      hasCredentials: true,
      clientIdHint: '1234',
      webhookId: 'WH-1',
    })
    expect(JSON.stringify(cfg)).not.toContain('s3cr3t')

    // Guardar sin credenciales conserva las anteriores.
    writePaypal(db, box, { enabled: true, mode: 'live', webhookId: 'WH-2' })
    expect(readPaypalCredentials(db, box)).toEqual({
      clientId: 'AbC-client-1234',
      clientSecret: 's3cr3t-valor',
    })
    expect(publicPayments(db, box).paypal).toEqual({
      enabled: true,
      clientId: 'AbC-client-1234',
      mode: 'live',
    })

    // Con otra clave no se descifra: el método no se ofrece.
    const other = new SecretBox(randomBytes(32))
    expect(readPayments(db, other).paypal.credentialsError).toMatch(/otra clave/)
    expect(publicPayments(db, other).paypal.enabled).toBe(false)

    writePaypal(db, box, { enabled: true, mode: 'live', webhookId: 'WH-2', clearCredentials: true })
    expect(publicPayments(db, box).paypal.enabled).toBe(false)
  })

  it('sin clave de datos no se pueden guardar credenciales', () => {
    const db = openDatabase(':memory:')
    seedIfEmpty(db)
    expect(() =>
      writePaypal(db, null, {
        enabled: true,
        mode: 'sandbox',
        webhookId: '',
        clientId: 'a',
        clientSecret: 'b',
      }),
    ).toThrow('NO_DATA_KEY')
  })

  it('Neo y transferencia solo cuentan como activos si están completos', () => {
    const db = openDatabase(':memory:')
    seedIfEmpty(db)
    writeNeo(db, { enabled: true, links: {} })
    writeTransfer(db, { enabled: true, accounts: [], instructions: { es: '', en: '' } })
    expect(publicPayments(db, null)).toMatchObject({
      neo: { enabled: false },
      transfer: { enabled: false },
    })
    writeNeo(db, {
      enabled: true,
      links: { small: { monthly: 'https://pagos.neo.example/abc', annual: '' } },
    })
    writeTransfer(db, {
      enabled: true,
      accounts: [
        {
          bank: 'Banco Industrial',
          type: { es: 'Monetaria', en: 'Checking' },
          number: '000-123456-7',
          holder: 'C&S Company',
          currency: 'GTQ',
        },
      ],
      instructions: { es: 'Indica la referencia.', en: 'Include the reference.' },
    })
    expect(publicPayments(db, null)).toMatchObject({
      neo: { enabled: true },
      transfer: { enabled: true },
    })
    expect(() =>
      writeNeo(db, { enabled: true, links: { small: { monthly: 'http://inseguro.example' } } }),
    ).toThrow()
    expect(() =>
      writeNeo(db, { enabled: true, links: { small: { monthly: 'javascript:alert(1)' } } }),
    ).toThrow()
  })
})
