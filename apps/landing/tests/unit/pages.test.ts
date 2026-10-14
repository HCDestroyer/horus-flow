import { describe, expect, it } from 'vitest'
import { legalDocs, titular } from '../../app/content/legal'
import { seedIfEmpty } from '../../server/lib/catalog'
import { openDatabase, type DB } from '../../server/lib/db'
import { isSafeLink, renderMarkdown } from '../../server/lib/markdown'
import { migrations } from '../../server/lib/migrations'
import {
  PageError,
  createPage,
  deletePage,
  findPublishedPage,
  footerPages,
  getPage,
  getPageVersion,
  listPageVersions,
  listPages,
  publishedPagePaths,
  restorePageVersion,
  toPublicPage,
  updatePage,
  type PageInput,
} from '../../server/lib/pages'
import { SEED_PAGES, legalDocToMarkdown } from '../../server/lib/pages-seed'
import { SiteCache } from '../../server/lib/site-cache'

const actor = { id: 1, email: 'ana@kns.gt' }

function freshDb(): DB {
  const db = openDatabase(':memory:')
  seedIfEmpty(db)
  return db
}

function input(over: Partial<PageInput> = {}): PageInput {
  return {
    kind: 'info',
    slug: { es: 'quienes-somos', en: 'about-us' },
    title: { es: 'Quiénes somos', en: 'About us' },
    summary: { es: 'Resumen', en: 'Summary' },
    body: { es: '## Hola\n\nTexto.', en: '' },
    published: true,
    showInFooter: true,
    draftNotice: false,
    sort: 100,
    ...over,
  }
}

function pageError(fn: () => unknown): PageError {
  try {
    fn()
  } catch (err) {
    if (err instanceof PageError) return err
    throw err
  }
  throw new Error('no lanzó')
}

describe('migración 2: páginas', () => {
  it('es la siguiente versión y crea las tablas', () => {
    expect(migrations.map((m) => m.version)).toEqual([1, 2])
    const db = openDatabase(':memory:')
    const tables = (
      db.prepare("SELECT name FROM sqlite_master WHERE type = 'table'").all() as { name: string }[]
    ).map((t) => t.name)
    expect(tables).toEqual(expect.arrayContaining(['pages', 'page_versions']))
  })

  it('siembra las tres páginas legales originales y los cinco borradores nuevos', () => {
    const db = openDatabase(':memory:')
    const pages = listPages(db)
    expect(pages.map((p) => [p.id, p.slug.es, p.slug.en])).toEqual([
      ['notice', 'aviso-legal', 'notice'],
      ['privacy', 'privacidad', 'privacy'],
      ['terms', 'terminos', 'terms'],
      ['cookies', 'cookies', 'cookies'],
      ['acceptable-use', 'uso-aceptable', 'acceptable-use'],
      ['support', 'soporte', 'support'],
      ['ai', 'inteligencia-artificial', 'ai'],
      ['security', 'seguridad', 'security'],
    ])
    for (const p of pages) {
      expect(p.kind).toBe('legal')
      expect(p.published).toBe(true)
      expect(p.showInFooter).toBe(true)
      expect(p.draftNotice).toBe(true)
      expect(p.body.en).toBe('')
      expect(p.title.en.length).toBeGreaterThan(0)
      expect(p.summary.es.length).toBeGreaterThan(0)
      // Cada página tiene su versión inicial con la instantánea completa.
      const versions = listPageVersions(db, p.id)
      expect(versions).toHaveLength(1)
      expect(getPageVersion(db, p.id, versions[0]!.id)).toEqual(p)
    }
  })

  it('convierte los textos legales originales a Markdown sin perder una palabra', () => {
    const db = openDatabase(':memory:')
    for (const key of ['notice', 'privacy', 'terms'] as const) {
      const page = getPage(db, key)!
      const doc = legalDocs[key]
      expect(page.title.es).toBe(doc.title)
      expect(page.body.es).toBe(legalDocToMarkdown(doc))
      for (const s of doc.sections) {
        expect(page.body.es).toContain(`## ${s.title}\n`)
        for (const p of s.paragraphs) expect(page.body.es).toContain(p)
      }
      // El HTML tiene cada sección como <h2> y cada párrafo como <p>.
      const html = renderMarkdown(page.body.es)
      expect(html.match(/<h2>/g)).toHaveLength(doc.sections.length)
      expect(html.match(/<p>/g)).toHaveLength(doc.sections.flatMap((s) => s.paragraphs).length)
    }
    expect(getPage(db, 'notice')!.body.es).toContain(titular)
  })

  it('los borradores nuevos marcan los datos pendientes y usan los hechos del producto', () => {
    const db = openDatabase(':memory:')
    for (const id of ['cookies', 'acceptable-use', 'support', 'ai', 'security']) {
      const body = getPage(db, id)!.body.es
      expect(body).toContain('C&S Company')
    }
    for (const id of ['support', 'acceptable-use', 'security', 'cookies']) {
      expect(getPage(db, id)!.body.es).toContain('[POR CONFIRMAR')
    }
    const ai = getPage(db, 'ai')!.body.es
    for (const fact of [
      'desactivado',
      'Ollama',
      'cifrada',
      'Cliente 3 · 10.20.x.x',
      'solo lectura',
      'verificada',
      'nunca ejecuta nada en los routers',
      '90 días',
      'límite diario',
      'MCP',
    ]) {
      expect(ai).toContain(fact)
    }
    expect(SEED_PAGES).toHaveLength(8)
  })
})

