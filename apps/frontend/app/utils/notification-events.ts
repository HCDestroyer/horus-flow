import type { NotificationCategory, NotificationEventType } from '~~/types/api'

/**
 * Categorías de avisos de los canales de notificación (alerts.yaml, NotificationCategory) y
 * los tipos de evento de cada una. Suscribirse a una categoría incluye también los tipos que
 * se añadan a ella más adelante; los tipos sueltos sirven para afinar.
 */
export const NOTIFICATION_CATEGORIES: {
  key: NotificationCategory
  icon: string
  events: NotificationEventType[]
}[] = [
  {
    key: 'security',
    icon: 'i-lucide-shield-alert',
    events: ['finding_opened', 'finding_reopened'],
  },
  {
    key: 'routers',
    icon: 'i-lucide-router',
    events: [
      'router_offline',
      'router_recovered',
      'router_cpu_high',
      'router_memory_high',
      'router_temperature_high',
      'router_hardware_failed',
      'router_interface_down',
      'router_interface_up',
      'router_rebooted',
    ],
  },
  {
    key: 'infrastructure',
    icon: 'i-lucide-server',
    events: ['service_down', 'service_degraded', 'service_recovered'],
  },
  {
    key: 'exporter_tunnel',
    icon: 'i-lucide-cable',
    events: ['exporter_silent', 'exporter_recovered', 'tunnel_down', 'tunnel_recovered'],
  },
  {
    // Resoluciones y estado de seguridad de clientes (pensada para webhooks, ADR-0030).
    key: 'security_updates',
    icon: 'i-lucide-shield-check',
    events: ['finding_resolved', 'customer_security_state_changed'],
  },
]

/** Categoría de un tipo de evento. */
export function eventCategory(e: NotificationEventType): NotificationCategory | undefined {
  return NOTIFICATION_CATEGORIES.find((c) => c.events.includes(e))?.key
}

/** Tipos de evento que recibe una suscripción (categorías completas + tipos sueltos). */
export function subscribedEvents(sub: {
  event_types: NotificationEventType[]
  categories?: NotificationCategory[]
}): NotificationEventType[] {
  const out = new Set(sub.event_types)
  for (const c of NOTIFICATION_CATEGORIES) {
    if (sub.categories?.includes(c.key)) c.events.forEach((e) => out.add(e))
  }
  return [...out]
}
