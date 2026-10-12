import type {
  ClientPrefix,
  CredentialStatus,
  Member,
  Finding,
  KindChange,
  NotificationChannel,
  NotificationDelivery,
  ReputationSource,
  PlatformRoleKey,
  PlatformUserStatus,
  Role,
} from '~~/types/api'

/**
 * Cambios hechos en la API simulada (las acciones del usuario) sobre la base determinista.
 * Viven en memoria de la pestaña (se pierden al recargar), salvo los kioscos, que necesitan
 * sobrevivir a la recarga de `/kiosk` y se guardan en `localStorage` desde `mocks/kiosk.ts`.
 */
export interface CustomerOverride {
  version: number
  kind?: KindChange['to_kind']
  kind_source?: 'default' | 'scoring' | 'manual'
  kind_locked?: boolean
  kind_changed_at?: string | null
  kind_confidence?: number | null
  alias?: string | null
  notes?: string | null
  reset_at?: string | null
}

export interface FindingOverride {
  version: number
  state: Finding['state']
  acknowledged_at?: string | null
  acknowledged_by?: string | null
  resolution?: Finding['resolution']
  updated_at: string
}

export interface OnboardingRun {
  /** Instante en que se generó el script (ms). */
  scriptAt: number
  tokenId: string
  expiresAt: number
  revokedAt?: number
}

/** Campos editables de un nodo (`SiteInput`) guardados por la API simulada. */
export interface SiteFields {
  name: string
  code?: string | null
  kind?: 'node' | 'region' | 'datacenter' | 'other'
  address?: string | null
  latitude?: number | null
  longitude?: number | null
  timezone?: string | null
  tags?: string[]
}

/** Campos de un router (`RouterInput` + `admin_state`) guardados por la API simulada. */
export interface RouterFields {
  name: string
  display_name?: string | null
  is_primary?: boolean
  model?: string | null
  routeros_version?: string | null
  admin_state?: 'active' | 'maintenance' | 'decommissioned'
  tags?: string[]
}

/** Nodo dado de alta desde la UI (`POST /sites`). */
export interface CreatedSite {
  id: string
  /** Índice estable (para realms y direcciones sintéticas). */
  index: number
  created_at: string
}

/** Router dado de alta desde la UI (`POST /routers`). */
export interface CreatedRouter {
  id: string
  site_id: string
  index: number
  created_at: string
}

/** Edición (`PATCH`) sobre un nodo o router: versión y campos cambiados. */
export interface Edit<T> {
  version: number
  updated_at: string
  fields: Partial<T>
}

/** Altas, ediciones y bajas de nodos y routers de un ISP (sobre la base determinista). */
export interface DeviceState {
  sites: CreatedSite[]
  siteEdits: Map<string, Edit<SiteFields>>
  deletedSites: Set<string>
  routers: CreatedRouter[]
  routerEdits: Map<string, Edit<RouterFields>>
  deletedRouters: Set<string>
  /** Estado de credenciales (sin secretos) por router. */
  credentials: Map<string, CredentialStatus[]>
}

export function emptyDeviceState(): DeviceState {
  return {
    sites: [],
    siteEdits: new Map(),
    deletedSites: new Set(),
    routers: [],
    routerEdits: new Map(),
    deletedRouters: new Set(),
    credentials: new Map(),
  }
}

export const mockState = {
  findings: new Map<string, FindingOverride>(),
  customers: new Map<string, CustomerOverride>(),
  kindHistory: new Map<string, KindChange[]>(),
  /** Primer `set-kind` de un cliente con tipo detectado devuelve 412 (cambio concurrente). */
  kindConflictServed: new Set<string>(),
  prefixes: new Map<string, ClientPrefix[]>(),
  onboarding: new Map<string, OnboardingRun>(),
  channels: new Map<string, NotificationChannel[]>(),
  deliveries: new Map<string, NotificationDelivery[]>(),
  reputation: null as ReputationSource[] | null,
  /** Hallazgos nuevos emitidos por el tiempo real simulado, por ISP. */
  liveFindings: new Map<string, number[]>(),
  /** Miembros por ISP (`mocks/members.ts`). */
  members: new Map<string, Member[]>(),
  /** Último token de invitación de cada miembro (los anteriores dejan de valer). */
  invitationTokens: new Map<string, string>(),
  /** Tokens de invitación ya aceptados (un solo uso). */
  usedInvitations: new Set<string>(),
  /** Roles propios por ISP (`mocks/roles.ts`; también en el storage de la sesión simulada). */
  customRoles: new Map<string, Role[]>(),
  /** Roles cuyo primer `PATCH` ya devolvió el 412 simulado (`horus.mock.roleConflict`). */
  roleConflictServed: new Set<string>(),
  /** Cambios de la consola de plataforma sobre las cuentas (estado y roles de plataforma). */
  platformUsers: new Map<
    string,
    { status?: PlatformUserStatus; platform_roles?: PlatformRoleKey[] }
  >(),
  /** Nodos y routers dados de alta, editados o borrados, por ISP (`mocks/devices.ts`). */
  devices: new Map<string, DeviceState>(),
}

export function resetMockState() {
  mockState.findings.clear()
  mockState.customers.clear()
  mockState.kindHistory.clear()
  mockState.kindConflictServed.clear()
  mockState.prefixes.clear()
  mockState.onboarding.clear()
  mockState.channels.clear()
  mockState.deliveries.clear()
  mockState.reputation = null
  mockState.liveFindings.clear()
  mockState.members.clear()
  mockState.invitationTokens.clear()
  mockState.usedInvitations.clear()
  mockState.customRoles.clear()
  mockState.roleConflictServed.clear()
  mockState.platformUsers.clear()
  mockState.devices.clear()
}

/** Hallazgo cerrado por una acción del usuario: sale de los agregados de "abiertos". */
export function isClosedByUser(findingId: string) {
  const o = mockState.findings.get(findingId)
  return o?.state === 'resolved' || o?.state === 'false_positive'
}
