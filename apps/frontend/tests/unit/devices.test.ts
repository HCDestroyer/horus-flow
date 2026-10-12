import { describe, expect, it } from 'vitest'
import {
  checkRouterOsVersion,
  credentialStatus,
  fieldErrors,
  mergePatch,
  normalizeFingerprint,
  nullable,
  parseCoordinate,
  routerOsReadOnlySnippet,
} from '~/utils/devices'

/** Reglas de nodos, routers y credenciales que la UI comprueba antes de enviar. */

describe('checkRouterOsVersion (D15: RouterOS 7.x ≥ 7.12)', () => {
  it('clasifica vacía, inválida, no compatible y compatible', () => {
    expect(checkRouterOsVersion('')).toBe('empty')
    expect(checkRouterOsVersion(null)).toBe('empty')
    expect(checkRouterOsVersion('6.49.10')).toBe('invalid')
    expect(checkRouterOsVersion('7')).toBe('invalid')
    expect(checkRouterOsVersion('7.16.2.1')).toBe('invalid')
    expect(checkRouterOsVersion('7.11.2')).toBe('unsupported')
    expect(checkRouterOsVersion('7.12')).toBe('ok')
    expect(checkRouterOsVersion(' 7.16.2 ')).toBe('ok')
  })
})

describe('normalizeFingerprint', () => {
  const hex = 'ab'.repeat(32)
  it('acepta 64 dígitos hex con o sin dos puntos o prefijo SHA256:', () => {
    const expected = Array(32).fill('AB').join(':')
    expect(normalizeFingerprint(hex)).toBe(expected)
    expect(normalizeFingerprint(expected.toLowerCase())).toBe(expected)
    expect(normalizeFingerprint(`SHA256:${hex}`)).toBe(expected)
  })
  it('rechaza longitudes o caracteres no válidos', () => {
    expect(normalizeFingerprint('ab'.repeat(31))).toBeNull()
    expect(normalizeFingerprint('zz'.repeat(32))).toBeNull()
    expect(normalizeFingerprint('')).toBeNull()
  })
})

describe('formularios', () => {
  it('nullable convierte vacío en null', () => {
    expect(nullable('  ')).toBeNull()
    expect(nullable(undefined)).toBeNull()
    expect(nullable(' x ')).toBe('x')
  })

  it('parseCoordinate: vacío → null; fuera de rango → undefined; coma decimal', () => {
    expect(parseCoordinate('', 90)).toBeNull()
    expect(parseCoordinate('4,71', 90)).toBe(4.71)
    expect(parseCoordinate('-74.07', 180)).toBe(-74.07)
    expect(parseCoordinate('91', 90)).toBeUndefined()
    expect(parseCoordinate('abc', 90)).toBeUndefined()
  })

  it('mergePatch envía solo lo que cambió (null y ausente son iguales)', () => {
    const original = { name: 'Nodo A', code: null, address: 'Calle 1', tags: ['a'] }
    expect(mergePatch(original, { name: 'Nodo A', code: null, address: 'Calle 1' })).toEqual({})
    expect(mergePatch(original, { name: 'Nodo B', code: 'NB', address: null })).toEqual({
      name: 'Nodo B',
      code: 'NB',
      address: null,
    })
    expect(mergePatch(original, { tags: ['a'] })).toEqual({})
    expect(mergePatch({ timezone: undefined }, { timezone: null })).toEqual({})
  })

  it('fieldErrors toma el primer mensaje de cada campo', () => {
    expect(
      fieldErrors({
        type: 'about:blank',
        title: 'x',
        status: 422,
        code: 'VALIDATION_FAILED',
        errors: [
          { field: 'name', code: 'REQUIRED', message: 'Falta el nombre' },
          { field: 'name', code: 'TOO_LONG', message: 'Otro' },
          { field: 'routeros_version', code: 'ROUTEROS_VERSION_INVALID', message: 'Solo 7.x' },
        ],
      }),
    ).toEqual({ name: 'Falta el nombre', routeros_version: 'Solo 7.x' })
    expect(fieldErrors(null)).toEqual({})
  })
})

describe('credenciales', () => {
  it('credentialStatus: lo guardado en la sesión gana; si no, el del router; si no, sin configurar', () => {
    const router = {
      credentials: [
        { kind: 'routeros_api' as const, configured: true, last_result: 'ok' as const },
      ],
    }
    expect(credentialStatus(router, 'routeros_api').configured).toBe(true)
    expect(credentialStatus(router, 'snmp')).toEqual({ kind: 'snmp', configured: false })
    const saved = { kind: 'snmp' as const, configured: true, updated_at: '2026-10-12T10:00:00Z' }
    expect(credentialStatus(router, 'snmp', saved)).toBe(saved)
    expect(credentialStatus(null, 'routeros_api', saved).configured).toBe(false)
  })

  it('los comandos de solo lectura no dan escritura ni incluyen secretos reales', () => {
    const s = routerOsReadOnlySnippet({ apiUser: 'horus-ro', snmpUser: 'horus-snmp' })
    expect(s).toContain('policy=read,api,rest-api,!write,!policy')
    expect(s).toContain('/user add name=horus-ro group=horus-ro address=10.255.0.1/32')
    expect(s).toContain('/snmp community add name=horus-snmp security=private')
    expect(s).toContain('write-access=no')
    // SHA256 no existe en todas las versiones de RouterOS 7 ("syntax error").
    expect(s).toContain('authentication-protocol=SHA1')
    expect(s).not.toMatch(/write-access=yes|policy=[^ ]*(?<!!)write/)
  })
})
