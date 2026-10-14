// Utilidades de las rutas de /api/admin/pages (páginas editables).
import type { H3Event } from 'h3'
import { z } from 'zod'
import { isPageId, PageError } from '../lib/pages'

/** Tope del cuerpo al guardar una página: dos cuerpos en Markdown de hasta 100 000 caracteres. */
export const PAGE_BODY_MAX_BYTES = 512 * 1024

export function pageIdParam(event: H3Event): string {
  const id = getRouterParam(event, 'id')
  if (!isPageId(id)) throw createError({ statusCode: 400, data: { code: 'BAD_ID' } })
  return id
}

export function versionIdParam(event: H3Event): number {
  const id = Number(getRouterParam(event, 'version'))
  if (!Number.isInteger(id) || id <= 0)
    throw createError({ statusCode: 400, data: { code: 'BAD_ID' } })
  return id
}

/** Traduce los errores de server/lib/pages.ts (y de zod) a respuestas HTTP. */
export function pageFail(err: unknown): never {
  if (err instanceof PageError) {
    throw createError({
      statusCode: err.status,
      data: {
        code: err.code,
        issues: err.path ? [{ path: err.path, message: err.code }] : undefined,
      },
    })
  }
  if (err instanceof z.ZodError) throw validationError(err)
  throw err
}
