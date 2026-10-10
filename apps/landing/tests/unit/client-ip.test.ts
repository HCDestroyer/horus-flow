import { describe, expect, it } from 'vitest'
import { buildTrustList, normalizeIp, resolveClientIp } from '../../server/utils/client-ip'

describe('normalizeIp', () => {
  it('quita el prefijo IPv4 mapeado y los corchetes', () => {
    expect(normalizeIp('::ffff:203.0.113.7')).toBe('203.0.113.7')
    expect(normalizeIp('[2001:db8::1]')).toBe('2001:db8::1')
    expect(normalizeIp(' 198.51.100.2 ')).toBe('198.51.100.2')
    expect(normalizeIp(undefined)).toBe('')
  })
})

describe('resolveClientIp', () => {
  const trust = buildTrustList(['10.0.0.0/8', '172.18.0.1', 'fd00::/8'])

  it('sin proxies de confianza usa la IP del socket e ignora X-Forwarded-For', () => {
    expect(resolveClientIp('203.0.113.7', '1.2.3.4', null)).toBe('203.0.113.7')
  })

  it('ignora la cabecera si el socket no es un proxy de confianza (cabecera falsificada)', () => {
    expect(resolveClientIp('203.0.113.7', '1.2.3.4', trust)).toBe('203.0.113.7')
  })

  it('detrás de un proxy de confianza toma la última IP no confiable', () => {
    expect(resolveClientIp('172.18.0.1', '198.51.100.9', trust)).toBe('198.51.100.9')
    // El cliente intenta colar una IP a la izquierda: se toma la añadida por el proxy.
    expect(resolveClientIp('172.18.0.1', '1.2.3.4, 198.51.100.9', trust)).toBe('198.51.100.9')
  })

  it('salta una cadena de proxies de confianza', () => {
    expect(resolveClientIp('::ffff:172.18.0.1', '198.51.100.9, 10.1.2.3', trust)).toBe(
      '198.51.100.9',
    )
  })

  it('soporta IPv6', () => {
    expect(resolveClientIp('fd00::5', '2001:db8::42', trust)).toBe('2001:db8::42')
  })

  it('una entrada no válida corta el recorrido', () => {
    expect(resolveClientIp('172.18.0.1', 'basura, 10.1.2.3', trust)).toBe('10.1.2.3')
  })

  it('sin cabecera devuelve el proxy', () => {
    expect(resolveClientIp('172.18.0.1', undefined, trust)).toBe('172.18.0.1')
  })

  it('descarta entradas de configuración no válidas', () => {
    expect(buildTrustList(['no-es-ip', '10.0.0.0/99'])).toBeNull()
    expect(buildTrustList([])).toBeNull()
  })
})
