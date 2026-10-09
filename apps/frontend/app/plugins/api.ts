/**
 * Plugin `api` (conventions.md §3.5, I0-15): crea el cliente único de la pestaña.
 * - `openapi-fetch` con los tipos generados del contrato (`$api.GET('/me')`).
 * - Tokens solo en memoria (`useAuthTokens`), elegidos por ruta: sesión o ISP actual.
 * - Ante 401, un único refresh por pestaña, serializado entre pestañas con Web Locks
 *   (la cookie de refresh rota y su reutilización revoca la sesión), y reintento único.
 * - Con `public.apiMock` usa la API simulada (chunk aparte; no entra si se desactiva).
 */
export default defineNuxtPlugin({
  name: 'api',
  async setup() {
    const config = useRuntimeConfig()
    const { session, scoped } = useAuthTokens()

    const baseUrl = absoluteBase(config.public.apiBase)
    const basePath = new URL(baseUrl).pathname
    const transport: ApiFetch = config.public.apiMock
      ? (await import('~~/mocks/server')).createMockFetch()
      : (request) => globalThis.fetch(request, { credentials: 'same-origin' })

    const getAccessToken = (path: string) => tokenForPath(path, session.value, scoped.value)

    // Cliente sin refresh automático para las llamadas que hace el propio refresh.
    const raw = createHorusClient(
      baseUrl,
      createAuthFetch({ fetch: transport, basePath, getAccessToken }),
    )

    async function renew(): Promise<boolean> {
      try {
        const res = await unwrap(raw.POST('/auth/refresh', WITH_CSRF))
        session.value = res.access_token
        const current = scoped.value
        if (current) {
          const body =
            current.scope.kind === 'tenant'
              ? { tenant_id: current.scope.tenantId }
              : { scope: 'platform' as const }
          const next = await unwrap(raw.POST('/auth/token', { body }))
          scoped.value = {
            scope: current.scope,
            token: next.access_token,
            expiresAt: next.expires_at,
          }
        }
        authChannel.post({ type: 'refreshed', at: Date.now() })
        return true
      } catch {
        session.value = null
        scoped.value = null
        return false
      }
    }

    function goToLogin(reason?: 'expired') {
      useAuth().clear()
      const route = useRoute()
      // El kiosco no tiene sesión de usuario: renueva su propio token (useKiosk).
      if (!route.meta.public && !window.location.pathname.startsWith('/kiosk')) {
        navigateTo({
          path: '/login',
          query: { redirect: route.fullPath, ...(reason ? { reason } : {}) },
        })
      }
    }

    const authChannel = openAuthChannel((message) => {
      // Otra pestaña cerró la sesión: la cookie ya no vale, esta pestaña también sale.
      if (message.type === 'logout') goToLogin()
    })

    const api = createHorusClient(
      baseUrl,
      createAuthFetch({
        fetch: transport,
        basePath,
        getAccessToken,
        refresh: () => withCrossTabLock(REFRESH_LOCK, renew),
        onSessionExpired: () => goToLogin('expired'),
      }),
    )

    return { provide: { api, authChannel } }
  },
})
