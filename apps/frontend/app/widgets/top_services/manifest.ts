import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'top_services',
  sizes: {
    normal: { default: { w: 4, h: 4 }, min: { w: 4, h: 4 } },
    wall: { default: { w: 4, h: 4 }, min: { w: 4, h: 4 } },
  },
  description: 'widgets.top_services.description',
  category: 'traffic',
  placeholder: 'bars',
  wall: { hide: ['up_bytes'], enlarge: ['label'] },
})
