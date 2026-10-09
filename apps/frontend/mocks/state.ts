import type {
  ClientPrefix,
  Finding,
  KindChange,
  NotificationChannel,
  NotificationDelivery,
  ReputationSource,
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
}

/** Hallazgo cerrado por una acción del usuario: sale de los agregados de "abiertos". */
export function isClosedByUser(findingId: string) {
  const o = mockState.findings.get(findingId)
  return o?.state === 'resolved' || o?.state === 'false_positive'
}
