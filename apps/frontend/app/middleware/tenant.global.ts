/**
 * ISP de la URL (`/t/:slug/…`, frontend.md §3.1–§3.2, I0-15):
 * - Si el usuario no es miembro → "No encontrado" (nunca "sin permiso", no revela que existe).
 * - Al entrar en un ISP distinto del activo: se descartan datos y suscripciones del anterior
 *   y se pide el token de ESE ISP (`POST /auth/token`) antes de cargar ninguna página.
 * - En la consola de plataforma se usa un token de ámbito plataforma.
 * - Se recuerda como último ISP usado.
 */
export default defineNuxtRouteMiddleware(async (to) => {
  const auth = useAuth()
  if (!auth.isAuthenticated.value) return

  const notFound = () =>
    abortNavigation(createError({ statusCode: 404, statusMessage: 'Not Found' }))

  const slug = typeof to.params.slug === 'string' ? to.params.slug : undefined
  if (slug && to.path.startsWith('/t/')) {
    const membership = findMembership(auth.me.value, slug)
    if (!membership) return notFound()

    const current = auth.activeScope.value
    if (current?.kind !== 'tenant' || current.tenantId !== membership.tenant_id) {
      const from = current?.kind === 'tenant' ? current.tenantId : undefined
      await resetTenantData(from, membership.tenant_id)
      try {
        await auth.activateScope({ kind: 'tenant', tenantId: membership.tenant_id })
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) return notFound()
        if (error instanceof ApiError && error.status === 403) {
          return abortNavigation(createError({ statusCode: 403, statusMessage: error.code }))
        }
        // Red caída u otro fallo: la página mostrará sus errores en contexto.
      }
    }
    rememberTenant(slug)
    return
  }

  if (to.path.startsWith('/platform') && isPlatformUser(auth.me.value)) {
    if (auth.activeScope.value?.kind !== 'platform') {
      const current = auth.activeScope.value
      await resetTenantData(current?.kind === 'tenant' ? current.tenantId : undefined, undefined)
      await auth.activateScope({ kind: 'platform' }).catch(() => undefined)
    }
  }
})
