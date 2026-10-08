import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'watched_ports',
  sizes: {
    normal: { default: { w: 4, h: 3 }, min: { w: 4, h: 3 } },
    wall: { default: { w: 4, h: 3 }, min: { w: 4, h: 3 } },
  },
  description: 'widgets.watched_ports.description',
  category: 'security',
  placeholder: 'table',
  wall: { hide: ['flows', 'protocol'], enlarge: ['customers'] },
})
