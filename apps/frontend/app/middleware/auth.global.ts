/**
 * Autenticación (frontend.md §16, conventions.md §3.2).
 * Al primer acceso intenta recuperar la sesión con la cookie de refresh; las rutas no públicas
 * exigen sesión y las de login redirigen al inicio si ya la hay.
 */
export default defineNuxtRouteMiddleware(async (to) => {
  const auth = useAuth()

  if (auth.status.value === 'unknown') {
    await auth.restore()
  }

  if (to.meta.public) {
    if (auth.isAuthenticated.value && to.path === '/login') {
      const redirect = typeof to.query.redirect === 'string' ? to.query.redirect : '/'
      return navigateTo(safeRedirect(redirect))
    }
    return
  }

  if (!auth.isAuthenticated.value) {
    return navigateTo({
      path: '/login',
      query: to.fullPath === '/' ? {} : { redirect: to.fullPath },
    })
  }
})
