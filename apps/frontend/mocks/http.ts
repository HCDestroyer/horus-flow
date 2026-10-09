import type { ProblemDetails } from '~~/types/api'

/** Respuestas HTTP de la API simulada (JSON y `application/problem+json`, RFC 9457). */

export function json(body: unknown, status = 200, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

export function text(body: string, status = 200, headers: Record<string, string> = {}) {
  return new Response(body, {
    status,
    headers: {
      'Content-Type': 'text/plain; charset=utf-8',
      'Cache-Control': 'no-store',
      ...headers,
    },
  })
}

export function noContent() {
  return new Response(null, { status: 204 })
}

export function randomHex(length: number) {
  const bytes = new Uint8Array(length / 2)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

export function problem(
  status: number,
  code: string,
  title: string,
  headers: Record<string, string> = {},
  extra: Partial<ProblemDetails> = {},
) {
  const body: ProblemDetails = {
    type: `https://docs.horus-flow.local/errors/${code.toLowerCase().replaceAll('_', '-')}`,
    title,
    status,
    code,
    trace_id: randomHex(32),
    ...extra,
  }
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/problem+json', ...headers },
  })
}

export const notFound = (code = 'NOT_FOUND') => problem(404, code, 'No encontrado')
export const forbidden = () => problem(403, 'PERMISSION_DENIED', 'Sin permiso')

/** `If-Match: "<version>"` → número (o `null` si falta). */
export function ifMatch(req: Request) {
  const raw = req.headers.get('If-Match')
  if (!raw) return null
  const n = Number(raw.replace(/^W\//, '').replaceAll('"', ''))
  return Number.isFinite(n) ? n : null
}

/** Paginación por cursor opaco (aquí, un desplazamiento en base64). */
export function paginate<T>(items: T[], url: URL, defaultLimit = 50) {
  const limit = Math.min(200, Math.max(1, Number(url.searchParams.get('limit') ?? defaultLimit)))
  const cursor = url.searchParams.get('cursor')
  const offset = cursor ? Number(atob(cursor)) || 0 : 0
  const data = items.slice(offset, offset + limit)
  const more = offset + limit < items.length
  return {
    data,
    page: {
      limit,
      has_more: more,
      next_cursor: more ? btoa(String(offset + limit)) : null,
      prev_cursor: offset > 0 ? btoa(String(Math.max(0, offset - limit))) : null,
      total: items.length,
    },
  }
}
