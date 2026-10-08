import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'top_customers',
  sizes: {
    normal: { default: { w: 6, h: 4 }, min: { w: 4, h: 4 } },
    wall: { default: { w: 6, h: 4 }, min: { w: 6, h: 4 } },
  },
  description: 'widgets.top_customers.description',
  category: 'customers',
  placeholder: 'table',
  wall: { hide: ['site', 'up_bytes'], enlarge: ['down_bytes'] },
})
