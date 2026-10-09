/**
 * Tiempo real (frontend.md §10, contrato C6 `packages/schemas/websocket/v0`).
 *
 * En I0 no hay gateway WebSocket: la fuente es la API simulada (`mocks/realtime.ts`) o una
 * fuente vacía. El cliente WS real (ticket de un uso, reconexión, re-suscripción) entra con
 * la historia de tiempo real del I1; esta interfaz es la que usarán los widgets.
 */

/** Mensaje `state` del protocolo `horus.ws.v1` (messages.schema.json). */
export interface RealtimeStateMessage {
  type: 'state'
  topic: string
  key: string
  time: string
  data: Record<string, unknown>
}

/** Mensaje `event` (clase event de C6, p. ej. tema `security`): proyección del evento. */
export interface RealtimeEventMessage {
  type: 'event'
  topic: string
  event: {
    id: string
    /** Tipo del evento (`horus.detection.finding.opened`…). */
    type: string
    time: string
    data: Record<string, unknown>
  }
}

export type RealtimeMessage = RealtimeStateMessage | RealtimeEventMessage

export type RealtimeHandler = (message: RealtimeMessage) => void

export interface RealtimeSource {
  /** Suscribe a un tema con ámbito de ISP; devuelve la función para cancelar. */
  subscribe(tenantId: string, topic: string, handler: RealtimeHandler): () => void
}

export const noopRealtime: RealtimeSource = { subscribe: () => () => {} }

/**
 * Registro de suscripciones de la pestaña, indexadas por ISP: al cambiar de ISP se cierran
 * todas (aislamiento en el cliente, frontend.md §3.2).
 */
export function createRealtimeRegistry(source: RealtimeSource) {
  const active = new Map<number, { tenantId: string; close: () => void }>()
  let seq = 0

  return {
    subscribe(tenantId: string, topic: string, handler: RealtimeHandler) {
      const id = ++seq
      const close = source.subscribe(tenantId, topic, (message) => {
        // Nunca se entrega un mensaje de un ISP distinto del de la suscripción.
        if (active.has(id)) handler(message)
      })
      active.set(id, { tenantId, close })
      return () => {
        active.get(id)?.close()
        active.delete(id)
      }
    },
    /** Cierra las suscripciones de un ISP (o todas). */
    closeTenant(tenantId?: string) {
      for (const [id, sub] of active) {
        if (!tenantId || sub.tenantId === tenantId) {
          sub.close()
          active.delete(id)
        }
      }
    },
    get size() {
      return active.size
    },
  }
}

export type RealtimeRegistry = ReturnType<typeof createRealtimeRegistry>
