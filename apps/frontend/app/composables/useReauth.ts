import ReauthModal from '~/components/common/ReauthModal.vue'

/**
 * Re-autenticación reciente para acciones `x-reauth` (cambiar roles, dar de alta un ISP…):
 * si la acción responde `403 REAUTH_REQUIRED`, se pide la contraseña (y el TOTP) en
 * `ReauthModal`, se guarda el token nuevo (`useAuth().reauthenticate`) y se reintenta la acción
 * sola, sin cerrar la sesión ni perder lo escrito.
 *
 * ```ts
 * const { withReauth } = useReauth()
 * await withReauth(() => unwrap($api.PUT(...)))
 * ```
 * Si el usuario cancela, se relanza el `REAUTH_REQUIRED` original (`isReauthRequired(e)`).
 */

export function isReauthRequired(error: unknown): error is ApiError {
  return error instanceof ApiError && error.code === 'REAUTH_REQUIRED'
}

/** Intentos máximos de la acción: el primero y un reintento por cada confirmación. */
const MAX_ATTEMPTS = 3

let modal: ReturnType<ReturnType<typeof useOverlay>['create']> | null = null

export function useReauth() {
  const overlay = useOverlay()
  modal ??= overlay.create(ReauthModal)
  const instance = modal

  /** Abre el diálogo; `true` si el usuario confirmó su identidad. */
  async function confirm(description?: string): Promise<boolean> {
    return (await instance.open(description ? { description } : {})) === true
  }

  async function withReauth<T>(action: () => Promise<T>, description?: string): Promise<T> {
    for (let attempt = 1; ; attempt++) {
      try {
        return await action()
      } catch (error) {
        if (!isReauthRequired(error) || attempt >= MAX_ATTEMPTS) throw error
        if (!(await confirm(description))) throw error
      }
    }
  }

  return { confirm, withReauth }
}
