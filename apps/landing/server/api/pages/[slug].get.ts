// GET /api/pages/:slug?locale=es|en — página legal o informativa publicada, con el Markdown ya
// convertido a HTML seguro (server/lib/markdown.ts). 404 si no existe o no está publicada. Si el
// slug es el del otro idioma, devuelve la página igualmente: la web redirige a su URL canónica.
import type { PublicPage } from '../../../shared/pages'
import { useApp } from '../../lib/context'

export default defineEventHandler((event): PublicPage => {
  const slug = String(getRouterParam(event, 'slug') ?? '')
  const locale = getQuery(event).locale === 'en' ? 'en' : 'es'
  const page = useApp().cache.page(slug, locale)
  setResponseHeader(event, 'Cache-Control', 'no-store')
  if (!page) throw createError({ statusCode: 404, statusMessage: 'Not Found' })
  return page
})