describe('Markdown seguro', () => {
  it('el HTML del autor se muestra como texto, nunca como etiquetas', () => {
    const html = renderMarkdown(
      'Hola <script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n<div style="x">a</div>\n\n<iframe src="https://evil.example"></iframe>',
    )
    expect(html).not.toMatch(/<script|<img|<div|<iframe/i)
    expect(html).toContain('&lt;script&gt;')
    expect(html).toContain('&lt;img src=x onerror=alert(1)&gt;')
  })

  it('no enlaza javascript:, data:, vbscript:, file: ni protocolo relativo', () => {
    for (const url of [
      'javascript:alert(1)',
      'JaVaScRiPt:alert(1)',
      'java\tscript:alert(1)',
      ' javascript:alert(1)',
      'data:text/html;base64,PHNjcmlwdD4=',
      'vbscript:msgbox(1)',
      'file:///etc/passwd',
      '//evil.example/x',
      '\\\\evil.example',
    ]) {
      expect(isSafeLink(url), url).toBe(false)
      const html = renderMarkdown(`[clic](${url})`)
      expect(html, url).not.toContain('<a ')
    }
    // Entidades: markdown-it las decodifica antes de validar el enlace.
    expect(renderMarkdown('[x](javascript&#58;alert(1))')).not.toContain('<a ')
    expect(renderMarkdown('<javascript:alert(1)>')).not.toContain('<a ')
    expect(renderMarkdown('[x](javascript:alert(1))')).not.toMatch(/href=/)
  })

  it('enlaza http, https, mailto y relativos; los externos con rel="noopener noreferrer"', () => {
    expect(renderMarkdown('[web](https://kns.gt/x)')).toContain(
      '<a href="https://kns.gt/x" rel="noopener noreferrer">web</a>',
    )
    expect(renderMarkdown('[correo](mailto:info@kns.gt)')).toContain(
      '<a href="mailto:info@kns.gt">correo</a>',
    )
    expect(renderMarkdown('[privacidad](/legal/privacidad)')).toContain(
      '<a href="/legal/privacidad">privacidad</a>',
    )
    expect(renderMarkdown('[arriba](#seccion)')).toContain('<a href="#seccion">')
    expect(renderMarkdown('[otra](terminos)')).toContain('<a href="terminos">')
    // linkify: las URL sueltas también, con rel.
    expect(renderMarkdown('Visita https://horusflow.kns.gt hoy')).toContain(
      'rel="noopener noreferrer"',
    )
  })

  it('sin imágenes y con el # bajado a <h2> (el <h1> es el título de la página)', () => {
    expect(renderMarkdown('![x](https://evil.example/p.png)')).not.toContain('<img')
    const html = renderMarkdown('# Uno\n\n## Dos\n\n### Tres')
    expect(html).not.toContain('<h1')
    expect(html).toContain('<h2>Uno</h2>')
    expect(html).toContain('<h2>Dos</h2>')
    expect(html).toContain('<h3>Tres</h3>')
  })

  it('listas, negrita y párrafos', () => {
    const html = renderMarkdown('- **uno**\n- dos\n\n1. a\n2. b\n\nFin.')
    expect(html).toContain('<ul>')
    expect(html).toContain('<ol>')
    expect(html).toContain('<strong>uno</strong>')
    expect(html).toContain('<p>Fin.</p>')
  })
})

