import type { Me, Membership } from '~~/types/api'

/**
 * Datos sintéticos de la API simulada (conventions.md §5: nunca datos reales de abonados).
 * Tipados con los tipos generados del OpenAPI: si el contrato cambia un campo, el mock deja
 * de compilar. Roles y permisos según el catálogo C7 (packages/schemas/permissions/v0).
 * Contraseña común de demo: `MOCK_PASSWORD`. TOTP de demo: `MOCK_TOTP_CODE`.
 */

export const MOCK_PASSWORD = 'horus-demo-2026'
export const MOCK_TOTP_CODE = '123456'

/** `tenant_admin` = todos los permisos de tenant (C7, `permissions: ['*']`). */
const TENANT_ADMIN = [
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

/** Rol `noc` (C7): sin `customers.read`, `kiosks.manage` ni administración. */
const NOC = [
  'sites.read',
  'devices.read',
  'wireguard.read',
  'snmp.read',
  'flows.read',
  'traffic.read',
  'security.findings.read',
  'alerts.read',
  'alerts.ack',
  'reports.read',
  'dashboards.read',
]

/** Rol `security_analyst` (C7): sin `users.read` ni `kiosks.manage`. */
const SECURITY_ANALYST = [
  'security.findings.read',
  'security.findings.manage',
  'security.evidence.read',
  'customers.read',
  'traffic.read',
  'traffic.customer.read',
  'alerts.read',
  'alerts.ack',
  'alerts.manage',
  'audit.read',
  'dashboards.read',
  'sites.read',
  'devices.read',
  'flows.read',
]

function scoped(perms: string[]): Membership['permissions_with_scope'] {
  return Object.fromEntries(perms.map((p) => [p, ['*']]))
}

export interface MockTenant {
  tenant_id: string
  tenant_slug: string
  tenant_name: string
  /** Nodos del ISP (para los datos de los widgets). */
  sites: { id: string; name: string }[]
  /** Prefijo de clientes sintético (RFC 1918). */
  prefix: string
}

export const TENANTS = {
  fibraNorte: {
    tenant_id: '01926b3e-0000-7000-8000-000000000001',
    tenant_slug: 'fibra-norte',
    tenant_name: 'Fibra Norte',
    prefix: '10.20',
    sites: [
      { id: '0192e111-0000-7000-8000-000000000101', name: 'Nodo Centro' },
      { id: '0192e111-0000-7000-8000-000000000102', name: 'Nodo Norte' },
      { id: '0192e111-0000-7000-8000-000000000103', name: 'Nodo Sur' },
      { id: '0192e111-0000-7000-8000-000000000104', name: 'Nodo Industrial' },
    ],
  },
  valleConecta: {
    tenant_id: '01926b3e-0000-7000-8000-000000000002',
    tenant_slug: 'valle-conecta',
    tenant_name: 'Valle Conecta',
    prefix: '10.40',
    sites: [
      { id: '0192e111-0000-7000-8000-000000000201', name: 'Nodo Valle Alto' },
      { id: '0192e111-0000-7000-8000-000000000202', name: 'Nodo Ribera' },
    ],
  },
  redAndina: {
    tenant_id: '01926b3e-0000-7000-8000-000000000003',
    tenant_slug: 'red-andina',
    tenant_name: 'Red Andina',
    prefix: '10.60',
    sites: [
      { id: '0192e111-0000-7000-8000-000000000301', name: 'Nodo Cumbre' },
      { id: '0192e111-0000-7000-8000-000000000302', name: 'Nodo Páramo' },
      { id: '0192e111-0000-7000-8000-000000000303', name: 'Nodo Laguna' },
    ],
  },
} as const satisfies Record<string, MockTenant>

export const ALL_TENANTS: readonly MockTenant[] = Object.values(TENANTS)

const ROLE_IDS: Record<string, string> = {
  tenant_admin: '0192a000-0000-7000-8000-000000000001',
  security_analyst: '0192a000-0000-7000-8000-000000000002',
  noc: '0192a000-0000-7000-8000-000000000004',
}

function membership(tenant: MockTenant, role: keyof typeof ROLE_IDS, perms: string[]): Membership {
  return {
    tenant_id: tenant.tenant_id,
    tenant_slug: tenant.tenant_slug,
    tenant_name: tenant.tenant_name,
    tenant_status: 'active',
    roles: [{ role_id: ROLE_IDS[role]!, role_key: role, scope: 'tenant' }],
    permissions_with_scope: scoped(perms),
  }
}

export interface MockUser {
  me: Me
  /** Usuario de login (`username` en `POST /auth/login`): el correo. */
  username: string
  password: string
  mfa: boolean
}

export const USERS: MockUser[] = [
  {
    // Administradora de dos ISP, analista de seguridad en un tercero y de la plataforma;
    // 2FA obligatorio (security.md §4.3).
    username: 'ana.ruiz@fibranorte.example',
    password: MOCK_PASSWORD,
    mfa: true,
    me: {
      id: '01926b3e-1111-7000-8000-000000000001',
      email: 'ana.ruiz@fibranorte.example',
      display_name: 'Ana Ruiz',
      locale: 'es',
      timezone: 'America/Bogota',
      default_tenant_id: null,
      mfa_enabled: true,
      must_change_password: false,
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
        membership(TENANTS.fibraNorte, 'tenant_admin', TENANT_ADMIN),
        membership(TENANTS.valleConecta, 'tenant_admin', TENANT_ADMIN),
        membership(TENANTS.redAndina, 'security_analyst', SECURITY_ANALYST),
      ],
    },
  },
  {
    // Operador NOC de un solo ISP, sin 2FA (recomendado, no obligatorio para `noc`).
    username: 'noc@fibranorte.example',
    password: MOCK_PASSWORD,
    mfa: false,
    me: {
      id: '01926b3e-1111-7000-8000-000000000002',
      email: 'noc@fibranorte.example',
      display_name: 'Luis Prieto',
      locale: 'es',
      timezone: 'America/Bogota',
      default_tenant_id: null,
      mfa_enabled: false,
      must_change_password: false,
      platform_roles: [],
      platform_permissions: [],
      memberships: [membership(TENANTS.fibraNorte, 'noc', NOC)],
    },
  },
]

export function findTenant(id: string | null | undefined) {
  return ALL_TENANTS.find((t) => t.tenant_id === id)
}
