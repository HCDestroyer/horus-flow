/**
 * ISP de la URL (`/t/:slug/…`, frontend.md §3.1): si el usuario no es miembro se muestra
 * "No encontrado" (nunca "sin permiso", para no revelar que el ISP existe).
 */
export default defineNuxtRouteMiddleware((to) => {
  const slug = typeof to.params.slug === 'string' ? to.params.slug : undefined
  if (!slug || !to.path.startsWith('/t/')) return

  const { me } = useAuth()
  if (!findMembership(me.value, slug)) {
    return abortNavigation(createError({ statusCode: 404, statusMessage: 'Not Found' }))
  }
  rememberTenant(slug)
})
