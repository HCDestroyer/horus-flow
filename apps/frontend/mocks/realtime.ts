import type { RealtimeSource } from '~/utils/realtime'
import { findingId, openFindings } from './base'
import { findTenant } from './data'
import {
  findSnmpRouter,
  onSnmpChange,
  routerLiveMetrics,
  routerMetricsMessage,
  routerStatusEvent,
  snmpRouters,
  type SnmpOptions,
} from './snmp'
import { mockState } from './state'
import { trafficSummaryMessage } from './widget-data'

/**
 * Fuente de tiempo real simulada, como haría el gateway por WebSocket (C6):
 * - `traffic.summary` (mensaje `state`) cada `intervalMs`.
 * - `security` (mensaje `event`): un hallazgo nuevo cada `findingEveryMs`
 *   (`localStorage['horus.mock.findingEveryMs']`, por defecto 45 s; 0 = nunca). El hallazgo
 *   entra en la base común, así que la lista, los widgets y el resumen lo cuentan igual.
 * - `routers.status` (mensaje `event`): cambio de observación de un router (p. ej. al concluir
 *   un sondeo pedido con `POST /routers/{id}/poll`).
 * - `router.{id}.metrics` (mensaje `state`): snapshot al suscribirse y cada `metricsEveryMs`
 *   (10 s) la última muestra con la forma de `GET /routers/{id}/metrics/live`.
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

/** Opciones del monitoreo simulado según el escenario (`horus.mock.scenario`). */
function snmpOptions(): SnmpOptions {
  let scenario = 'normal'
  let stepMs: number | undefined
  try {
    scenario = window.localStorage.getItem('horus.mock.scenario') ?? 'normal'
    const raw = Number(window.localStorage.getItem('horus.mock.onboardingStepMs'))
    stepMs = Number.isFinite(raw) && raw > 0 ? raw : undefined
  } catch {
    // sin storage
  }
  return { scenario, degraded: scenario === 'degraded', stepMs }
}

export function createMockRealtime(
  intervalMs = 5000,
  now = () => new Date(),
  metricsEveryMs = 10_000,
): RealtimeSource {
  return {
    subscribe(tenantId, topic, handler) {
      const tenant = findTenant(tenantId)
      if (!tenant) return () => {}
      if (topic === 'routers.status') {
        return onSnmpChange((tid, routerId) => {
          if (tid !== tenantId) return
          const at = now()
          const sr = findSnmpRouter(tenant, routerId, at, snmpOptions())
          if (sr) handler(routerStatusEvent(sr.status, at))
        })
      }
      const metrics = topic.match(/^router\.([^.]+)\.metrics$/)
      if (metrics) {
        const routerId = metrics[1]!
        const send = () => {
          const at = now()
          const sr = snmpRouters(tenant, at, snmpOptions()).find(
            (r) => r.item.router.id === routerId,
          )
          if (sr) handler(routerMetricsMessage(routerLiveMetrics(sr, at), at))
        }
        // Snapshot al suscribirse (clase `state`, C6), después cada sondeo simulado.
        const first = setTimeout(send, 0)
        const timer = setInterval(send, metricsEveryMs)
        const off = onSnmpChange((tid, rid) => {
          if (tid === tenantId && rid === routerId) send()
        })
        return () => {
          clearTimeout(first)
          clearInterval(timer)
          off()
        }
      }
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
