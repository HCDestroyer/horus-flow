import type { Me, Membership } from '~~/shared/api/types'

/**
 * Datos sintéticos de la API simulada (conventions.md §5: nunca datos reales de abonados).
 * Contraseña común de demo: ver `MOCK_PASSWORD`. TOTP de demo: `MOCK_TOTP_CODE`.
 */

export const MOCK_PASSWORD = 'horus-demo-2026'
export const MOCK_TOTP_CODE = '123456'

const ALL_TENANT_PERMISSIONS = [
  'users.read',
  'users.manage',
  'roles.read',
  'audit.read',
  'sites.read',
  'sites.create',
  'devices.read',
  'devices.create',
  'wireguard.read',
  'flows.read',
  'customers.read',
  'traffic.read',
  'security.findings.read',
  'security.findings.manage',
  'security.evidence.read',
  'alerts.read',
  'reports.read',
  'dashboards.read',
  'dashboards.manage',
  'kiosks.manage',
  'settings.read',
  'settings.manage',
]

/** Rol `noc` de security.md §6.2: sin `customers.read`, `kiosks.manage` ni administración. */
const NOC_PERMISSIONS = [
  'sites.read',
  'devices.read',
  'wireguard.read',
  'flows.read',
  'traffic.read',
  'security.findings.read',
  'alerts.read',
  'reports.read',
  'dashboards.read',
]

function scoped(perms: string[]): Membership['permissions_with_scope'] {
  return Object.fromEntries(perms.map((p) => [p, ['*']]))
}

export const TENANTS = {
  fibraNorte: {
    tenant_id: '01926b3e-0000-7000-8000-000000000001',
    tenant_slug: 'fibra-norte',
    tenant_name: 'Fibra Norte',
  },
  valleConecta: {
    tenant_id: '01926b3e-0000-7000-8000-000000000002',
    tenant_slug: 'valle-conecta',
    tenant_name: 'Valle Conecta',
  },
} as const

export interface MockUser {
  me: Me
  password: string
  mfa: boolean
}

export const USERS: MockUser[] = [
  {
    // Administradora de dos ISP y de la plataforma; 2FA obligatorio (security.md §4.3).
    password: MOCK_PASSWORD,
    mfa: true,
    me: {
      id: '01926b3e-1111-7000-8000-000000000001',
      email: 'ana.ruiz@fibranorte.example',
      display_name: 'Ana Ruiz',
      locale: 'es',
      time_zone: 'America/Bogota',
      mfa_enabled: true,
      platform_roles: ['platform_admin'],
      platform_permissions: [
        'platform.tenants.read',
        'platform.tenants.manage',
        'platform.users.read',
        'platform.users.manage',
        'platform.status.read',
        'platform.storage.manage',
      ],
      memberships: [
        {
          ...TENANTS.fibraNorte,
          roles: ['tenant_admin'],
          permissions_with_scope: scoped(ALL_TENANT_PERMISSIONS),
        },
        {
          ...TENANTS.valleConecta,
          roles: ['tenant_admin'],
          permissions_with_scope: scoped(ALL_TENANT_PERMISSIONS),
        },
      ],
    },
  },
  {
    // Operador NOC de un solo ISP, sin 2FA (recomendado, no obligatorio para `noc`).
    password: MOCK_PASSWORD,
    mfa: false,
    me: {
      id: '01926b3e-1111-7000-8000-000000000002',
      email: 'noc@fibranorte.example',
      display_name: 'Luis Prieto',
      locale: 'es',
      time_zone: 'America/Bogota',
      mfa_enabled: false,
      platform_roles: [],
      platform_permissions: [],
      memberships: [
        {
          ...TENANTS.fibraNorte,
          roles: ['noc'],
          permissions_with_scope: scoped(NOC_PERMISSIONS),
        },
      ],
    },
  },
]
