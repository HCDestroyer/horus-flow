import { describe, expect, it } from 'vitest'
import {
  buildNavigation,
  canSeeSection,
  findSection,
  NAV_SECTIONS,
  tenantSwitchPath,
} from '~/utils/navigation'
import { USERS } from '~~/mocks/data'

const [admin, noc] = USERS.map((u) => u.me) as [(typeof USERS)[0]['me'], (typeof USERS)[1]['me']]

function ids(groups: ReturnType<typeof buildNavigation>) {
  return groups.flatMap((g) => g.sections.map((s) => s.id))
}

describe('buildNavigation', () => {
  it('oculta (no deshabilita) las secciones sin permiso de lectura', () => {
    const visible = ids(buildNavigation(noc, 'fibra-norte', 'I1'))
    expect(visible).toContain('nodes')
    expect(visible).toContain('findings')
    expect(visible).not.toContain('clients') // sin customers.read
    expect(visible).not.toContain('kiosks') // sin kiosks.manage
    expect(visible).not.toContain('users') // sin users.read
  })

  it('elimina los grupos que se quedan vacíos', () => {
    const groups = buildNavigation(noc, 'fibra-norte', 'I1').map((g) => g.id)
    expect(groups).not.toContain('admin')
    expect(groups).not.toContain('platform')
  })

  it('muestra la consola de plataforma solo con permisos platform.*', () => {
    const groups = buildNavigation(admin, 'fibra-norte', 'I1').map((g) => g.id)
    expect(groups).toEqual(['main', 'operation', 'security', 'admin', 'platform'])
  })

  it('aplica la aparición progresiva por incremento', () => {
    const i0 = ids(buildNavigation(admin, 'fibra-norte', 'I0'))
    expect(i0).toEqual(['overview', 'nodes', 'users', 'platform-isps', 'platform-users'])
    const i1 = ids(buildNavigation(admin, 'fibra-norte', 'I1'))
    expect(i1).not.toContain('investigate') // I2
    expect(i1).not.toContain('alerts') // I3
    expect(ids(buildNavigation(admin, 'fibra-norte', 'I3'))).toContain('alerts')
  })

  it('genera las rutas del ISP bajo /t/:slug y las de plataforma absolutas', () => {
    const sections = buildNavigation(admin, 'valle-conecta', 'I1').flatMap((g) => g.sections)
    expect(sections.find((s) => s.id === 'overview')?.href).toBe('/t/valle-conecta')
    expect(sections.find((s) => s.id === 'findings')?.href).toBe(
      '/t/valle-conecta/security/findings',
    )
    expect(sections.find((s) => s.id === 'platform-isps')?.href).toBe('/platform/isps')
  })

  it('no muestra nada de un ISP del que el usuario no es miembro', () => {
    expect(buildNavigation(noc, 'valle-conecta', 'I1')).toEqual([])
    expect(buildNavigation(null, 'fibra-norte', 'I1')).toEqual([])
  })
})

describe('mapa de secciones', () => {
  it('tiene ids y rutas únicos', () => {
    const idSet = new Set(NAV_SECTIONS.map((s) => s.id))
    const pathSet = new Set(NAV_SECTIONS.map((s) => `${s.scope}:${s.path}`))
    expect(idSet.size).toBe(NAV_SECTIONS.length)
    expect(pathSet.size).toBe(NAV_SECTIONS.length)
  })

  it('encuentra secciones por ruta', () => {
    expect(findSection('tenant', 'security/findings')?.id).toBe('findings')
    expect(findSection('platform', '/platform/system')?.id).toBe('platform-system')
    expect(findSection('tenant', 'no-existe')).toBeUndefined()
  })

  it('una sección de plataforma sin permiso no es visible', () => {
    const system = findSection('platform', '/platform/system')!
    expect(canSeeSection(system, noc)).toBe(false)
    expect(canSeeSection(system, admin)).toBe(true)
  })
})

describe('cambio de ISP (frontend.md §3.2)', () => {
  it('conserva la sección si existe y es visible en el ISP nuevo', () => {
    expect(tenantSwitchPath('/t/fibra-norte/security/findings', 'valle-conecta', admin, 'I1')).toBe(
      '/t/valle-conecta/security/findings',
    )
  })

  it('descarta los detalles (IDs del ISP anterior) y los filtros', () => {
    expect(
      tenantSwitchPath(
        '/t/fibra-norte/dashboards/0192f000-x?range=6h',
        'valle-conecta',
        admin,
        'I1',
      ),
    ).toBe('/t/valle-conecta/dashboards')
  })

  it('si la sección no es visible en el ISP nuevo, va a su inicio', () => {
    // En Red Andina Ana es security_analyst: sin users.read.
    expect(tenantSwitchPath('/t/fibra-norte/admin/users', 'red-andina', admin, 'I1')).toBe(
      '/t/red-andina',
    )
    expect(tenantSwitchPath('/t/fibra-norte', 'red-andina', admin, 'I1')).toBe('/t/red-andina')
  })
})
