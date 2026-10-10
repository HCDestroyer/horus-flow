// Base de datos SQLite (better-sqlite3) en DATA_DIR (por defecto .data/; en Docker, un volumen).
// API síncrona: cada operación es corta y SQLite en modo WAL aguanta de sobra el tráfico de una
// landing. Una sola conexión por proceso.
import { mkdirSync } from 'node:fs'
import { join, resolve } from 'node:path'
import Database from 'better-sqlite3'
import { migrations, type Migration } from './migrations'

export type DB = Database.Database

export function dataDir(env: Record<string, string | undefined> = process.env): string {
  return resolve(env.DATA_DIR?.trim() || '.data')
}

/** Abre (y crea si hace falta) la base de datos y aplica las migraciones pendientes. */
export function openDatabase(file: string, list: Migration[] = migrations): DB {
  if (file !== ':memory:') mkdirSync(resolve(file, '..'), { recursive: true })
  const db = new Database(file)
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
