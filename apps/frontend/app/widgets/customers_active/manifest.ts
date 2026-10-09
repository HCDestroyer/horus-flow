import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'customers_active',
  sizes: {
    normal: { default: { w: 3, h: 2 }, min: { w: 3, h: 2 } },
    wall: { default: { w: 3, h: 2 }, min: { w: 3, h: 2 } },
  },
  description: 'widgets.customers_active.description',
  category: 'customers',
  placeholder: 'kpi',
  wall: { hide: ['total'], enlarge: ['active'] },
})
