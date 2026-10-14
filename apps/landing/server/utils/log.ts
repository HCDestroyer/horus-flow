// Registro estructurado (una línea JSON por evento). Nunca se registran datos personales
// completos: el correo va enmascarado y no se registran nombres, teléfonos ni mensajes.

export type LogLevel = 'info' | 'warn' | 'error'
export type LogFn = (level: LogLevel, event: string, fields?: Record<string, unknown>) => void

export const log: LogFn = (level, event, fields = {}) => {
  const line = JSON.stringify({ time: new Date().toISOString(), level, event, ...fields })
  if (level === 'error') process.stderr.write(line + '\n')
  else process.stdout.write(line + '\n')
}

/** a***@dominio.com: suficiente para correlacionar sin guardar el correo. */
export function maskEmail(email: string): string {
  const at = email.lastIndexOf('@')
  if (at < 1) return '***'
  return `${email[0]}***${email.slice(at)}`
}

/** 203.0.113.x / 2001:db8:1:: (prefijo /48): para correlacionar abusos sin la IP completa. */
export function maskIp(ip: string): string {
  if (ip.includes('.')) return ip.split('.').slice(0, 3).join('.') + '.x'
  if (ip.includes(':')) return ip.split(':').slice(0, 3).join(':') + '::'
  return 'unknown'
}
