import type { AccessTokenResponse, Me } from '~~/types/api'

export type AuthStatus = 'unknown' | 'anonymous' | 'authenticated'

/** Ámbito del token de trabajo de la pestaña (api.md §0.2). */
export type TokenScope = { kind: 'tenant'; tenantId: string } | { kind: 'platform' }

interface ScopedToken {
  scope: TokenScope
  token: string
  expiresAt: string
}

/**
 * Tokens de la pestaña, solo en memoria (security.md §5.1; nunca storage):
 * - `session`: token sin tenant (`scope=session`), solo para `/me*`, `/auth/*` y pedir otros.
 * - `scoped`: token del ISP actual (`scope=tenant`, `tid`) o de plataforma.
 */
export function useAuthTokens() {
  const session = useState<string | null>('auth:session', () => null)
  const scoped = useState<ScopedToken | null>('auth:scoped', () => null)
  return { session, scoped }
}

/** Token que corresponde a una ruta de la API (sin prefijo `/api/v1`). */
export function tokenForPath(
  path: string,
  session: string | null,
  scoped: { token: string } | null,
): string | null {
  const sessionOnly = path.startsWith('/auth/') || path === '/me' || path.startsWith('/me/')
  if (sessionOnly) return session
  return scoped?.token ?? session
}

export function sameScope(a: TokenScope | undefined, b: TokenScope) {
  if (!a || a.kind !== b.kind) return false
  return a.kind === 'platform' || (b.kind === 'tenant' && a.tenantId === b.tenantId)
}

/**
 * Sesión del usuario (sin Pinia, conventions.md §3.4): estado sobre `useState`.
 *
 * Flujo (api.md §0.2, §2.1): login → (2FA) → token de sesión → `GET /me` → por cada ISP que
 * se abre, `POST /auth/token {tenant_id}` → token del ISP (10 min), que usan las rutas de
 * negocio. Al cambiar de ISP se pide otro y se descartan los datos del anterior.
 */
export function useAuth() {
  const { $api } = useNuxtApp()
  const { session, scoped } = useAuthTokens()
  const me = useState<Me | null>('auth:me', () => null)
  const status = useState<AuthStatus>('auth:status', () => 'unknown')

  async function establish(res: AccessTokenResponse) {
    session.value = res.access_token
    scoped.value = null
    me.value = await unwrap($api.GET('/me'))
    status.value = 'authenticated'
  }

  /** Devuelve el `mfa_token` si la cuenta tiene 2FA; si no, deja la sesión iniciada. */
  async function login(username: string, password: string): Promise<{ mfaToken: string | null }> {
    const res = await unwrap($api.POST('/auth/login', { body: { username, password } }))
    if ('mfa_required' in res) return { mfaToken: res.mfa_token }
    await establish(res)
    return { mfaToken: null }
  }

  async function verifyMfa(mfaToken: string, code: string) {
    const res = await unwrap($api.POST('/auth/mfa/verify', { body: { mfa_token: mfaToken, code } }))
    await establish(res)
  }

  /** Al cargar la SPA: intenta recuperar la sesión con la cookie de refresh. */
  async function restore(): Promise<boolean> {
    try {
      const res = await unwrap($api.POST('/auth/refresh', WITH_CSRF))
      await establish(res)
      return true
    } catch {
      clear()
      return false
    }
  }

  /**
   * Activa el token del ISP o de plataforma para esta pestaña (`POST /auth/token`).
   * Lanza `ApiError` 404 `TENANT_NOT_FOUND` si el usuario no es miembro.
   */
  async function activateScope(scope: TokenScope) {
    if (scoped.value && sameScope(scoped.value.scope, scope)) return
    const body =
      scope.kind === 'tenant' ? { tenant_id: scope.tenantId } : { scope: 'platform' as const }
    // El token anterior deja de usarse antes de pedir el nuevo: ninguna petición del ISP
    // nuevo sale con el token del anterior.
    scoped.value = null
    const res = await unwrap($api.POST('/auth/token', { body }))
    scoped.value = { scope, token: res.access_token, expiresAt: res.expires_at }
  }

  function clear() {
    session.value = null
    scoped.value = null
    me.value = null
    status.value = 'anonymous'
  }

  async function logout() {
    try {
      await $api.POST('/auth/logout', WITH_CSRF)
    } catch {
      // La sesión local se cierra igualmente.
    }
    clear()
    useNuxtApp().$authChannel.post({ type: 'logout' })
    await navigateTo('/login')
  }

  return {
    me: computed(() => me.value),
    status: computed(() => status.value),
    isAuthenticated: computed(() => status.value === 'authenticated'),
    activeScope: computed(() => scoped.value?.scope),
    login,
    verifyMfa,
    restore,
    activateScope,
    logout,
    clear,
  }
}
