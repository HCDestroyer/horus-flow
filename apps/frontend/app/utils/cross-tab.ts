/**
 * Coordinación entre pestañas del mismo navegador (I0-15).
 *
 * El refresh es rotativo con detección de reutilización (security.md §5.1): si dos pestañas
 * presentan a la vez la misma cookie, la segunda reutiliza un refresh ya rotado. Con la
 * Web Locks API las pestañas renuevan **de una en una**: la segunda espera y presenta la
 * cookie ya rotada por la primera. No se comparte ningún token entre pestañas (cada una
 * guarda el suyo en memoria, api.md §0.2); solo se avisa de eventos de sesión.
 */

interface LockManagerLike {
  request<T>(name: string, callback: () => Promise<T>): Promise<T>
}

export const REFRESH_LOCK = 'horus.auth.refresh'
export const AUTH_CHANNEL = 'horus.auth'

export type AuthBroadcast = { type: 'logout' } | { type: 'refreshed'; at: number }

/** Ejecuta `fn` con un cerrojo exclusivo compartido por todas las pestañas del origen. */
export function withCrossTabLock<T>(
  name: string,
  fn: () => Promise<T>,
  locks: LockManagerLike | undefined = globalThis.navigator?.locks as LockManagerLike | undefined,
): Promise<T> {
  if (!locks?.request) return fn()
  return locks.request(name, fn)
}

/** Canal de eventos de sesión entre pestañas (cierre de sesión, refresh hecho). */
export function openAuthChannel(onMessage: (message: AuthBroadcast) => void) {
  if (typeof BroadcastChannel === 'undefined') {
    return { post: (_: AuthBroadcast) => {}, close: () => {} }
  }
  const channel = new BroadcastChannel(AUTH_CHANNEL)
  channel.onmessage = (event: MessageEvent<AuthBroadcast>) => onMessage(event.data)
  return {
    post: (message: AuthBroadcast) => channel.postMessage(message),
    close: () => channel.close(),
  }
}
