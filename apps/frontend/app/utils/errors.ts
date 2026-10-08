import { ApiError, NetworkError } from './api-client'

export interface ErrorDescription {
  /** Clave i18n del mensaje principal. */
  messageKey: string
  params?: Record<string, unknown>
  code?: string
  traceId?: string
  /** ¿Tiene sentido reintentar? */
  retryable: boolean
}

/**
 * Traduce un error a copia para el usuario (frontend.md §9.3): qué pasó y qué hacer, sin
 * culpar; el detalle técnico (code, trace_id) va plegado.
 */
export function describeError(error: unknown): ErrorDescription {
  if (error instanceof NetworkError) {
    return { messageKey: 'errors.network', retryable: true }
  }
  if (error instanceof ApiError) {
    const base = { code: error.code, traceId: error.problem.trace_id }
    switch (error.status) {
      case 403:
        return { ...base, messageKey: 'errors.forbidden.title', retryable: false }
      case 404:
        return { ...base, messageKey: 'errors.notFound.title', retryable: false }
      case 429:
        return {
          ...base,
          messageKey: 'errors.rateLimited',
          params: { seconds: error.retryAfter ?? 30 },
          retryable: true,
        }
      default:
        if (error.status >= 500) return { ...base, messageKey: 'errors.service', retryable: true }
        return { ...base, messageKey: 'errors.generic.title', retryable: true }
    }
  }
  return { messageKey: 'errors.generic.title', retryable: true }
}
