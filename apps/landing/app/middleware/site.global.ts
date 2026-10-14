// Carga los datos del sitio (catálogo, ajustes, pagos; GET /api/site) antes de mostrar una
// página pública: en el servidor en cada render (con caché corta en el servidor, invalidada al
// guardar en el panel) y en el cliente solo si aún no están. El panel (/admin) no los usa.
import type { PublicSite } from '#shared/catalog'

export default defineNuxtRouteMiddleware(async (to) => {
  if (to.path === '/admin' || to.path.startsWith('/admin/')) return
  const state = useSiteState()
  if (import.meta.server || !state.value) {
    state.value = await $fetch<PublicSite>('/api/site')
  }
})
