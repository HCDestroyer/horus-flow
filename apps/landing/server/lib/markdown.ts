// Markdown → HTML seguro para las páginas editables (/admin › Páginas). Solo en el servidor:
// la web y la vista previa del panel reciben el HTML ya convertido.
//
// - markdown-it con `html: false`: el HTML que escriba el autor se muestra como texto escapado,
//   nunca como etiquetas.
// - Enlaces solo http, https, mailto y relativos (/ruta, #ancla, ruta); javascript:, data:,
//   vbscript:, file:… y los de protocolo relativo (//otro-sitio) se quedan como texto. Los
//   absolutos (http/https) llevan rel="noopener noreferrer".
// - Sin imágenes (la CSP solo admite imágenes propias): `![alt](url)` se queda como texto.
// - Los títulos `#` bajan a <h2>: el <h1> de la página es su título.
import MarkdownIt from 'markdown-it'

const SCHEME = /^([a-z][a-z0-9+.-]*):/i
const ALLOWED_SCHEMES = new Set(['http', 'https', 'mailto'])

/** ¿Se permite este destino de enlace? */
export function isSafeLink(raw: string): boolean {
  // markdown-it pasa la URL ya normalizada (con %XX): se comprueba también decodificada.
  try {
    return checkLink(raw) && checkLink(decodeURIComponent(raw))
  } catch {
    return false
  }
}

function checkLink(raw: string): boolean {
  // Quita espacios y caracteres de control que los navegadores ignoran al leer el esquema
  // (p. ej. "java\tscript:").
  // eslint-disable-next-line no-control-regex
  const url = raw.replace(/[\u0000- \u007f-\u009f]/g, '')
  if (!url) return false
  if (url.startsWith('//') || url.startsWith('\\') || url.startsWith('/\\')) return false
  const m = SCHEME.exec(url)
  if (m) return ALLOWED_SCHEMES.has(m[1]!.toLowerCase())
  // Sin esquema: relativo. Un ":" antes de la primera "/", "?" o "#" sería un esquema raro.
  const head = url.split(/[/?#]/, 1)[0] ?? ''
  return !head.includes(':')
}

function createRenderer() {
  const md = new MarkdownIt('default', {
    html: false,
    linkify: true,
    typographer: false,
    breaks: false,
  })
  md.disable(['image'])
  md.validateLink = isSafeLink

  const renderToken = md.renderer.renderToken.bind(md.renderer)

  md.renderer.rules.heading_open = (tokens, idx, options) => {
    const t = tokens[idx]!
    if (t.tag === 'h1') t.tag = 'h2'
    return renderToken(tokens, idx, options)
  }
  md.renderer.rules.heading_close = (tokens, idx, options) => {
    const t = tokens[idx]!
    if (t.tag === 'h1') t.tag = 'h2'
    return renderToken(tokens, idx, options)
  }
  md.renderer.rules.link_open = (tokens, idx, options) => {
    const t = tokens[idx]!
    const href = String(t.attrGet('href') ?? '')
    if (/^https?:/i.test(href)) t.attrSet('rel', 'noopener noreferrer')
    return renderToken(tokens, idx, options)
  }
  return md
}

const renderer = createRenderer()

/** Tamaño máximo de un cuerpo en Markdown (caracteres). */
export const MARKDOWN_MAX = 100_000

/** Convierte Markdown en HTML seguro. */
export function renderMarkdown(source: string): string {
  return renderer.render(source.slice(0, MARKDOWN_MAX))
}
