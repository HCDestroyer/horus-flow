import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'exporters_status',
  sizes: {
    normal: { default: { w: 12, h: 2 }, min: { w: 4, h: 2 } },
    wall: { default: { w: 12, h: 2 }, min: { w: 12, h: 2 } },
  },
  description: 'widgets.exporters_status.description',
  category: 'infrastructure',
  placeholder: 'grid',
  wall: { hide: ['site', 'loss_ratio'], enlarge: ['state'] },
})
