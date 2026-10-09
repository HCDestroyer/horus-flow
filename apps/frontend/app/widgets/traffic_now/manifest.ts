import type { WidgetData } from '~~/types/api'
import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'traffic_now',
  sizes: {
    normal: { default: { w: 4, h: 2 }, min: { w: 3, h: 2 } },
    wall: { default: { w: 4, h: 2 }, min: { w: 4, h: 2 } },
  },
  description: 'widgets.traffic_now.description',
  category: 'traffic',
  placeholder: 'sparkline',
  wall: { hide: ['sparkline'], enlarge: ['down_bps'] },
})

/** Vacío solo si no hay ninguna tasa (0 bit/s es un dato; null es "sin dato"). */
export function isEmpty(data: WidgetData['data']) {
  return data.kind !== 'state' || (data.values.down_bps == null && data.values.up_bps == null)
}
