import { describe, expect, it } from 'vitest'
import {
  checkPoolCidr,
  cidrOverlaps,
  cidrSize,
  formatCidr,
  formatIp,
  parseCidr,
  suggestPoolCidr,
} from '~/utils/cidr'

/** Rangos de túnel del hub WireGuard: lo que la UI comprueba antes de enviar. */

const HUB = { pools: ['10.255.0.0/16'], services: '10.254.0.0/24' }

describe('parseCidr / formatCidr', () => {
  it('IPv4 e IPv6 con longitud obligatoria', () => {
    expect(parseCidr('10.1.0.0/16')).toMatchObject({ family: 4, length: 16 })
    expect(parseCidr(' fd00:1::/64 ')).toMatchObject({ family: 6, length: 64 })
    expect(parseCidr('10.1.0.0')).toBeNull()
    expect(parseCidr('10.1.0.0/33')).toBeNull()
    expect(parseCidr('10.1.0/16')).toBeNull()
    expect(parseCidr('300.1.0.0/16')).toBeNull()
    expect(parseCidr('fd00::1::/64')).toBeNull()
    expect(parseCidr('10.1.0.0/16/2')).toBeNull()
    expect(parseCidr('hola')).toBeNull()
  })

  it('normaliza a la dirección de red (IPv6 comprimida)', () => {
    expect(formatCidr(parseCidr('10.1.2.3/16')!)).toBe('10.1.0.0/16')
    expect(formatCidr(parseCidr('FD00:0:0:0:0:0:0:0/64')!)).toBe('fd00::/64')
    expect(formatIp(6, parseCidr('2001:db8:0:1::/64')!.network)).toBe('2001:db8:0:1::')
    expect(formatIp(6, parseCidr('2001:db8:1:2:3:4:5:6/128')!.network)).toBe('2001:db8:1:2:3:4:5:6')
  })

  it('direcciones utilizables (sin red ni broadcast)', () => {
    expect(cidrSize('10.255.0.0/16')).toBe(65534)
    expect(cidrSize('10.0.0.0/28')).toBe(14)
    expect(cidrSize('fd00::/120')).toBe(254)
  })
})

describe('cidrOverlaps', () => {
  it('contenidos, iguales y disjuntos; nunca entre familias', () => {
    expect(cidrOverlaps('10.0.0.0/8', '10.255.0.0/16')).toBe(true)
    expect(cidrOverlaps('10.255.0.0/16', '10.0.0.0/8')).toBe(true)
    expect(cidrOverlaps('10.255.0.0/16', '10.255.0.0/16')).toBe(true)
    expect(cidrOverlaps('10.254.0.0/16', '10.254.0.0/24')).toBe(true)
    expect(cidrOverlaps('10.253.0.0/16', '10.254.0.0/24')).toBe(false)
    expect(cidrOverlaps('fd00::/64', 'fd00::/120')).toBe(true)
    expect(cidrOverlaps('fd00::/64', '10.0.0.0/8')).toBe(false)
  })
})

describe('checkPoolCidr', () => {
  it('acepta un rango privado libre y lo normaliza', () => {
    expect(checkPoolCidr('10.253.0.0/16', HUB)).toEqual({
      ok: true,
      cidr: '10.253.0.0/16',
      family: 4,
    })
    expect(checkPoolCidr('fd00:abcd::/64', HUB)).toMatchObject({ ok: true, family: 6 })
  })

  it('vacío o mal escrito', () => {
    expect(checkPoolCidr('  ', HUB)).toMatchObject({ ok: false, error: 'required' })
    expect(checkPoolCidr('10.253.0.0', HUB)).toMatchObject({ ok: false, error: 'syntax' })
  })

  it('longitud: IPv4 /16–/28, IPv6 /64–/120', () => {
    expect(checkPoolCidr('10.0.0.0/15', HUB)).toMatchObject({ error: 'prefix', family: 4 })
    expect(checkPoolCidr('10.0.0.0/29', HUB)).toMatchObject({ error: 'prefix', family: 4 })
    expect(checkPoolCidr('10.0.0.0/28', HUB)).toMatchObject({ ok: true })
    expect(checkPoolCidr('fd00::/63', HUB)).toMatchObject({ error: 'prefix', family: 6 })
    expect(checkPoolCidr('fd00::/121', HUB)).toMatchObject({ error: 'prefix', family: 6 })
    expect(checkPoolCidr('fd00::/120', HUB)).toMatchObject({ ok: true })
  })

  it('exige la dirección de red y la propone', () => {
    expect(checkPoolCidr('10.253.4.1/16', HUB)).toEqual({
      ok: false,
      error: 'network',
      detail: '10.253.0.0/16',
      family: 4,
    })
  })

  it('rechaza el espacio CGNAT 100.64.0.0/10 (dentro o solapado)', () => {
    expect(checkPoolCidr('100.64.0.0/16', HUB)).toMatchObject({ error: 'cgnat' })
    expect(checkPoolCidr('100.127.255.0/24', HUB)).toMatchObject({ error: 'cgnat' })
    expect(checkPoolCidr('100.128.0.0/16', HUB)).toMatchObject({ ok: true })
  })

  it('sin solaparse con los rangos del hub ni con el de servicios', () => {
    expect(checkPoolCidr('10.255.128.0/17', HUB)).toEqual({
      ok: false,
      error: 'overlapPool',
      detail: '10.255.0.0/16',
      family: 4,
    })
    expect(checkPoolCidr('10.254.0.0/16', HUB)).toEqual({
      ok: false,
      error: 'overlapServices',
      detail: '10.254.0.0/24',
      family: 4,
    })
  })
})

describe('suggestPoolCidr', () => {
  it('el primer /16 libre de 10/8 bajando desde 10.255', () => {
    expect(suggestPoolCidr(['10.255.0.0/16'], '10.254.0.0/24')).toBe('10.253.0.0/16')
    expect(suggestPoolCidr(['10.255.0.0/16', '10.253.0.0/16'], '10.254.0.0/24')).toBe(
      '10.252.0.0/16',
    )
    // Un rango más pequeño dentro de un /16 también lo ocupa.
    expect(suggestPoolCidr(['10.255.0.0/16', '10.253.8.0/24'], '10.254.0.0/24')).toBe(
      '10.252.0.0/16',
    )
    expect(suggestPoolCidr([], null)).toBe('10.255.0.0/16')
  })

  it('la sugerencia siempre pasa la validación', () => {
    const suggestion = suggestPoolCidr(HUB.pools, HUB.services)!
    expect(checkPoolCidr(suggestion, HUB).ok).toBe(true)
  })

  it('null si 10/8 está entero ocupado', () => {
    expect(suggestPoolCidr(['10.0.0.0/8'])).toBeNull()
  })
})
