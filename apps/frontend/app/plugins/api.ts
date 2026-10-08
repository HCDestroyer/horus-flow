import type { AccessTokenResponse } from '~~/shared/api/types'

/**
 * Plugin `api` (conventions.md §3.5): crea el cliente único de la pestaña.
 * - Access token solo en memoria (`useState`, nunca storage).
 * - Ante 401, una sola llamada a `POST /auth/refresh` (con `X-Requested-With: horus`).
 * - Si `public.apiMock`, usa la API simulada (chunk aparte, no entra si se desactiva).
 */
export default defineNuxtPlugin({
  name: 'api',
  async setup() {
    const config = useRuntimeConfig()
    const token = useAccessToken()

    const transport = config.public.apiMock
      ? (await import('~~/mocks/transport')).createMockTransport()
      : createFetchTransport(config.public.apiBase)

    const api: ApiClient = createApiClient({
      transport,
      getAccessToken: () => token.value,
      refresh: async () => {
        try {
          const res = await api.post<AccessTokenResponse>('/auth/refresh')
          token.value = res.access_token
          return true
        } catch {
          token.value = null
          return false
        }
      },
      onSessionExpired: () => {
        useAuth().clear()
        const route = useRoute()
        if (!route.meta.public) {
          navigateTo({ path: '/login', query: { redirect: route.fullPath, reason: 'expired' } })
        }
      },
    })

    return { provide: { api } }
  },
})
