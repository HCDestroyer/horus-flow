import { describe, expect, it } from 'vitest'
import type { RemoteDestinationTestResult } from '~~/types/api'
import {
  canEnable,
  credentialsBody,
  destinationUrl,
  destinationValues,
  isValidHost,
  lagParts,
  serverChanged,
  stageStates,
  testOutcome,
  validateCredentials,
  validateDestination,
  type CredentialForm,
  type DestinationForm,
} from '~/utils/remote-destinations'

/** Reglas de los destinos remotos SFTP que la UI comprueba y presenta. */

const FORM: DestinationForm = {
  name: 'NAS de la oficina',
  host: 'nas.fibranorte.example',
  port: 22,
  username: 'horus',
  path: '/srv/copias',
  retention_days: 30,
}

const KEY = [
  '-----BEGIN OPENSSH PRIVATE KEY-----',
  'b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW',
  '-----END OPENSSH PRIVATE KEY-----',
].join('\n')

const credentials = (patch: Partial<CredentialForm>): CredentialForm => ({
  mode: 'private_key',
  password: '',
  privateKey: '',
  passphrase: '',
  ...patch,
})

describe('destinationUrl', () => {
  it('compone sftp://usuario@host:puerto/ruta', () => {
    expect(destinationUrl({ username: 'horus', host: 'nas.example', port: 22, path: '/srv' })).toBe(
      'sftp://horus@nas.example:22/srv',
    )
  })
  it('pone la IPv6 entre corchetes y añade la barra a la ruta', () => {
    expect(destinationUrl({ username: 'u', host: '2001:db8::5', port: 2222, path: 'copias' })).toBe(
      'sftp://u@[2001:db8::5]:2222/copias',
    )
  })
})

describe('lagParts', () => {
  it('elige la unidad', () => {
    expect(lagParts(null)).toBeNull()
    expect(lagParts(42)).toEqual({ key: 'platform.storage.lag.seconds', n: 42 })
    expect(lagParts(9 * 60 + 5)).toEqual({ key: 'platform.storage.lag.minutes', n: 9 })
    expect(lagParts(3 * 3600)).toEqual({ key: 'platform.storage.lag.hours', n: 3 })
    expect(lagParts(3 * 86_400)).toEqual({ key: 'platform.storage.lag.days', n: 3 })
  })
})

describe('validateDestination', () => {
  it('acepta un destino correcto y limpia espacios', () => {
    expect(validateDestination(FORM)).toEqual({})
    expect(destinationValues({ ...FORM, host: ' [2001:db8::5] ', name: ' NAS ' })).toMatchObject({
      host: '2001:db8::5',
      name: 'NAS',
    })
  })
  it('explica los errores habituales del host', () => {
    expect(validateDestination({ ...FORM, host: 'sftp://nas.example' }).host).toBe('hostScheme')
    expect(validateDestination({ ...FORM, host: 'horus@nas.example' }).host).toBe('hostUser')
    expect(validateDestination({ ...FORM, host: 'nas.example:22' }).host).toBe('host')
    expect(validateDestination({ ...FORM, host: '300.1.1.1' }).host).toBe('host')
    expect(isValidHost('192.0.2.10')).toBe(true)
    expect(isValidHost('[2001:db8::1]')).toBe(true)
  })
  it('puerto, usuario, ruta absoluta y retención en rango', () => {
    const e = validateDestination({
      name: '',
      host: 'nas',
      port: 70000,
      username: 'con espacio',
      path: 'relativa',
      retention_days: 0,
    })
    expect(e).toEqual({
      name: 'name',
      port: 'port',
      username: 'username',
      path: 'pathAbsolute',
      retention_days: 'retention',
    })
    expect(validateDestination({ ...FORM, retention_days: 3651 }).retention_days).toBe('retention')
    expect(validateDestination({ ...FORM, port: null }).port).toBe('port')
  })
})

