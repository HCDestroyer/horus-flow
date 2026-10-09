import type { Page } from '@playwright/test'

export interface LayoutProblem {
  /** `data-widget-id` del widget o selector del contenedor auditado. */
  where: string
  kind: 'cut' | 'outside' | 'overlap' | 'page-scroll'
  text: string
}

/**
 * Auditoría "sin desbordamiento" (I0-16). En cada contenedor auditado
 * (`[data-testid="widget"]` y los selectores de `extra`), ningún texto visible puede:
 * - quedar **cortado**: salirse de un ancestro que recorta (`overflow` ≠ `visible`), salvo el
 *   truncado deliberado con puntos suspensivos (nunca en cifras KPI);
 * - **salirse** del contenedor, de su tarjeta (`[data-layout-box]`) o de la ventana;
 * - **solaparse** con otro texto (rectángulos de los nodos de texto, `Range.getClientRects`).
 * Además, la página no puede tener scroll horizontal (y, con `noScroll`, tampoco vertical).
 *
 * Lo oculto a propósito (`display: none`, `visibility: hidden`, `.sr-only`) no cuenta:
 * ocultar lo secundario es la degradación correcta. El texto dentro de `<canvas>` (ECharts)
 * no es DOM: los widgets murales no dibujan etiquetas de texto propias en el canvas.
 */
/** Problemas como líneas legibles ("w-traffic-now cut: 232"), para el mensaje del test. */
export async function layoutReport(
  page: Page,
  options: { extra?: string[]; noScroll?: boolean } = {},
): Promise<string[]> {
  const problems = await layoutProblems(page, options)
  return problems.map((p) => `${p.where} ${p.kind}: ${p.text}`)
}

