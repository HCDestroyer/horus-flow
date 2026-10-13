/**
 * Markdown seguro para las respuestas del asistente de IA (docs/assist.md): un subconjunto
 * pequeño (títulos, párrafos, listas, citas, separadores, bloques de código y en línea negrita,
 * cursiva y código) convertido a un árbol de datos que la vista pinta con plantillas Vue. Nunca
 * se genera HTML ni se usa `v-html`: cualquier etiqueta del texto se muestra tal cual, como
 * texto. Los enlaces no son clicables (se muestran como texto con su URL): una respuesta
 * generada no debe poder llevar a ningún sitio.
 */

export type MdInline =
  | { type: 'text'; text: string }
  | { type: 'strong'; text: string }
  | { type: 'em'; text: string }
  | { type: 'code'; text: string }

export type MdBlock =
  | { type: 'heading'; level: 2 | 3 | 4; inline: MdInline[] }
  | { type: 'paragraph'; inline: MdInline[] }
  | { type: 'list'; ordered: boolean; items: MdInline[][] }
  | { type: 'quote'; inline: MdInline[] }
  | { type: 'code'; lang: string; text: string }
  | { type: 'hr' }

/** Límite de tamaño procesado (una respuesta del asistente nunca debería superarlo). */
const MAX_INPUT = 60_000

/** Texto en línea → nodos (negrita `**x**`, cursiva `*x*`/`_x_`, código `` `x` ``, enlaces como texto). */
export function parseInline(src: string): MdInline[] {
  const out: MdInline[] = []
  // Enlaces [texto](url) → "texto (url)" como texto plano.
  const text = src.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, '$1 ($2)')
  const re =
    /(`+)([^`]+?)\1|\*\*([^*]+?)\*\*|__([^_]+?)__|\*([^*\s][^*]*?)\*|(?<![\w])_([^_\s][^_]*?)_(?![\w])/g
  let last = 0
  for (const m of text.matchAll(re)) {
    const i = m.index ?? 0
    if (i > last) out.push({ type: 'text', text: text.slice(last, i) })
    if (m[2] !== undefined) out.push({ type: 'code', text: m[2] })
    else if (m[3] !== undefined || m[4] !== undefined)
      out.push({ type: 'strong', text: (m[3] ?? m[4])! })
    else out.push({ type: 'em', text: (m[5] ?? m[6])! })
    last = i + m[0].length
  }
  if (last < text.length) out.push({ type: 'text', text: text.slice(last) })
  return merge(out)
}

function merge(nodes: MdInline[]) {
  const out: MdInline[] = []
  for (const n of nodes) {
    const prev = out[out.length - 1]
    if (n.type === 'text' && prev?.type === 'text') prev.text += n.text
    else if (n.text !== '') out.push(n)
  }
  return out
}

const LIST_RE = /^\s{0,6}([-*+]|\d{1,3}[.)])\s+(.*)$/

/** Markdown → bloques. */
export function parseMarkdown(src: string): MdBlock[] {
  const lines = src.slice(0, MAX_INPUT).replace(/\r\n?/g, '\n').split('\n')
  const blocks: MdBlock[] = []
  let para: string[] = []
  const flush = () => {
    if (para.length) blocks.push({ type: 'paragraph', inline: parseInline(para.join(' ')) })
    para = []
  }
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!
    const fence = line.match(/^\s*(```+|~~~+)\s*([\w+-]*)/)
    if (fence) {
      flush()
      const marker = fence[1]!
      const body: string[] = []
      i++
      while (i < lines.length && !lines[i]!.trim().startsWith(marker)) body.push(lines[i++]!)
      blocks.push({ type: 'code', lang: (fence[2] ?? '').toLowerCase(), text: body.join('\n') })
      continue
    }
    if (!line.trim()) {
      flush()
      continue
    }
    const heading = line.match(/^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/)
    if (heading) {
      flush()
      const level = Math.min(4, Math.max(2, heading[1]!.length)) as 2 | 3 | 4
      blocks.push({ type: 'heading', level, inline: parseInline(heading[2]!) })
      continue
    }
    if (/^\s{0,3}([-*_])(\s*\1){2,}\s*$/.test(line)) {
      flush()
      blocks.push({ type: 'hr' })
      continue
    }
    const quote = line.match(/^\s{0,3}>\s?(.*)$/)
    if (quote) {
      flush()
      const body = [quote[1]!]
      while (i + 1 < lines.length && /^\s{0,3}>/.test(lines[i + 1]!)) {
        body.push(lines[++i]!.replace(/^\s{0,3}>\s?/, ''))
      }
      blocks.push({ type: 'quote', inline: parseInline(body.join(' ')) })
      continue
    }
    const item = line.match(LIST_RE)
    if (item) {
      flush()
      const ordered = /\d/.test(item[1]!)
      const items: string[] = [item[2]!]
      while (i + 1 < lines.length) {
        const next = lines[i + 1]!
        const m = next.match(LIST_RE)
        if (m && /\d/.test(m[1]!) !== ordered) break
        if (m) {
          items.push(m[2]!)
          i++
        } else if (next.trim() && /^\s{2,}\S/.test(next) && !/^\s*(```|~~~)/.test(next)) {
          // Continuación del elemento anterior (línea sangrada).
          items[items.length - 1] += ' ' + next.trim()
          i++
        } else break
      }
      blocks.push({ type: 'list', ordered, items: items.map(parseInline) })
      continue
    }
    para.push(line.trim())
  }
  flush()
  return blocks
}
