import { describe, expect, it } from 'vitest'
import { openDatabase, type DB } from '../../server/lib/db'

function fresh(): DB {
  const db = openDatabase(':memory:', [])
  db.exec('CREATE TABLE t (v INTEGER NOT NULL)')
  return db
}
const count = (db: DB) => (db.prepare('SELECT COUNT(*) AS n FROM t').get() as { n: number }).n

describe('DB (node:sqlite)', () => {
  it('confirma la transacción y devuelve el resultado', () => {
    const db = fresh()
    const id = db.transaction(() =>
      Number(db.prepare('INSERT INTO t (v) VALUES (?)').run(1).lastInsertRowid),
    )()
    expect(id).toBe(1)
    expect(count(db)).toBe(1)
  })

  it('deshace todo si la función lanza', () => {
    const db = fresh()
    expect(() =>
      db.transaction(() => {
        db.prepare('INSERT INTO t (v) VALUES (?)').run(1)
        throw new Error('boom')
      })(),
    ).toThrow('boom')
    expect(count(db)).toBe(0)
  })

  it('anida con SAVEPOINT: el fallo interior solo deshace lo interior', () => {
    const db = fresh()
    db.transaction(() => {
      db.prepare('INSERT INTO t (v) VALUES (?)').run(1)
      try {
        db.transaction(() => {
          db.prepare('INSERT INTO t (v) VALUES (?)').run(2)
          throw new Error('interior')
        })()
      } catch {
        // se ignora: la exterior sigue
      }
      db.prepare('INSERT INTO t (v) VALUES (?)').run(3)
    })()
    const vals = (db.prepare('SELECT v FROM t ORDER BY v').all() as { v: number }[]).map((r) => r.v)
    expect(vals).toEqual([1, 3])
  })

  it('acepta parámetros con nombre sin prefijo y aplica los pragmas', () => {
    const db = fresh()
    db.prepare('INSERT INTO t (v) VALUES (@v)').run({ v: 7 })
    expect((db.prepare('SELECT v FROM t').get() as { v: number }).v).toBe(7)
    expect(db.pragma('foreign_keys')).toEqual([{ foreign_keys: 1 }])
  })
})
