// Páginas editables (legales e informativas): lectura pública, validación, guardado con
// historial de versiones (instantánea completa, como pricing_versions) y restauración.
//
// Reglas:
// - Slugs en minúsculas (a-z, 0-9 y guiones), únicos en cada idioma y sin chocar con el slug
//   de otra página en el otro idioma (así /en/legal/<slug-es> puede redirigir sin ambigüedad).
//   Las palabras reservadas (admin, api, comprar, en…) se rechazan.
// - El tipo (legal/info) se fija al crear: decide la URL (/legal/… o /info/…).
// - Las páginas legales no se borran: se despublican. Las informativas sí se pueden borrar.
// - Sin cuerpo en inglés, la versión inglesa muestra el texto en español con el aviso "solo en
//   español" (contentLocale = 'es'); sin título o resumen en inglés, usa los españoles.
import { randomBytes } from 'node:crypto'
import { z } from 'zod'
import {
  PAGE_KINDS,
  RESERVED_SLUGS,
  SLUG_MAX,
  SLUG_PATTERN,
  pagePath,
  type AdminPage,
  type PageLocale,
  type PageVersionSummary,
  type PublicPage,
  type SitePageLink,
} from '../../shared/pages'
import { nowIso, type DB } from './db'
import { MARKDOWN_MAX, renderMarkdown } from './markdown'

interface PageRow {
  id: string
  kind: AdminPage['kind']
  slug_es: string
  slug_en: string
  title_es: string
  title_en: string
  summary_es: string
  summary_en: string
  body_es: string
  body_en: string
  published: number
  show_in_footer: number
  draft_notice: number
  sort: number
  created_at: string
  updated_at: string
  updated_by: string
}

function fromRow(r: PageRow): AdminPage {
  return {
    id: r.id,
    kind: r.kind,
    slug: { es: r.slug_es, en: r.slug_en },
    title: { es: r.title_es, en: r.title_en },
    summary: { es: r.summary_es, en: r.summary_en },
    body: { es: r.body_es, en: r.body_en },
    published: r.published === 1,
    showInFooter: r.show_in_footer === 1,
    draftNotice: r.draft_notice === 1,
    sort: r.sort,
    createdAt: r.created_at,
    updatedAt: r.updated_at,
    updatedBy: r.updated_by,
  }
}

// ---- Validación ----------------------------------------------------------------------------

const slug = z
  .string()
  .trim()
  .min(1, 'required')
  .max(SLUG_MAX, 'tooLong')
  .regex(SLUG_PATTERN, 'slugFormat')
  .refine((s) => !RESERVED_SLUGS.has(s), 'slugReserved')

const text = (max: number) => z.string().trim().max(max, 'tooLong')

/** Lo que el panel envía al crear o guardar una página. */
export const pageInputSchema = z.object({
  kind: z.enum(PAGE_KINDS),
  slug: z.object({ es: slug, en: slug }),
  title: z.object({ es: text(160).min(1, 'required'), en: text(160) }),
  summary: z.object({ es: text(300), en: text(300) }),
  body: z.object({
    es: z.string().max(MARKDOWN_MAX, 'tooLong'),
    en: z.string().max(MARKDOWN_MAX, 'tooLong'),
  }),
  published: z.boolean(),
  showInFooter: z.boolean(),
  draftNotice: z.boolean(),
  sort: z.number().int().min(0).max(100_000),
})

export type PageInput = z.output<typeof pageInputSchema>

export class PageError extends Error {
  constructor(
    readonly code:
      | 'NOT_FOUND'
      | 'SLUG_TAKEN'
      | 'LEGAL_NOT_DELETABLE'
      | 'KIND_LOCKED'
      | 'CONFLICT'
      | 'VERSION_NOT_FOUND',
    readonly status: number,
    readonly path = '',
  ) {
    super(code)
  }
}

/** Comprueba que ningún slug choca con el de otra página (en cualquiera de los dos idiomas). */
function assertSlugsFree(db: DB, input: PageInput, selfId: string | null): void {
  const rows = db
    .prepare('SELECT id, slug_es, slug_en FROM pages WHERE id IS NOT ?')
    .all(selfId) as { id: string; slug_es: string; slug_en: string }[]
  for (const lang of ['es', 'en'] as const) {
    const s = input.slug[lang]
    if (rows.some((r) => r.slug_es === s || r.slug_en === s)) {
      throw new PageError('SLUG_TAKEN', 409, `slug.${lang}`)
    }
  }
}

// ---- Lectura -------------------------------------------------------------------------------

export function getPage(db: DB, id: string): AdminPage | null {
  const r = db.prepare('SELECT * FROM pages WHERE id = ?').get(id) as PageRow | undefined
  return r ? fromRow(r) : null
}

export function listPages(db: DB): AdminPage[] {
  return (db.prepare('SELECT * FROM pages ORDER BY sort, id').all() as PageRow[]).map(fromRow)
}

