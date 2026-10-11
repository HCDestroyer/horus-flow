import { describe, expect, it, vi } from 'vitest'
import { canShareUrl, copyText, shareUrl } from '~/composables/useShareLink'

const URL_ = 'https://horus.example/invitation#token=abc'
const data = { title: 'Invitación', text: 'Abre el enlace', url: URL_ }

describe('useShareLink: compartir', () => {
  it('sin navigator.share no se ofrece', () => {
    expect(canShareUrl(URL_, {} as Navigator)).toBe(false)
    expect(canShareUrl(URL_, undefined)).toBe(false)
  })

  it('con share y sin canShare, se ofrece; canShare=false lo oculta', () => {
    const share = vi.fn()
    expect(canShareUrl(URL_, { share } as unknown as Navigator)).toBe(true)
    expect(canShareUrl(URL_, { share, canShare: () => false } as unknown as Navigator)).toBe(false)
    expect(canShareUrl(URL_, { share, canShare: () => true } as unknown as Navigator)).toBe(true)
  })

  it('comparte título, texto y URL', async () => {
    const share = vi.fn().mockResolvedValue(undefined)
    expect(await shareUrl(data, { share } as unknown as Navigator)).toBe('shared')
    expect(share).toHaveBeenCalledWith(data)
  })

  it('cerrar la hoja (AbortError) no es un fallo', async () => {
    const abort = Object.assign(new Error('cancel'), { name: 'AbortError' })
    const share = vi.fn().mockRejectedValue(abort)
    expect(await shareUrl(data, { share } as unknown as Navigator)).toBe('aborted')
    const other = vi.fn().mockRejectedValue(new TypeError('x'))
    expect(await shareUrl(data, { share: other } as unknown as Navigator)).toBe('failed')
    expect(await shareUrl(data, {} as Navigator)).toBe('unavailable')
  })
})

describe('useShareLink: copiar', () => {
  it('usa navigator.clipboard si está disponible', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    const doc = { execCommand: vi.fn() } as unknown as Document
    expect(await copyText(URL_, { nav: { clipboard: { writeText } }, doc })).toBe(true)
    expect(writeText).toHaveBeenCalledWith(URL_)
    expect(doc.execCommand).not.toHaveBeenCalled()
  })

  it('sin portapapeles (contexto no seguro): selecciona el campo y execCommand', async () => {
    const input = {
      focus: vi.fn(),
      select: vi.fn(),
      setSelectionRange: vi.fn(),
    } as unknown as HTMLInputElement
    const execCommand = vi.fn().mockReturnValue(true)
    const doc = { execCommand } as unknown as Document
    expect(await copyText(URL_, { nav: {}, doc, input })).toBe(true)
    expect(input.select).toHaveBeenCalled()
    expect(execCommand).toHaveBeenCalledWith('copy')
  })

  it('si writeText falla, también recurre a la selección', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'))
    const input = { focus: vi.fn(), select: vi.fn() } as unknown as HTMLInputElement
    const doc = { execCommand: vi.fn().mockReturnValue(false) } as unknown as Document
    expect(await copyText(URL_, { nav: { clipboard: { writeText } }, doc, input })).toBe(false)
    expect(input.select).toHaveBeenCalled()
  })
})