export async function layoutProblems(
  page: Page,
  options: { extra?: string[]; noScroll?: boolean } = {},
): Promise<LayoutProblem[]> {
  return page.evaluate(
    ({ extra, noScroll }) => {
      type Box = { left: number; right: number; top: number; bottom: number }
      const problems: { where: string; kind: string; text: string }[] = []
      const tol = 1
      const doc = document.documentElement
      const vw = doc.clientWidth
      const vh = window.innerHeight

      if (doc.scrollWidth > vw + tol) {
        problems.push({
          where: 'page',
          kind: 'page-scroll',
          text: `ancho ${doc.scrollWidth} > ${vw}`,
        })
      }
      if (noScroll && doc.scrollHeight > vh + tol) {
        problems.push({
          where: 'page',
          kind: 'page-scroll',
          text: `alto ${doc.scrollHeight} > ${vh}`,
        })
      }

      const ancestorsUntil = (el: Element, stop: Element) => {
        const out: Element[] = []
        let p = el.parentElement
        while (p && p !== stop) {
          out.push(p)
          p = p.parentElement
        }
        return out
      }
      const shown = (el: Element) => {
        if (el.closest('.sr-only')) return false
        const cs = getComputedStyle(el)
        if (cs.visibility !== 'visible') return false
        return el.getClientRects().length > 0
      }
      const isTruncation = (el: Element) => {
        const cs = getComputedStyle(el)
        return (
          cs.textOverflow === 'ellipsis' &&
          cs.whiteSpace === 'nowrap' &&
          !el.closest('.w-kpi, .w-kpi-2')
        )
      }
      const clips = (el: Element) => {
        const cs = getComputedStyle(el)
        return cs.overflowX !== 'visible' || cs.overflowY !== 'visible'
      }
      const box = (r: DOMRect | Box): Box => ({
        left: r.left,
        right: r.right,
        top: r.top,
        bottom: r.bottom,
      })
      const inter = (a: Box, b: Box): Box => ({
        left: Math.max(a.left, b.left),
        right: Math.min(a.right, b.right),
        top: Math.max(a.top, b.top),
        bottom: Math.min(a.bottom, b.bottom),
      })
      const inside = (a: Box, b: Box) =>
        a.left >= b.left - tol &&
        a.right <= b.right + tol &&
        a.top >= b.top - tol &&
        a.bottom <= b.bottom + tol
      // Las cajas de texto incluyen el interlineado interno de la fuente: para los solapes
      // se recortan un 20 % arriba y abajo (≈ la tinta de los glifos).
      const ink = (b: Box): Box => {
        const h = (b.bottom - b.top) * 0.2
        return { ...b, top: b.top + h, bottom: b.bottom - h }
      }
      const describe = (t: string) => (t.length > 48 ? `${t.slice(0, 47)}…` : t)

      const containers = [
        ...[...document.querySelectorAll<HTMLElement>('[data-testid="widget"]')].map((el) => ({
          where: el.dataset.widgetId ?? 'widget',
          el,
        })),
        ...extra.flatMap((sel) =>
          [...document.querySelectorAll<HTMLElement>(sel)].map((el) => ({ where: sel, el })),
        ),
      ]

      for (const { where, el: root } of containers) {
        if (!shown(root)) continue
        const rootBox = box(root.getBoundingClientRect())
        const texts: { el: Element; text: string; boxes: Box[] }[] = []
        const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
        while (walker.nextNode()) {
          const node = walker.currentNode as Text
          const text = node.textContent?.trim() ?? ''
          const el = node.parentElement
          if (!text || !el || !shown(el) || el.closest('svg, canvas, script, style, table caption'))
            continue
          const chain = [el, ...ancestorsUntil(el, root)]
          const range = document.createRange()
          range.selectNodeContents(node)
          let boxes = [...range.getClientRects()]
            .filter((r) => r.width > 0.5 && r.height > 0.5)
            .map(box)
          // Truncado deliberado: lo visible es la caja del elemento que pone los "…".
          const trunc = chain.find(isTruncation)
          if (trunc) {
            const t = box(trunc.getBoundingClientRect())
            boxes = boxes.map((b) => inter(b, t)).filter((b) => b.right - b.left > 0.5)
          }
          if (!boxes.length) continue

          let reported = false
          for (const a of [...chain, root]) {
            if (a === trunc || !clips(a)) continue
            const ab = box(a.getBoundingClientRect())
            if (boxes.some((b) => !inside(b, ab))) {
              problems.push({ where, kind: a === root ? 'outside' : 'cut', text: describe(text) })
              reported = true
              break
            }
          }
          if (!reported && boxes.some((b) => !inside(b, rootBox))) {
            problems.push({ where, kind: 'outside', text: describe(text) })
            reported = true
          }
          // Cajas visibles dentro del widget (tarjetas con fondo, `data-layout-box`): el texto
          // tampoco puede salirse de su fondo aunque no recorte.
          const tile = el.closest('[data-layout-box]')
          if (!reported && tile && root.contains(tile)) {
            if (boxes.some((b) => !inside(b, box(tile.getBoundingClientRect())))) {
              problems.push({
                where,
                kind: 'outside',
                text: `${describe(text)} (fuera de su tarjeta)`,
              })
              reported = true
            }
          }
          if (!reported && boxes.some((b) => b.left < -tol || b.right > vw + tol)) {
            problems.push({
              where,
              kind: 'outside',
              text: `${describe(text)} (fuera de la ventana)`,
            })
          }
          texts.push({ el, text, boxes })
        }

        for (let i = 0; i < texts.length; i++) {
          for (let j = i + 1; j < texts.length; j++) {
            const a = texts[i]!
            const b = texts[j]!
            if (a.el === b.el) continue
            const hit = a.boxes.some((x) =>
              b.boxes.some((y) => {
                const r = inter(ink(x), ink(y))
                return r.right - r.left > 1 && r.bottom - r.top > 1
              }),
            )
            if (hit) {
              problems.push({
                where,
                kind: 'overlap',
                text: `"${describe(a.text)}" × "${describe(b.text)}"`,
              })
            }
          }
        }
      }
      return problems
    },
    { extra: options.extra ?? [], noScroll: options.noScroll ?? false },
  ) as Promise<LayoutProblem[]>
}
