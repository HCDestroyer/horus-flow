import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'findings_feed',
  sizes: {
    normal: { default: { w: 6, h: 4 }, min: { w: 6, h: 4 } },
    wall: { default: { w: 6, h: 4 }, min: { w: 6, h: 4 } },
  },
  description: 'widgets.findings_feed.description',
  category: 'security',
  placeholder: 'list',
  wall: { hide: ['kind', 'site', 'confidence'], enlarge: ['severity', 'summary'] },
})