describe('serverChanged / canEnable', () => {
  it('solo host o puerto obligan a volver a probar', () => {
    const base = { host: 'nas.example', port: 22 }
    expect(serverChanged(base, { host: 'NAS.example', port: 22 })).toBe(false)
    expect(serverChanged(base, { host: 'otro.example', port: 22 })).toBe(true)
    expect(serverChanged(base, { host: 'nas.example', port: 2222 })).toBe(true)
  })
  it('activar exige credenciales y huella confirmada', () => {
    expect(canEnable({ has_credentials: true, host_key_fingerprint: 'SHA256:x' })).toBe(true)
    expect(canEnable({ has_credentials: false, host_key_fingerprint: 'SHA256:x' })).toBe(false)
    expect(canEnable({ has_credentials: true, host_key_fingerprint: null })).toBe(false)
  })
})

describe('credenciales write-only', () => {
  it('contraseña obligatoria', () => {
    expect(validateCredentials(credentials({ mode: 'password' }))).toBe('passwordRequired')
    expect(validateCredentials(credentials({ mode: 'password', password: 'x' }))).toBeNull()
  })
  it('clave privada OpenSSH: rechaza la pública, PuTTY y texto suelto', () => {
    expect(validateCredentials(credentials({}))).toBe('keyRequired')
    expect(validateCredentials(credentials({ privateKey: 'ssh-ed25519 AAAAC3Nz horus' }))).toBe(
      'keyIsPublic',
    )
    expect(
      validateCredentials(credentials({ privateKey: 'PuTTY-User-Key-File-3: ssh-ed25519' })),
    ).toBe('keyPutty')
    expect(validateCredentials(credentials({ privateKey: 'hola' }))).toBe('keyFormat')
    expect(validateCredentials(credentials({ privateKey: `  ${KEY}\n` }))).toBeNull()
  })
  it('el cuerpo lleva solo lo del modo elegido', () => {
    expect(
      credentialsBody(credentials({ mode: 'password', password: 's', privateKey: KEY })),
    ).toEqual({ password: 's' })
    expect(credentialsBody(credentials({ privateKey: KEY }))).toEqual({ private_key: `${KEY}\n` })
    expect(credentialsBody(credentials({ privateKey: KEY, passphrase: 'pp' }))).toEqual({
      private_key: `${KEY}\n`,
      passphrase: 'pp',
    })
  })
})

describe('resultado de la prueba de conexión', () => {
  const result = (r: Partial<RemoteDestinationTestResult>): RemoteDestinationTestResult => ({
    ok: false,
    stage: 'connect',
    host_key_fingerprint: null,
    host_key_confirmed: false,
    error: null,
    ...r,
  })

  it('correcta: todos los pasos hechos', () => {
    const r = result({ ok: true, stage: 'done', host_key_fingerprint: 'SHA256:a' })
    expect(testOutcome(r)).toBe('ok')
    expect(Object.values(stageStates(r))).toEqual(['done', 'done', 'done', 'done', 'done'])
  })

  it('primera conexión (TOFU): espera a confirmar la huella', () => {
    const r = result({ stage: 'host_key', host_key_fingerprint: 'SHA256:a' })
    expect(testOutcome(r)).toBe('confirm')
    expect(stageStates(r)).toMatchObject({
      connect: 'done',
      host_key: 'waiting',
      auth: 'skipped',
      write: 'skipped',
    })
  })

  it('huella distinta de la confirmada: la clave del servidor cambió', () => {
    const r = result({ stage: 'host_key', host_key_fingerprint: 'SHA256:b', error: 'cambió' })
    expect(testOutcome(r, 'SHA256:a')).toBe('mismatch')
    expect(testOutcome({ ...r, destination: { host_key_fingerprint: 'SHA256:a' } as never })).toBe(
      'mismatch',
    )
    expect(testOutcome(r, 'SHA256:b')).toBe('confirm')
  })

  it('fallo de autenticación o escritura', () => {
    const r = result({ stage: 'auth', host_key_fingerprint: 'SHA256:a', host_key_confirmed: true })
    expect(testOutcome(r)).toBe('failed')
    expect(stageStates(r)).toMatchObject({ connect: 'done', host_key: 'done', auth: 'failed' })
    expect(testOutcome(result({ stage: 'connect', error: 'timeout' }))).toBe('failed')
  })
})
