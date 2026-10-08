import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'findings_summary',
  sizes: {
    normal: { default: { w: 4, h: 2 }, min: { w: 3, h: 2 } },
    wall: { default: { w: 4, h: 2 }, min: { w: 4, h: 2 } },
  },
  description: 'widgets.findings_summary.description',
  category: 'security',
  placeholder: 'kpi',
  wall: { hide: ['new_last_24h'], enlarge: ['open_total'] },
})
