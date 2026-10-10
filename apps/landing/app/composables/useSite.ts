// Datos de la landing que vienen de la base de datos (GET /api/site): catálogo de planes y
// precios, ajustes (contacto, soporte, banner) y métodos de pago activos. Los carga el
// middleware global (middleware/site.global.ts) y viajan al navegador en el payload: un precio
// cambiado en el panel se ve en la siguiente carga de la página.
import type { PublicSite } from '#shared/catalog'

export function useSiteState() {
  return useState<PublicSite | null>('site', () => null)
}

/** Datos del sitio en una página pública (siempre cargados por el middleware). */
export function useSite(): ComputedRef<PublicSite> {
  const state = useSiteState()
  return computed(() => state.value!)
}
