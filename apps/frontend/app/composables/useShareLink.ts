/**
 * Compartir o copiar un enlace (invitaciones, I1).
 *
 * - Compartir: hoja nativa del sistema (Web Share API). Solo se ofrece si el navegador la
 *   tiene y no rechaza la URL (`canShare`); cancelar la hoja (`AbortError`) no es un error.
 * - Copiar: `navigator.clipboard` (contexto seguro) y, si falla o no existe (HTTP en una LAN,
 *   iframe), selección del campo + `document.execCommand('copy')`.
 *
 * Las funciones reciben `navigator`/`document` como parámetro para poder probarlas.
 */

type ShareNavigator = Pick<Navigator, 'share' | 'canShare'> | undefined
type ClipboardNavigator = { clipboard?: Pick<Clipboard, 'writeText'> } | undefined

export interface ShareLinkData {
  title: string
  text: string
  url: string
}

export type ShareResult = 'shared' | 'aborted' | 'unavailable' | 'failed'

const defaultNavigator = () => (typeof navigator === 'undefined' ? undefined : navigator)

/** ¿Se puede ofrecer "Compartir"? `navigator.share` existe y `canShare` no rechaza la URL. */
export function canShareUrl(url: string, nav: ShareNavigator = defaultNavigator()): boolean {
  if (!nav || typeof nav.share !== 'function') return false
  try {
    return nav.canShare?.({ url }) !== false
  } catch {
    return false
  }
}

export async function shareUrl(
  data: ShareLinkData,
  nav: ShareNavigator = defaultNavigator(),
): Promise<ShareResult> {
  if (!canShareUrl(data.url, nav)) return 'unavailable'
  try {
    await nav!.share(data)
    return 'shared'
  } catch (error) {
    // El usuario cerró la hoja de compartir: nada que avisar.
    if ((error as { name?: string })?.name === 'AbortError') return 'aborted'
    return 'failed'
  }
}

/**
 * Copia `text` al portapapeles. `input`: campo que contiene el texto, para el método de
 * reserva (se selecciona y se copia). Devuelve si se copió.
 */
export async function copyText(
  text: string,
  options: {
    input?: HTMLInputElement | HTMLTextAreaElement | null
    nav?: ClipboardNavigator
    doc?: Pick<Document, 'execCommand' | 'createElement' | 'body'>
  } = {},
): Promise<boolean> {
  const nav = 'nav' in options ? options.nav : (defaultNavigator() as ClipboardNavigator)
  if (nav?.clipboard?.writeText) {
    try {
      await nav.clipboard.writeText(text)
      return true
    } catch {
      // Sin permiso o contexto no seguro: método de reserva.
    }
  }
  const doc = options.doc ?? (typeof document === 'undefined' ? undefined : document)
  if (!doc) return false
  let field = options.input ?? null
  let temporary = false
  if (!field) {
    field = doc.createElement('textarea')
    field.value = text
    field.setAttribute('readonly', '')
    field.style.position = 'fixed'
    field.style.opacity = '0'
    doc.body.appendChild(field)
    temporary = true
  }
  try {
    field.focus()
    field.select()
    field.setSelectionRange?.(0, text.length)
    return doc.execCommand('copy')
  } catch {
    return false
  } finally {
    if (temporary) field.remove()
  }
}

export function useShareLink() {
  return { canShare: canShareUrl, share: shareUrl, copy: copyText }
}
