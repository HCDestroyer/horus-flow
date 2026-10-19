import type { Router, SwitchPort } from '~~/types/api'
import type { ProtectionLevel } from '~/utils/transport'

/**
 * Switches de monitoreo (docs/vendors/mikrotik.md §13, huawei.md §11): plataformas, límites y
 * ayudas de presentación compartidas por la lista, la ficha y el asistente de alta.
 */
export const SWITCH_PLATFORMS = ['routeros', 'swos', 'vrp'] as const
export type SwitchPlatform = (typeof SWITCH_PLATFORMS)[number]

/** Máximo de puertos monitoreados por switch (cardinalidad de las series, snmp.yaml). */
export const MAX_SWITCH_PORTS = 64
/** Cambios de estado en una hora a partir de los cuales un puerto se considera inestable. */
export const FLAP_THRESHOLD = 4
/** Puntos de la tendencia en vivo por puerto (un punto por sondeo). */
export const PORT_TREND_POINTS = 30

export const PLATFORM_ICON: Record<SwitchPlatform, string> = {
  routeros: 'i-lucide-network',
  swos: 'i-lucide-ethernet-port',
  vrp: 'i-lucide-server',
}

/** SwOS solo habla SNMP v1/v2c (comunidad en claro). */
export const isV2cOnly = (p: string | null | undefined) => p === 'swos'
/** Solo la serie S de Huawei exporta sFlow (RouterOS no tiene sFlow; SwOS no exporta flujos). */
export const sflowSupported = (p: string | null | undefined) => p === 'vrp'

/** Utilización máxima (rx/tx) de la última muestra; null sin muestra. */
export function portUtilization(p: Pick<SwitchPort, 'sample'>): number | null {
  const s = p.sample
  if (!s || (s.utilization_rx === null && s.utilization_tx === null)) return null
  return Math.max(s.utilization_rx ?? 0, s.utilization_tx ?? 0)
}

/** Orden de la tabla de puertos: subida primero, después monitoreados, después por ifIndex. */
export function sortPorts(list: SwitchPort[]): SwitchPort[] {
  return [...list].sort(
    (a, b) =>
      Number(b.uplink) - Number(a.uplink) ||
      Number(b.monitored) - Number(a.monitored) ||
      a.if_index - b.if_index,
  )
}

/** Estado del puerto = icono + texto + color (nunca solo color). */
export function portStatusVisual(p: Pick<SwitchPort, 'oper_status' | 'admin_up' | 'flaps_1h'>) {
  if (p.admin_up === false)
    return { key: 'adminDown', icon: 'i-lucide-power-off', cls: 'text-muted' }
  if (p.oper_status === 'up' && p.flaps_1h >= FLAP_THRESHOLD)
    return { key: 'flapping', icon: 'i-lucide-activity', cls: 'text-warning' }
  if (p.oper_status === 'up')
    return { key: 'up', icon: 'i-lucide-circle-check', cls: 'text-success' }
  if (p.oper_status === null)
    return { key: 'unknown', icon: 'i-lucide-circle-help', cls: 'text-muted' }
  return { key: 'down', icon: 'i-lucide-circle-x', cls: 'text-error' }
}

/** Aplica la elección local (monitorear/subida) y valida el máximo. */
export function selectionBody(choice: Record<string, { monitored: boolean; uplink: boolean }>): {
  ports: { name: string; uplink: boolean }[]
  tooMany: boolean
} {
  const ports = Object.entries(choice)
    .filter(([, c]) => c.monitored)
    .map(([name, c]) => ({ name, uplink: c.uplink }))
  return { ports, tooMany: ports.length > MAX_SWITCH_PORTS }
}

/** Camino de un switch vía router del sitio (ADR-0032), de la respuesta del alta o de la ficha. */
export interface ViaPath {
  router_id: string
  router_name: string
  transport: string
  protection_level: ProtectionLevel
  horus_address: string | null
  block?: string | null
}

/** Camino de la ficha de un switch (`via_router` + `via_address`); null en modo directo. */
export function viaPathOf(
  r: Pick<Router, 'connection_mode' | 'via_router' | 'via_router_id' | 'via_address'>,
): ViaPath | null {
  // Sin el resumen del router (no se pudo leer) no se inventa transporte ni protección.
  if (r.connection_mode !== 'via_router' || !r.via_router) return null
  return {
    router_id: r.via_router.id,
    router_name: r.via_router.name,
    transport: r.via_router.connection_mode,
    protection_level: r.via_router.protection_level,
    horus_address: r.via_address,
    block: null,
  }
}
