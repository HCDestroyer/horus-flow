import { defineWidgetManifest } from '../define'

export default defineWidgetManifest({
  type: 'routers_status_grid',
  sizes: {
    normal: { default: { w: 6, h: 3 }, min: { w: 4, h: 3 } },
    wall: { default: { w: 12, h: 2 }, min: { w: 6, h: 2 } },
  },
  description: 'widgets.routers_status_grid.description',
  category: 'infrastructure',
  // No hay formulario de configuración todavía (el editor de dashboards es de I2).
  configForm: null,
  placeholder: 'grid',
  wall: { hide: ['site', 'memory_percent', 'uptime_seconds', 'reason'], enlarge: ['status'] },
})
