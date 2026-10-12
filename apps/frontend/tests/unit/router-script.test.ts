import { describe, expect, it } from 'vitest'
import { splitRouterScript } from '../../app/utils/router-script'

const SCRIPT = [
  '# ===== Horus Flow · onboarding =====',
  '# Revise el script antes de pegarlo.',
  '',
  '# 1) Túnel WireGuard hacia Horus',
  '/interface wireguard add name=wg-horus comment="horus"',
  '/interface wireguard peers add interface=wg-horus \\',
  '    endpoint-address=horus.example.net comment="horus"',
  '',
  '# 4) Certificado autofirmado',
  '/certificate add name=horus-api common-name=10.255.1.2',
  '/certificate sign horus-api',
  '/ip service set www-ssl certificate=horus-api disabled=no',
  '',
  '# 7) Enrolamiento',
  '{',
  '    :local pk [/interface wireguard get [find name=wg-horus] public-key]',
  '# 8) dentro del bloque no se corta',
  '    /tool fetch url="https://horus.example.net/api/v1/enroll/wireguard" output=none',
  '}',
  '',
].join('\n')

describe('splitRouterScript', () => {
  it('parte por encabezados, corta tras /certificate sign y no rompe bloques', () => {
    const parts = splitRouterScript(SCRIPT)
    expect(parts.map((p) => p.title)).toEqual([
      'Túnel WireGuard hacia Horus',
      'Certificado autofirmado',
      'Certificado autofirmado',
      'Enrolamiento',
    ])
    expect(parts[0]!.code).toContain('endpoint-address=horus.example.net')
    expect(parts[1]!.code.trimEnd().endsWith('/certificate sign horus-api')).toBe(true)
    expect(parts[1]!.waitAfter).toBe(true)
    expect(parts[2]!.code).toMatch(/^\/ip service set www-ssl/)
    expect(parts[3]!.code).toContain('/tool fetch')
    expect(parts[3]!.code.trimEnd().endsWith('}')).toBe(true)
    // Nada se pierde: todas las órdenes siguen en alguna parte.
    const all = parts.map((p) => p.code).join('')
    for (const l of SCRIPT.split('\n').filter((x) => x.startsWith('/'))) expect(all).toContain(l)
  })

  it('un script sin encabezados es una sola parte', () => {
    expect(splitRouterScript('/a\n/b\n')).toHaveLength(1)
  })
})
