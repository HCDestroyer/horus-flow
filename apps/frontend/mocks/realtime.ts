import type { RealtimeSource } from '~/utils/realtime'
import { findTenant } from './data'
import { trafficSummaryMessage } from './widget-data'

/**
 * Fuente de tiempo real simulada: emite `traffic.summary` (mensaje `state`, C6) cada
 * `intervalMs` para el ISP de la suscripción, como haría el gateway por WebSocket.
 */
export function createMockRealtime(intervalMs = 5000, now = () => new Date()): RealtimeSource {
  return {
    subscribe(tenantId, topic, handler) {
      const tenant = findTenant(tenantId)
      if (!tenant || topic !== 'traffic.summary') return () => {}
      const emit = () => handler(trafficSummaryMessage(tenant, now()))
      const timer = setInterval(emit, intervalMs)
      return () => clearInterval(timer)
    },
  }
}
