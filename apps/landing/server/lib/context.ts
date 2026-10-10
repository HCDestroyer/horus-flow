// Estado del proceso del servidor: base de datos, clave de datos y caché pública. Se crea en el
// arranque (server/plugins/database.ts) y lo comparten las rutas.
import { readFileSync } from 'node:fs'
import type { AuthDeps } from './auth/service'
import { countAdmins, createAdminSync } from './auth/service'
import { seedIfEmpty } from './catalog'
import { dataDir, useDb, type DB } from './db'
import { loadKey, type KeySource } from './secrets'
import { SiteCache } from './site-cache'

export interface AppContext {
  db: DB
  key: KeySource
  cache: SiteCache
  auth: () => AuthDeps
}

let ctx: AppContext | null = null

export function initApp(
  env: Record<string, string | undefined> = process.env,
  db: DB = useDb(),
): AppContext {
  const key = loadKey(env, dataDir(env))
  const cache = new SiteCache(
    () => db,
    () => key.box,
  )
  ctx = { db, key, cache, auth: () => ({ db, box: key.box, now: Date.now }) }
  seedIfEmpty(db)
  return ctx
}

export function useApp(): AppContext {
  return ctx ?? initApp()
}

/** Solo tests. */
export function setApp(next: AppContext | null): void {
  ctx = next
}

/**
 * Primer administrador desde ADMIN_EMAIL + ADMIN_PASSWORD_FILE, solo si aún no hay ninguno.
 * Devuelve lo que ha pasado para el registro (sin la contraseña).
 */
export function bootstrapAdmin(
  deps: AuthDeps,
  env: Record<string, string | undefined>,
): 'created' | 'exists' | 'skipped' | string {
  const email = env.ADMIN_EMAIL?.trim()
  const file = env.ADMIN_PASSWORD_FILE?.trim()
  if (!email || !file) return 'skipped'
  if (countAdmins(deps.db) > 0) return 'exists'
  try {
    const password = readFileSync(file, 'utf8').replace(/\r?\n$/, '')
    createAdminSync(
      deps,
      { email, password, name: env.ADMIN_NAME ?? '' },
      { id: null, email: 'sistema (ADMIN_EMAIL)' },
    )
    return 'created'
  } catch (err) {
    return `error: ${String((err as Error).message ?? err)}`
  }
}
