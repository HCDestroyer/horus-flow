// GET /api/site — lo que la landing necesita de la base de datos: catálogo (solo planes
// visibles), ajustes públicos y métodos de pago activos (sin secretos). Con caché corta.
import type { PublicSite } from '../../shared/catalog'
import { useApp } from '../lib/context'

export default defineEventHandler((event): PublicSite => {
  const site = useApp().cache.get()
  setResponseHeader(event, 'Cache-Control', 'no-store')
  return {
    ...site,
    catalog: { ...site.catalog, plans: site.catalog.plans.filter((p) => p.visible) },
  }
})
