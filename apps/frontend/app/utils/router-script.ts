/**
 * Divide el script de alta de RouterOS en partes para pegarlas de una en una. Pegado entero, lo
 * que llega mientras RouterOS ejecuta un comando lento (`/certificate sign` muestra "progress" y
 * tarda segundos) se pierde y el resto del script queda a medias. Las partes siguen los
 * encabezados `# N) …` del script; un `/certificate sign` cierra su parte para que lo siguiente
 * se pegue cuando la firma haya terminado. Las líneas anteriores al primer encabezado que solo
 * son comentarios no forman parte.
 */
export interface ScriptPart {
  /** Título del encabezado (`Tnel WireGuard…`), vacío si no lo hay. */
  title: string
  code: string
  /** La parte termina en una orden lenta: esperar a que acabe antes de la siguiente. */
  waitAfter: boolean
}

const HEADER = /^#\s*(\d+)\)\s*(.*)$/
const SLOW = /^\s*\/certificate\s+sign\b/

export function splitRouterScript(script: string): ScriptPart[] {
  const parts: ScriptPart[] = []
  let title = ''
  let lines: string[] = []
  const flush = (waitAfter: boolean) => {
    const code = lines.join('\n').trim()
    const onlyComments = code.split('\n').every((l) => l.trim() === '' || l.trim().startsWith('#'))
    if (code && !(onlyComments && parts.length === 0 && !title)) {
      parts.push({ title, code: code + '\n', waitAfter })
    }
    lines = []
  }
  let open = 0 // profundidad de bloques { } (nunca se corta dentro)
  for (const line of script.split('\n')) {
    const m = open === 0 ? HEADER.exec(line.trim()) : null
    if (m) {
      flush(false)
      title = m[2]!.trim()
    }
    lines.push(line)
    open += (line.match(/\{/g)?.length ?? 0) - (line.match(/\}/g)?.length ?? 0)
    if (open === 0 && SLOW.test(line) && !line.trimEnd().endsWith('\\')) flush(true)
  }
  flush(false)
  return parts
}
