import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'findings_trend',
  sizes: {
    normal: { default: { w: 6, h: 3 }, min: { w: 6, h: 3 } },
    wall: { default: { w: 6, h: 3 }, min: { w: 6, h: 3 } },
  },
  description: 'widgets.findings_trend.description',
  category: 'security',
  placeholder: 'timeseries',
  wall: { hide: ['tooltip'], enlarge: ['axis_labels'] },
})
