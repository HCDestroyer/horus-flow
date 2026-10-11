/**
 * ISP de la URL (`/t/:slug/…`, frontend.md §3.1–§3.2, I0-15):
 * - Si el usuario no es miembro → "No encontrado" (nunca "sin permiso", no revela que existe).
 * - Al entrar en un ISP distinto del activo: se descartan datos y suscripciones del anterior
 *   y se pide el token de ESE ISP (`POST /auth/token`) antes de cargar ninguna página.
 * - En la consola de plataforma se usa un token de ámbito plataforma.
 * - Se recuerda como último ISP usado.
 * - Si la cuenta debe cambiar la contraseña o activar el TOTP antes de recibir un token (p. ej.
 *   el superadministrador recién instalado), se lleva a "Mi cuenta" con el motivo en lugar de
 *   seguir con un token que el gateway rechazaría (TOKEN_SCOPE_INVALID).
 */
const SETUP_CODES = {
  PASSWORD_CHANGE_REQUIRED: 'password',
  MFA_ENROLLMENT_REQUIRED: 'mfa',
  MFA_REQUIRED: 'mfa-login',
} as const

function setupRequired(error: unknown) {
  if (!(error instanceof ApiError)) return null
  const reason = SETUP_CODES[error.code as keyof typeof SETUP_CODES]
  return reason ? navigateTo({ path: '/account', query: { required: reason } }) : null
}

export default defineNuxtRouteMiddleware(async (to) => {
  const auth = useAuth()
  if (!auth.isAuthenticated.value) return

  const notFound = () =>
    abortNavigation(createError({ statusCode: 404, statusMessage: 'Not Found' }))

  if (auth.me.value?.must_change_password && to.path !== '/account') {
    return navigateTo({ path: '/account', query: { required: 'password' } })
  }

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
        const setup = setupRequired(error)
        if (setup) return setup
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
    if (!auth.me.value?.mfa_enabled) {
      return navigateTo({ path: '/account', query: { required: 'mfa' } })
    }
    if (auth.activeScope.value?.kind !== 'platform') {
      const current = auth.activeScope.value
      await resetTenantData(current?.kind === 'tenant' ? current.tenantId : undefined, undefined)
      try {
        await auth.activateScope({ kind: 'platform' })
      } catch (error) {
        const setup = setupRequired(error)
        if (setup) return setup
        // Red caída u otro fallo: la página mostrará sus errores en contexto.
      }
    }
  }
})
