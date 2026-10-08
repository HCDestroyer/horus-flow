import type { AccessTokenResponse, LoginResponse, Me } from '~~/shared/api/types'

export type AuthStatus = 'unknown' | 'anonymous' | 'authenticated'

/** Access token en memoria de la pestaña (security.md §5.1). Nunca se persiste. */
export function useAccessToken() {
  return useState<string | null>('auth:token', () => null)
}

/**
 * Sesión del usuario (sin Pinia, conventions.md §3.4): estado sobre `useState`.
 *
 * Flujo (docs/api.md §0.2, §2.1): login → (2FA) → token de sesión → `GET /me`.
 * El token por ISP (`POST /auth/token`) llega con el selector de ISP en I0-15.
 */
export function useAuth() {
  const { $api } = useNuxtApp()
  const token = useAccessToken()
  const me = useState<Me | null>('auth:me', () => null)
  const status = useState<AuthStatus>('auth:status', () => 'unknown')

  async function establish(res: AccessTokenResponse) {
    token.value = res.access_token
    me.value = await $api.get<Me>('/me')
    status.value = 'authenticated'
  }

  /** Devuelve el `mfa_token` si la cuenta tiene 2FA; si no, deja la sesión iniciada. */
  async function login(email: string, password: string): Promise<{ mfaToken: string | null }> {
    const res = await $api.post<LoginResponse>('/auth/login', { email, password })
    if ('mfa_required' in res) return { mfaToken: res.mfa_token }
    await establish(res)
    return { mfaToken: null }
  }

  async function verifyMfa(mfaToken: string, code: string) {
    const res = await $api.post<AccessTokenResponse>('/auth/mfa/verify', {
      mfa_token: mfaToken,
      code,
    })
    await establish(res)
  }

  /** Al cargar la SPA: intenta recuperar la sesión con la cookie de refresh. */
  async function restore(): Promise<boolean> {
    try {
      const res = await $api.post<AccessTokenResponse>('/auth/refresh')
      await establish(res)
      return true
    } catch {
      clear()
      return false
    }
  }

  function clear() {
    token.value = null
    me.value = null
    status.value = 'anonymous'
  }

  async function logout() {
    try {
      await $api.post('/auth/logout')
    } catch {
      // La sesión local se cierra igualmente.
    }
    clear()
    await navigateTo('/login')
  }

  return {
    me: computed(() => me.value),
    status: computed(() => status.value),
    isAuthenticated: computed(() => status.value === 'authenticated'),
    login,
    verifyMfa,
    restore,
    logout,
    clear,
  }
}
