import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'botnet_signals',
  sizes: {
    normal: { default: { w: 4, h: 3 }, min: { w: 4, h: 3 } },
    wall: { default: { w: 4, h: 3 }, min: { w: 4, h: 3 } },
  },
  description: 'widgets.botnet_signals.description',
  category: 'security',
  placeholder: 'bars',
  wall: { hide: ['zero_signals'], enlarge: [] },
})