/** Enlaces del pie: páginas publicadas con "mostrar en el pie", en orden. */
export function footerPages(db: DB): SitePageLink[] {
  return (
    db
      .prepare(
        `SELECT id, kind, slug_es, slug_en, title_es, title_en FROM pages
         WHERE published = 1 AND show_in_footer = 1 ORDER BY sort, id`,
      )
      .all() as Pick<PageRow, 'id' | 'kind' | 'slug_es' | 'slug_en' | 'title_es' | 'title_en'>[]
  ).map((r) => ({
    id: r.id,
    kind: r.kind,
    title: { es: r.title_es, en: r.title_en || r.title_es },
    path: { es: pagePath(r.kind, 'es', r.slug_es), en: pagePath(r.kind, 'en', r.slug_en) },
  }))
}

/** Rutas de todas las páginas publicadas (sitemap). */
export function publishedPagePaths(db: DB): { es: string; en: string; updatedAt: string }[] {
  return (
    db
      .prepare(
        'SELECT kind, slug_es, slug_en, updated_at FROM pages WHERE published = 1 ORDER BY sort, id',
      )
      .all() as Pick<PageRow, 'kind' | 'slug_es' | 'slug_en' | 'updated_at'>[]
  ).map((r) => ({
    es: pagePath(r.kind, 'es', r.slug_es),
    en: pagePath(r.kind, 'en', r.slug_en),
    updatedAt: r.updated_at,
  }))
}

/** Página pública en un idioma: Markdown → HTML seguro, con los textos de respaldo en español. */
export function toPublicPage(p: AdminPage, locale: PageLocale): PublicPage {
  const hasEnglishBody = p.body.en.trim().length > 0
  const contentLocale: PageLocale = locale === 'en' && hasEnglishBody ? 'en' : 'es'
  const pick = (v: { es: string; en: string }) => (locale === 'en' && v.en.trim() ? v.en : v.es)
  return {
    id: p.id,
    kind: p.kind,
    locale,
    title: pick(p.title),
    titleLocale: locale === 'en' && p.title.en.trim() ? 'en' : 'es',
    summary: pick(p.summary),
    html: renderMarkdown(p.body[contentLocale]),
    contentLocale,
    draftNotice: p.draftNotice,
    updatedAt: p.updatedAt,
    slugs: { ...p.slug },
    paths: { es: pagePath(p.kind, 'es', p.slug.es), en: pagePath(p.kind, 'en', p.slug.en) },
  }
}

/**
 * Busca una página publicada por el slug de la URL: primero en el idioma pedido y, si no, en el
 * otro (p. ej. /en/legal/privacidad), para que la página redirija a su URL canónica.
 */
export function findPublishedPage(db: DB, slugValue: string, locale: PageLocale): AdminPage | null {
  if (!SLUG_PATTERN.test(slugValue) || slugValue.length > SLUG_MAX) return null
  const other = locale === 'en' ? 'es' : 'en'
  const row = (db
    .prepare(`SELECT * FROM pages WHERE published = 1 AND slug_${locale} = ?`)
    .get(slugValue) ??
    db.prepare(`SELECT * FROM pages WHERE published = 1 AND slug_${other} = ?`).get(slugValue)) as
    PageRow | undefined
  return row ? fromRow(row) : null
}

// ---- Escritura -----------------------------------------------------------------------------

export interface PageActor {
  id: number | null
  email: string
}

function insertVersion(
  db: DB,
  page: AdminPage,
  actor: PageActor,
  now: number,
  note: string,
  restoredFrom: number | null,
): number {
  const info = db
    .prepare(
      `INSERT INTO page_versions (page_id, created_at, admin_id, admin_email, note, restored_from, snapshot)
       VALUES (?, ?, ?, ?, ?, ?, ?)`,
    )
    .run(
      page.id,
      nowIso(now),
      actor.id,
      actor.email,
      note.slice(0, 200),
      restoredFrom,
      JSON.stringify(page),
    )
  return Number(info.lastInsertRowid)
}

