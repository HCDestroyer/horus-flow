import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import {
  getPricingVersion,
  listPricingVersions,
  publishCatalog,
  readCatalog,
  readSiteSettings,
  seedCatalog,
  seedIfEmpty,
} from '../../server/lib/catalog'
import { migrate, openDatabase } from '../../server/lib/db'
import { migrations } from '../../server/lib/migrations'
import { SiteCache } from '../../server/lib/site-cache'

const actor = { id: 1, email: 'ana@kns.gt' }

function freshDb() {
  const db = openDatabase(':memory:')
  seedIfEmpty(db)
  return db
}

describe('migraciones', () => {
  const dirs: string[] = []
  afterEach(() => dirs.splice(0).forEach((d) => rmSync(d, { recursive: true, force: true })))

  it('se aplican una vez, en orden, y quedan registradas', () => {
    const dir = mkdtempSync(join(tmpdir(), 'hf-db-'))
    dirs.push(dir)
    const file = join(dir, 'test.sqlite')
    const db = openDatabase(file)
    const applied = db.prepare('SELECT version FROM schema_migrations').all()
    expect(applied).toEqual(migrations.map((m) => ({ version: m.version })))
    expect(migrate(db)).toEqual([])
    db.close()
    // Al reabrir no se vuelve a aplicar nada.
    const again = openDatabase(file)
    expect(migrate(again)).toEqual([])
    again.close()
  })

  it('una migración nueva se aplica sobre una base existente', () => {
    const db = openDatabase(':memory:')
    const extra = [
      ...migrations,
      { version: 99, name: 'prueba', sql: 'CREATE TABLE t99 (x INTEGER)' },
    ]
    expect(migrate(db, extra)).toEqual([99])
    expect(migrate(db, extra)).toEqual([])
  })
})

describe('semilla', () => {
  it('siembra desde app/config/pricing.ts solo la primera vez', () => {
    const db = openDatabase(':memory:')
    expect(seedIfEmpty(db)).toBe(true)
    expect(seedIfEmpty(db)).toBe(false)
    const c = readCatalog(db)
    expect(c).toEqual(seedCatalog())
    expect(c.confirmed).toBe(true)
    expect(c.plans.find((p) => p.id === 'small')!.prices!.USD).toEqual({
      monthly: 149,
      annual: 1490,
    })
    expect(c.plans.find((p) => p.id === 'enterprise')!.prices).toBeNull()
    expect(listPricingVersions(db)).toHaveLength(1)
    expect(readSiteSettings(db).support.text.es).toContain('24/7')
    expect(readSiteSettings(db).support.responseTime).toEqual({ es: '', en: '' })
  })
})

describe('edición de precios', () => {
  it('publica, guarda historial y restaura una versión anterior', () => {
    const db = freshDb()
    const c = readCatalog(db)
    const medium = c.plans.find((p) => p.id === 'medium')!
    medium.prices!.USD.monthly = 449.5
    medium.includes.push({ es: 'Nuevo', en: 'New' })
    const { version, before, after } = publishCatalog(db, c, actor, { note: 'subida' })
    expect(before!.plans.find((p) => p.id === 'medium')!.prices!.USD.monthly).toBe(399)
    expect(after.plans.find((p) => p.id === 'medium')!.prices!.USD.monthly).toBe(449.5)
    expect(
      readCatalog(db)
        .plans.find((p) => p.id === 'medium')!
        .includes.at(-1),
    ).toEqual({ es: 'Nuevo', en: 'New' })

    const versions = listPricingVersions(db)
    expect(versions[0]).toMatchObject({ id: version, adminEmail: 'ana@kns.gt', note: 'subida' })
    const first = versions.at(-1)!
    const restored = publishCatalog(db, getPricingVersion(db, first.id)!, actor, {
      restoredFrom: first.id,
    })
    expect(readCatalog(db).plans.find((p) => p.id === 'medium')!.prices!.USD.monthly).toBe(399)
    expect(listPricingVersions(db)[0]).toMatchObject({
      id: restored.version,
      restoredFrom: first.id,
    })
  })

  it('ordena, oculta y marca el destacado', () => {
    const db = freshDb()
    const c = readCatalog(db)
    c.plans.reverse()
    c.plans.forEach((p) => (p.highlighted = p.id === 'large'))
    c.plans.find((p) => p.id === 'small')!.visible = false
    publishCatalog(db, c, actor)
    const r = readCatalog(db)
    expect(r.plans.map((p) => p.id)).toEqual(['enterprise', 'large', 'medium', 'small'])
    expect(r.plans.find((p) => p.highlighted)!.id).toBe('large')
    expect(r.plans.find((p) => p.id === 'small')!.visible).toBe(false)
  })

  it('rechaza importes no válidos, dos destacados y planes de más o de menos', () => {
    const db = freshDb()
    const bad = (mut: (c: ReturnType<typeof readCatalog>) => void) => {
      const c = readCatalog(db)
      mut(c)
      return () => publishCatalog(db, c, actor)
    }
    expect(bad((c) => (c.plans[0]!.prices!.USD.monthly = -1))).toThrow()
    expect(bad((c) => (c.plans[0]!.prices!.USD.monthly = 1.234))).toThrow()
    expect(bad((c) => c.plans.forEach((p) => (p.highlighted = true)))).toThrow()
    expect(bad((c) => c.plans.pop())).toThrow()
    expect(bad((c) => (c.plans[0]!.name.es = ''))).toThrow()
    // Nada se ha publicado.
    expect(listPricingVersions(db)).toHaveLength(1)
  })
})

describe('caché de la landing', () => {
  it('sirve de caché durante el TTL e invalida al guardar', () => {
    const db = freshDb()
    let now = 1_000_000
    const cache = new SiteCache(
      () => db,
      () => null,
      30_000,
      () => now,
    )
    const price = () => cache.get().catalog.plans.find((p) => p.id === 'small')!.prices!.USD.monthly
    expect(price()).toBe(149)
    expect(cache.loads).toBe(1)

    const c = readCatalog(db)
    c.plans.find((p) => p.id === 'small')!.prices!.USD.monthly = 159
    publishCatalog(db, c, actor)
    // Sin invalidar (otro proceso): sigue el valor anterior hasta que vence el TTL.
    expect(price()).toBe(149)
    now += 30_000
    expect(price()).toBe(159)
    expect(cache.loads).toBe(2)

    // Mismo proceso: el panel invalida al guardar y se ve al momento.
    c.plans.find((p) => p.id === 'small')!.prices!.USD.monthly = 169
    publishCatalog(db, c, actor)
    cache.invalidate()
    expect(price()).toBe(169)
  })
})
