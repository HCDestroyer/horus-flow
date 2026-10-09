import type { RealtimeSource } from '~/utils/realtime'
import { findingId, openFindings } from './base'
import { findTenant } from './data'
import { mockState } from './state'
import { trafficSummaryMessage } from './widget-data'

/**
 * Fuente de tiempo real simulada, como haría el gateway por WebSocket (C6):
 * - `traffic.summary` (mensaje `state`) cada `intervalMs`.
 * - `security` (mensaje `event`): un hallazgo nuevo cada `findingEveryMs`
 *   (`localStorage['horus.mock.findingEveryMs']`, por defecto 45 s; 0 = nunca). El hallazgo
 *   entra en la base común, así que la lista, los widgets y el resumen lo cuentan igual.
 */
function readFindingEvery() {
  try {
    const raw = window.localStorage.getItem('horus.mock.findingEveryMs')
    if (raw !== null) return Number(raw)
  } catch {
    // sin storage
  }
  return 45_000
}

export function createMockRealtime(intervalMs = 5000, now = () => new Date()): RealtimeSource {
  return {
    subscribe(tenantId, topic, handler) {
      const tenant = findTenant(tenantId)
      if (!tenant) return () => {}
      if (topic === 'traffic.summary') {
        const timer = setInterval(() => handler(trafficSummaryMessage(tenant, now())), intervalMs)
        return () => clearInterval(timer)
      }
      if (topic === 'security') {
        const every = readFindingEvery()
        if (!every) return () => {}
        const timer = setInterval(() => {
          const at = now().getTime()
          const list = mockState.liveFindings.get(tenantId) ?? []
          list.push(at)
          mockState.liveFindings.set(tenantId, list)
          const id = findingId(90 + list.length - 1)
          const f = openFindings(tenant, now()).find((x) => x.id === id)
          // Sin IP del cliente en el payload (events.md §5.7).
          handler({
            type: 'event',
            topic: 'security',
            event: {
              id: `evt-${id}`,
              type: 'horus.detection.finding.opened',
              time: new Date(at).toISOString(),
              data: { finding_id: id, severity: f?.severity, kind: f?.kind },
            },
          })
        }, every)
        return () => clearInterval(timer)
      }
      return () => {}
    },
  }
}
