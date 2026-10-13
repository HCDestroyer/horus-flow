import { describe, expect, it } from 'vitest'
import { parseInline, parseMarkdown } from '~/utils/markdown'

describe('markdown seguro del asistente', () => {
  it('títulos, listas, código y citas', () => {
    const blocks = parseMarkdown(
      [
        '## Qué está pasando',
        'El **Cliente 1** contactó `203.0.113.9`.',
        '',
        '1. Primero',
        '2. Segundo',
        '   continuación',
        '- viñeta',
        '```routeros',
        '/ip firewall filter print',
        '```',
        '> mensaje sugerido',
        '---',
      ].join('\n'),
    )
    expect(blocks.map((b) => b.type)).toEqual([
      'heading',
      'paragraph',
      'list',
      'list',
      'code',
      'quote',
      'hr',
    ])
    expect(blocks[0]).toMatchObject({ level: 2 })
    expect(blocks[2]).toMatchObject({ ordered: true })
    const list = blocks[2] as { items: { text: string }[][] }
    expect(list.items[1]![0]!.text).toBe('Segundo continuación')
    expect(blocks[4]).toEqual({ type: 'code', lang: 'routeros', text: '/ip firewall filter print' })
  })

  it('nunca produce HTML: las etiquetas quedan como texto', () => {
    const blocks = parseMarkdown('<script>alert(1)</script>\n<img src=x onerror=alert(1)>')
    expect(blocks).toEqual([
      {
        type: 'paragraph',
        inline: [{ type: 'text', text: '<script>alert(1)</script> <img src=x onerror=alert(1)>' }],
      },
    ])
  })

  it('los enlaces no son clicables (texto con su URL)', () => {
    expect(parseInline('ver [docs](javascript:alert(1)) y [web](https://x.example)')).toEqual([
      { type: 'text', text: 'ver docs (javascript:alert(1)) y web (https://x.example)' },
    ])
  })

  it('negrita, cursiva y código en línea', () => {
    expect(parseInline('a **b** _c_ *d* `e*f*`')).toEqual([
      { type: 'text', text: 'a ' },
      { type: 'strong', text: 'b' },
      { type: 'text', text: ' ' },
      { type: 'em', text: 'c' },
      { type: 'text', text: ' ' },
      { type: 'em', text: 'd' },
      { type: 'text', text: ' ' },
      { type: 'code', text: 'e*f*' },
    ])
    // snake_case no es cursiva.
    expect(parseInline('traffic_flow_target_drift')).toEqual([
      { type: 'text', text: 'traffic_flow_target_drift' },
    ])
  })

  it('h1 se rebaja a h2 y h6 a h4', () => {
    expect(parseMarkdown('# A\n###### B').map((b) => (b as { level: number }).level)).toEqual([
      2, 4,
    ])
  })
})
