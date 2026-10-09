import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'security_by_node',
  sizes: {
    normal: { default: { w: 4, h: 4 }, min: { w: 4, h: 4 } },
    wall: { default: { w: 4, h: 4 }, min: { w: 4, h: 4 } },
  },
  description: 'widgets.security_by_node.description',
  category: 'security',
  placeholder: 'bars',
  wall: { hide: ['open_findings'], enlarge: ['customers_with_signals'] },
})
