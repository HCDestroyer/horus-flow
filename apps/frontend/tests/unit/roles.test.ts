import { describe, expect, it } from 'vitest'
import { hasTenantWidePermission } from '~/utils/permissions'
import {
  deriveRoleKey,
  groupPermissions,
  isValidRoleKey,
  permissionArea,
  roleHasPermission,
  rolePermissionKeys,
  sortRoles,
} from '~/utils/roles'
import type { Membership } from '~~/types/api'

describe('deriveRoleKey', () => {
  it('minúsculas, sin acentos y espacios → _', () => {
    expect(deriveRoleKey('Técnico de campo')).toBe('tecnico_de_campo')
    expect(deriveRoleKey('Señalización ÁREA Norte')).toBe('senalizacion_area_norte')
  })

  it('símbolos y espacios repetidos colapsan en un solo _ y no quedan en los extremos', () => {
    expect(deriveRoleKey('  NOC  (nocturno) / fin de semana!  ')).toBe('noc_nocturno_fin_de_semana')
  })

  it('empieza por una letra y no pasa de 63 caracteres', () => {
    expect(deriveRoleKey('24x7 soporte')).toBe('x7_soporte')
    expect(deriveRoleKey('_1 nivel')).toBe('nivel')
    const long = deriveRoleKey('a'.repeat(80))
    expect(long).toHaveLength(63)
    expect(isValidRoleKey(long)).toBe(true)
  })

  it('el resultado de un nombre normal cumple el patrón del contrato', () => {
    for (const name of ['Soporte N1', 'Gestión de clientes', 'Auditor externo 2026']) {
      expect(isValidRoleKey(deriveRoleKey(name))).toBe(true)
    }
    expect(deriveRoleKey('¡¡!!')).toBe('')
  })
})

describe('isValidRoleKey', () => {
  it('aplica ^[a-z][a-z0-9_]{1,62}$', () => {
    expect(isValidRoleKey('soporte_n1')).toBe(true)
    expect(isValidRoleKey('a')).toBe(false)
    expect(isValidRoleKey('1soporte')).toBe(false)
    expect(isValidRoleKey('Soporte')).toBe(false)
    expect(isValidRoleKey('soporte-n1')).toBe(false)
    expect(isValidRoleKey(`a${'b'.repeat(63)}`)).toBe(false)
  })
})

describe('áreas de permisos', () => {
  const catalog = [
    { key: 'users.read' },
    { key: 'users.manage' },
    { key: 'devices.read' },
    { key: 'devices.credentials.write' },
    { key: 'security.findings.read' },
    { key: 'users.extra' },
  ]

  it('el área es el prefijo de la clave', () => {
    expect(permissionArea('devices.credentials.write')).toBe('devices')
    expect(permissionArea('security.findings.read')).toBe('security')
    expect(permissionArea('sinpunto')).toBe('sinpunto')
  })

  it('agrupa en el orden del catálogo (primera aparición de cada área)', () => {
    const groups = groupPermissions(catalog)
    expect(groups.map((g) => g.id)).toEqual(['users', 'devices', 'security'])
    expect(groups[0]!.permissions.map((p) => p.key)).toEqual([
      'users.read',
      'users.manage',
      'users.extra',
    ])
  })

  it('"*" incluye todos los permisos del catálogo', () => {
    expect(roleHasPermission({ permissions: ['*'] }, 'devices.read')).toBe(true)
    expect(roleHasPermission({ permissions: ['users.read'] }, 'devices.read')).toBe(false)
    expect(rolePermissionKeys({ permissions: ['*'] }, catalog)).toHaveLength(catalog.length)
    expect(rolePermissionKeys({ permissions: ['devices.read', 'otro'] }, catalog)).toEqual([
      'devices.read',
    ])
  })
})

describe('sortRoles', () => {
  it('sistema primero (en su orden) y luego los propios por nombre', () => {
    const roles = [
      { name: 'Zeta', is_system: false },
      { name: 'Administrador del ISP', is_system: true },
      { name: 'Ámbar', is_system: false },
      { name: 'Operador NOC', is_system: true },
    ]
    expect(sortRoles(roles).map((r) => r.name)).toEqual([
      'Administrador del ISP',
      'Operador NOC',
      'Ámbar',
      'Zeta',
    ])
  })
})

describe('hasTenantWidePermission (anti-escalada)', () => {
  const membership = {
    permissions_with_scope: {
      'users.read': ['*'],
      'devices.read': ['tenant'],
      'customers.kind.write': ['site:0192e111-0000-7000-8000-000000000101'],
    },
  } as unknown as Membership

  it('solo cuenta el alcance de todo el ISP', () => {
    expect(hasTenantWidePermission(membership, 'users.read')).toBe(true)
    expect(hasTenantWidePermission(membership, 'devices.read')).toBe(true)
    expect(hasTenantWidePermission(membership, 'customers.kind.write')).toBe(false)
    expect(hasTenantWidePermission(membership, 'roles.manage')).toBe(false)
    expect(hasTenantWidePermission(undefined, 'users.read')).toBe(false)
  })
})
