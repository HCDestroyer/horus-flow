import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'traffic_timeseries',
  sizes: {
    normal: { default: { w: 8, h: 3 }, min: { w: 6, h: 3 } },
    wall: { default: { w: 8, h: 3 }, min: { w: 6, h: 3 } },
  },
  description: 'widgets.traffic_timeseries.description',
  category: 'traffic',
  placeholder: 'timeseries',
  wall: { hide: ['legend', 'tooltip'], enlarge: ['line_width', 'axis_labels'] },
})
