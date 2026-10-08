/**
 * Preferencias en `localStorage` (frontend.md §16: solo preferencias, nada sensible).
 * El tema lo guarda @nuxtjs/color-mode con la clave `horus-color-mode`.
 */
const LAST_TENANT_KEY = 'horus.lastTenant'

export function rememberTenant(slug: string) {
  try {
    localStorage.setItem(LAST_TENANT_KEY, slug)
  } catch {
    // navegación privada o storage bloqueado: no es crítico
  }
}

export function lastTenant(): string | null {
  try {
    return localStorage.getItem(LAST_TENANT_KEY)
  } catch {
    return null
  }
}
