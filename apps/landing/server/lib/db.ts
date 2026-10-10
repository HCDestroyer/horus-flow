// Base de datos SQLite en DATA_DIR (por defecto .data/; en Docker, un volumen), con el módulo
// integrado de Node (`node:sqlite`, Node ≥ 22.13): sin dependencias nativas que compilar, así que
// `pnpm install` funciona igual en Windows, macOS, Linux y en la imagen distroless.
// API síncrona: cada operación es corta y SQLite en modo WAL aguanta de sobra el tráfico de una
// landing. Una sola conexión por proceso.
import { mkdirSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { migrations, type Migration } from './migrations'

/** Sentencia preparada: parámetros y filas sin tipar, como el resto del código espera. */
export interface Statement {
  run(...params: unknown[]): { changes: number | bigint; lastInsertRowid: number | bigint }
  get(...params: unknown[]): unknown
  all(...params: unknown[]): unknown[]
}

/**
 * Conexión SQLite con las utilidades que usa el código: `prepare`, `exec`, `close`, `pragma()` y
 * `transaction(fn)`, que devuelve una función que ejecuta `fn` dentro de una transacción
 * (BEGIN IMMEDIATE … COMMIT, o ROLLBACK si lanza). Las transacciones anidadas usan SAVEPOINT.
 */
export class DB {
  readonly #db: DatabaseSync
  #depth = 0

  constructor(file: string) {
    this.#db = new DatabaseSync(file)
  }

  prepare(sql: string): Statement {
    return this.#db.prepare(sql) as unknown as Statement
  }

  exec(sql: string): void {
    this.#db.exec(sql)
  }

  close(): void {
    this.#db.close()
  }

  pragma(statement: string): unknown[] {
    return this.prepare(`PRAGMA ${statement}`).all()
  }

  transaction<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => R {
    return (...args: A): R => {
      const sp = `sp_${this.#depth}`
      this.exec(this.#depth === 0 ? 'BEGIN IMMEDIATE' : `SAVEPOINT ${sp}`)
      this.#depth++
      try {
        const out = fn(...args)
        this.#depth--
        this.exec(this.#depth === 0 ? 'COMMIT' : `RELEASE ${sp}`)
        return out
      } catch (err) {
        this.#depth--
        this.exec(this.#depth === 0 ? 'ROLLBACK' : `ROLLBACK TO ${sp}; RELEASE ${sp}`)
        throw err
      }
    }
  }
}

export function dataDir(env: Record<string, string | undefined> = process.env): string {
  return resolve(env.DATA_DIR?.trim() || '.data')
}

/** Abre (y crea si hace falta) la base de datos y aplica las migraciones pendientes. */
export function openDatabase(file: string, list: Migration[] = migrations): DB {
  if (file !== ':memory:') mkdirSync(resolve(file, '..'), { recursive: true })
  const db = new DB(file)
  db.pragma('journal_mode = WAL')
  db.pragma('foreign_keys = ON')
  db.pragma('busy_timeout = 5000')
  db.pragma('synchronous = NORMAL')
  migrate(db, list)
  return db
}

export function migrate(db: DB, list: Migration[] = migrations): number[] {
  db.exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TEXT NOT NULL
  )`)
  const done = new Set(
    (db.prepare('SELECT version FROM schema_migrations').all() as { version: number }[]).map(
      (r) => r.version,
    ),
  )
  const applied: number[] = []
  for (const m of [...list].sort((a, b) => a.version - b.version)) {
    if (done.has(m.version)) continue
    db.transaction(() => {
      db.exec(m.sql)
      db.prepare('INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)').run(
        m.version,
        m.name,
        new Date().toISOString(),
      )
    })()
    applied.push(m.version)
  }
  return applied
}

let shared: DB | null = null

/** Conexión del proceso del servidor: DATA_DIR/horus-landing.sqlite. */
export function useDb(): DB {
  if (!shared) shared = openDatabase(join(dataDir(), 'horus-landing.sqlite'))
  return shared
}

/** Solo para tests: sustituye la conexión del proceso. */
export function setDb(db: DB | null): void {
  shared = db
}

export const nowIso = (ms = Date.now()) => new Date(ms).toISOString()