function writeRow(
  db: DB,
  id: string,
  input: PageInput,
  actor: PageActor,
  now: number,
  insert: boolean,
) {
  const values = [
    input.slug.es,
    input.slug.en,
    input.title.es,
    input.title.en,
    input.summary.es,
    input.summary.en,
    input.body.es,
    input.body.en,
    input.published ? 1 : 0,
    input.showInFooter ? 1 : 0,
    input.draftNotice ? 1 : 0,
    input.sort,
    nowIso(now),
    actor.email,
  ]
  if (insert) {
    db.prepare(
      `INSERT INTO pages (slug_es, slug_en, title_es, title_en, summary_es, summary_en, body_es,
         body_en, published, show_in_footer, draft_notice, sort, updated_at, updated_by, id, kind, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    ).run(...values, id, input.kind, nowIso(now))
  } else {
    db.prepare(
      `UPDATE pages SET slug_es = ?, slug_en = ?, title_es = ?, title_en = ?, summary_es = ?,
         summary_en = ?, body_es = ?, body_en = ?, published = ?, show_in_footer = ?,
         draft_notice = ?, sort = ?, updated_at = ?, updated_by = ?
       WHERE id = ?`,
    ).run(...values, id)
  }
}

export const newPageId = () => `p-${randomBytes(5).toString('hex')}`

/** Crea una página (valida con pageInputSchema). Devuelve la página y su primera versión. */
export function createPage(
  db: DB,
  raw: unknown,
  actor: PageActor,
  opts: { note?: string; now?: number; id?: string } = {},
): { page: AdminPage; version: number } {
  const input = pageInputSchema.parse(raw)
  const now = opts.now ?? Date.now()
  return db.transaction(() => {
    assertSlugsFree(db, input, null)
    const id = opts.id ?? newPageId()
    writeRow(db, id, input, actor, now, true)
    const page = getPage(db, id)!
    const version = insertVersion(db, page, actor, now, opts.note ?? 'Creada', null)
    return { page, version }
  })()
}

/**
 * Guarda una página existente. `expectedUpdatedAt` (el updatedAt que vio el editor) evita pisar
 * el cambio de otra persona: si no coincide, CONFLICT (409).
 */
export function updatePage(
  db: DB,
  id: string,
  raw: unknown,
  actor: PageActor,
  opts: { note?: string; now?: number; expectedUpdatedAt?: string; restoredFrom?: number } = {},
): { before: AdminPage; page: AdminPage; version: number } {
  const input = pageInputSchema.parse(raw)
  const now = opts.now ?? Date.now()
  return db.transaction(() => {
    const before = getPage(db, id)
    if (!before) throw new PageError('NOT_FOUND', 404)
    if (opts.expectedUpdatedAt && opts.expectedUpdatedAt !== before.updatedAt) {
      throw new PageError('CONFLICT', 409)
    }
    if (input.kind !== before.kind) throw new PageError('KIND_LOCKED', 422, 'kind')
    assertSlugsFree(db, input, id)
    writeRow(db, id, input, actor, now, false)
    const page = getPage(db, id)!
    const version = insertVersion(db, page, actor, now, opts.note ?? '', opts.restoredFrom ?? null)
    return { before, page, version }
  })()
}

/** Borra una página informativa (las legales solo se despublican). */
export function deletePage(db: DB, id: string): AdminPage {
  return db.transaction(() => {
    const page = getPage(db, id)
    if (!page) throw new PageError('NOT_FOUND', 404)
    if (page.kind === 'legal') throw new PageError('LEGAL_NOT_DELETABLE', 409)
    db.prepare('DELETE FROM pages WHERE id = ?').run(id)
    return page
  })()
}

export function listPageVersions(db: DB, pageId: string, limit = 50): PageVersionSummary[] {
  return (
    db
      .prepare(
        `SELECT id, created_at, admin_email, note, restored_from FROM page_versions
         WHERE page_id = ? ORDER BY id DESC LIMIT ?`,
      )
      .all(pageId, limit) as {
      id: number
      created_at: string
      admin_email: string | null
      note: string
      restored_from: number | null
    }[]
  ).map((r) => ({
    id: r.id,
    createdAt: r.created_at,
    adminEmail: r.admin_email,
    note: r.note,
    restoredFrom: r.restored_from,
  }))
}

export function getPageVersion(db: DB, pageId: string, versionId: number): AdminPage | null {
  const row = db
    .prepare('SELECT snapshot FROM page_versions WHERE id = ? AND page_id = ?')
    .get(versionId, pageId) as { snapshot: string } | undefined
  return row ? (JSON.parse(row.snapshot) as AdminPage) : null
}

/** Vuelve a publicar el contenido de una versión anterior (como versión nueva). */
export function restorePageVersion(
  db: DB,
  pageId: string,
  versionId: number,
  actor: PageActor,
  now = Date.now(),
) {
  const snap = getPageVersion(db, pageId, versionId)
  if (!snap) throw new PageError('VERSION_NOT_FOUND', 404)
  const input: PageInput = {
    kind: snap.kind,
    slug: snap.slug,
    title: snap.title,
    summary: snap.summary,
    body: snap.body,
    published: snap.published,
    showInFooter: snap.showInFooter,
    draftNotice: snap.draftNotice,
    sort: snap.sort,
  }
  return updatePage(db, pageId, input, actor, {
    note: `Restaurada la versión ${versionId}`,
    restoredFrom: versionId,
    now,
  })
}

/** Identificador de página en una URL del panel (clave de la semilla o p-xxxxxxxxxx). */
export function isPageId(id: unknown): id is string {
  return typeof id === 'string' && /^[a-z0-9-]{1,64}$/.test(id)
}
