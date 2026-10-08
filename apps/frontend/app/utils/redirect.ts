/**
 * Solo permite redirecciones internas tras el login (evita open redirect: `//evil.com`,
 * `https://…`, `javascript:`).
 */
export function safeRedirect(target: unknown, fallback = '/') {
  if (typeof target !== 'string') return fallback
  if (!target.startsWith('/') || target.startsWith('//') || target.startsWith('/\\')) {
    return fallback
  }
  if (target.startsWith('/login')) return fallback
  return target
}
