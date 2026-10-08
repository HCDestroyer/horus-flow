import { describe, expect, it } from 'vitest'
import { hasTenantPermission, findMembership, pickDefaultTenant } from '~/utils/permissions'
import { safeRedirect } from '~/utils/redirect'
import { USERS } from '~~/mocks/data'

const admin = USERS[0]!.me
const noc = USERS[1]!.me

describe('pickDefaultTenant', () => {
  it('abre el último ISP usado si el usuario sigue siendo miembro', () => {
    expect(pickDefaultTenant(admin, 'valle-conecta')?.tenant_slug).toBe('valle-conecta')
  })

  it('si no, el primero alfabéticamente', () => {
    expect(pickDefaultTenant(admin, 'otro-isp')?.tenant_slug).toBe('fibra-norte')
    expect(pickDefaultTenant(admin, null)?.tenant_slug).toBe('fibra-norte')
  })

  it('sin membresías no hay ISP', () => {
    expect(pickDefaultTenant({ ...noc, memberships: [] }, null)).toBeUndefined()
  })
})

describe('permisos por ISP', () => {
  it('evalúa el permiso en la membresía del ISP de la ruta', () => {
    expect(hasTenantPermission(findMembership(noc, 'fibra-norte'), 'traffic.read')).toBe(true)
    expect(hasTenantPermission(findMembership(noc, 'fibra-norte'), 'customers.read')).toBe(false)
    expect(hasTenantPermission(findMembership(noc, 'valle-conecta'), 'traffic.read')).toBe(false)
  })
})

describe('safeRedirect', () => {
  it.each([
    ['/t/fibra-norte/clients', '/t/fibra-norte/clients'],
    ['//evil.example', '/'],
    ['https://evil.example', '/'],
    ['/\\evil.example', '/'],
    ['javascript:alert(1)', '/'],
    ['/login?redirect=/x', '/'],
    [undefined, '/'],
  ])('%s → %s', (input, expected) => {
    expect(safeRedirect(input)).toBe(expected)
  })
})