describe('validación y guardado', () => {
  it('crea una página informativa con su primera versión', () => {
    const db = freshDb()
    const { page, version } = createPage(db, input(), actor)
    expect(page.id).toMatch(/^p-[0-9a-f]{10}$/)
    expect(page.updatedBy).toBe('ana@kns.gt')
    expect(listPageVersions(db, page.id).map((v) => v.id)).toEqual([version])
  })

  it('rechaza slugs con mayúsculas, acentos, espacios, guiones sueltos o palabras reservadas', () => {
    const db = freshDb()
    for (const es of [
      'Mayusculas',
      'política',
      'con espacio',
      '-guion',
      'guion-',
      'doble--guion',
      'admin',
      'api',
      'comprar',
      'en',
      '',
      'a'.repeat(81),
      '../admin',
    ]) {
      expect(() => createPage(db, input({ slug: { es, en: 'ok-en' } }), actor), es).toThrow()
    }
    expect(() => createPage(db, input({ title: { es: '', en: '' } }), actor)).toThrow()
    expect(() => createPage(db, { ...input(), kind: 'otra' }, actor)).toThrow()
    expect(() => createPage(db, { ...input(), sort: -1 }, actor)).toThrow()
    expect(listPages(db)).toHaveLength(8)
  })

  it('un slug no puede repetirse en ningún idioma (tampoco cruzado)', () => {
    const db = freshDb()
    expect(
      pageError(() => createPage(db, input({ slug: { es: 'privacidad', en: 'x-y' } }), actor)),
    ).toMatchObject({ code: 'SLUG_TAKEN', path: 'slug.es' })
    // "privacy" es el slug inglés de otra página: tampoco vale como slug español.
    expect(
      pageError(() => createPage(db, input({ slug: { es: 'privacy', en: 'x-y' } }), actor)),
    ).toMatchObject({ code: 'SLUG_TAKEN', path: 'slug.es' })
    expect(
      pageError(() => createPage(db, input({ slug: { es: 'x-y', en: 'aviso-legal' } }), actor)),
    ).toMatchObject({ code: 'SLUG_TAKEN', path: 'slug.en' })
    // Una página puede usar el mismo slug en los dos idiomas.
    expect(() => createPage(db, input({ slug: { es: 'faq', en: 'faq' } }), actor)).not.toThrow()
  })

  it('guardar crea versión nueva, detecta conflictos y no cambia el tipo', () => {
    const db = freshDb()
    const before = getPage(db, 'privacy')!
    const next = { ...before, title: { ...before.title, es: 'Privacidad (v2)' } }
    const r = updatePage(db, 'privacy', next, actor, {
      note: 'Cambio de título',
      expectedUpdatedAt: before.updatedAt,
      now: Date.parse('2026-10-20T10:00:00Z'),
    })
    expect(r.before.title.es).toBe('Política de privacidad')
    expect(r.page.title.es).toBe('Privacidad (v2)')
    expect(r.page.updatedAt).toBe('2026-10-20T10:00:00.000Z')
    expect(listPageVersions(db, 'privacy').map((v) => v.note)).toEqual([
      'Cambio de título',
      'Semilla inicial (migración 2)',
    ])
    // Con el updatedAt antiguo: otra persona guardó entretanto.
    expect(
      pageError(() =>
        updatePage(db, 'privacy', next, actor, { expectedUpdatedAt: before.updatedAt }),
      ),
    ).toMatchObject({ code: 'CONFLICT', status: 409 })
    expect(
      pageError(() => updatePage(db, 'privacy', { ...next, kind: 'info' }, actor)),
    ).toMatchObject({ code: 'KIND_LOCKED' })
    expect(pageError(() => updatePage(db, 'no-existe', next, actor))).toMatchObject({
      code: 'NOT_FOUND',
      status: 404,
    })
  })

  it('las legales no se borran; las informativas sí (con su historial)', () => {
    const db = freshDb()
    expect(pageError(() => deletePage(db, 'terms'))).toMatchObject({
      code: 'LEGAL_NOT_DELETABLE',
      status: 409,
    })
    const { page } = createPage(db, input(), actor)
    deletePage(db, page.id)
    expect(getPage(db, page.id)).toBeNull()
    expect(listPageVersions(db, page.id)).toEqual([])
  })

  it('restaurar vuelve a guardar una versión anterior como versión nueva', () => {
    const db = freshDb()
    const original = getPage(db, 'terms')!
    const first = listPageVersions(db, 'terms')[0]!.id
    updatePage(db, 'terms', { ...original, body: { es: 'Otro texto', en: '' } }, actor)
    const r = restorePageVersion(db, 'terms', first, actor)
    expect(r.page.body.es).toBe(original.body.es)
    const versions = listPageVersions(db, 'terms')
    expect(versions).toHaveLength(3)
    expect(versions[0]).toMatchObject({ restoredFrom: first })
    expect(pageError(() => restorePageVersion(db, 'terms', 99_999, actor))).toMatchObject({
      code: 'VERSION_NOT_FOUND',
    })
    // Una versión de otra página no sirve.
    const other = listPageVersions(db, 'privacy')[0]!.id
    expect(pageError(() => restorePageVersion(db, 'terms', other, actor))).toMatchObject({
      code: 'VERSION_NOT_FOUND',
    })
  })
})

