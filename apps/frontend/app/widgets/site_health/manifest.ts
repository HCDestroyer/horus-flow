import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'site_health',
  sizes: {
    normal: { default: { w: 6, h: 3 }, min: { w: 4, h: 3 } },
    wall: { default: { w: 12, h: 2 }, min: { w: 6, h: 2 } },
  },
  description: 'widgets.site_health.description',
  category: 'infrastructure',
  configForm: null,
  placeholder: 'list',
  wall: { hide: ['forecast'], enlarge: ['status'] },
})
