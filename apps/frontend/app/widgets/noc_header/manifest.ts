import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'noc_header',
  sizes: {
    normal: { default: { w: 12, h: 1 }, min: { w: 12, h: 1 } },
    wall: { default: { w: 12, h: 1 }, min: { w: 12, h: 1 } },
  },
  description: 'widgets.noc_header.description',
  category: 'layout',
  placeholder: 'header',
  wall: { hide: [], enlarge: ['clock', 'freshness'] },
})