describe('lectura pública', () => {
  it('sin cuerpo en inglés muestra el español (y título/resumen en inglés si los hay)', () => {
    const db = freshDb()
    const p = toPublicPage(getPage(db, 'privacy')!, 'en')
    expect(p).toMatchObject({
      title: 'Privacy policy',
      titleLocale: 'en',
      contentLocale: 'es',
      paths: { es: '/legal/privacidad', en: '/en/legal/privacy' },
    })
    expect(p.html).toContain('<h2>1. Responsable</h2>')
    const withEnglish = updatePage(
      db,
      'privacy',
      { ...getPage(db, 'privacy')!, body: { es: 'ES', en: '## English' } },
      actor,
    ).page
    expect(toPublicPage(withEnglish, 'en')).toMatchObject({
      contentLocale: 'en',
      html: '<h2>English</h2>\n',
    })
  })

  it('busca por el slug del idioma y, si no, por el del otro; nunca las no publicadas', () => {
    const db = freshDb()
    expect(findPublishedPage(db, 'privacy', 'en')?.id).toBe('privacy')
    expect(findPublishedPage(db, 'privacidad', 'en')?.id).toBe('privacy')
    expect(findPublishedPage(db, 'notice', 'es')?.id).toBe('notice')
    expect(findPublishedPage(db, 'no-existe', 'es')).toBeNull()
    expect(findPublishedPage(db, "x' OR 1=1 --", 'es')).toBeNull()
    const p = getPage(db, 'cookies')!
    updatePage(db, 'cookies', { ...p, published: false }, actor)
    expect(findPublishedPage(db, 'cookies', 'es')).toBeNull()
    expect(publishedPagePaths(db).map((x) => x.es)).not.toContain('/legal/cookies')
    expect(footerPages(db).map((x) => x.id)).not.toContain('cookies')
  })

  it('pie: publicadas con "mostrar en el pie", en orden y con las rutas de cada idioma', () => {
    const db = freshDb()
    const p = getPage(db, 'ai')!
    updatePage(db, 'ai', { ...p, showInFooter: false }, actor)
    createPage(db, input({ sort: 5 }), actor)
    const links = footerPages(db)
    expect(links[0]).toMatchObject({
      kind: 'info',
      path: { es: '/info/quienes-somos', en: '/en/info/about-us' },
    })
    expect(links.map((l) => l.id)).not.toContain('ai')
    expect(links.find((l) => l.id === 'terms')).toMatchObject({
      title: { es: 'Términos y condiciones', en: 'Terms and conditions' },
      path: { es: '/legal/terminos', en: '/en/legal/terms' },
    })
  })
})

describe('caché de páginas', () => {
  it('sirve de caché durante el TTL e invalida al guardar', () => {
    const db = freshDb()
    let now = 1_000_000
    const cache = new SiteCache(
      () => db,
      () => null,
      30_000,
      () => now,
    )
    expect(cache.page('terminos', 'es')?.title).toBe('Términos y condiciones')
    expect(cache.page('terminos', 'es')?.title).toBe('Términos y condiciones')
    expect(cache.pageLoads).toBe(1)
    expect(cache.get().pages.map((p) => p.id)).toContain('terms')

    const p = getPage(db, 'terms')!
    updatePage(db, 'terms', { ...p, title: { ...p.title, es: 'Términos v2' } }, actor)
    // Otro proceso: el valor anterior hasta que vence el TTL.
    expect(cache.page('terminos', 'es')?.title).toBe('Términos y condiciones')
    now += 30_000
    expect(cache.page('terminos', 'es')?.title).toBe('Términos v2')

    // Mismo proceso: el panel invalida al guardar y se ve al momento (página y pie).
    updatePage(db, 'terms', { ...getPage(db, 'terms')!, published: false }, actor)
    cache.invalidate()
    expect(cache.page('terminos', 'es')).toBeNull()
    expect(cache.get().pages.map((x) => x.id)).not.toContain('terms')
  })
})
