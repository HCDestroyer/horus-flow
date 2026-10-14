import { describe, expect, it, vi } from 'vitest'
import { validateManifest } from '~/widgets/validate'
import {
  fillGaps,
  gridModeFor,
  placeWidgets,
  readingOrder,
  totalRows,
} from '~/utils/dashboard-layout'
import { agoParts, dataTime, freshnessLevel } from '~/utils/freshness'
import { formatBps, formatBytes, formatChange, formatPercent } from '~/utils/format'
import { createRealtimeRegistry, type RealtimeHandler } from '~/utils/realtime'
import { TEMPLATES } from '~~/mocks/server'
import { WIDGET_CATALOG } from '~~/types/api/widget-catalog'

/** Pruebas del marco de widgets (I0-16): `pnpm test widgets`. */

const manifests = import.meta.glob<{ default: unknown }>('../../app/widgets/[a-z]*/manifest.ts', {
  eager: true,
})

describe('manifiestos de presentación (C9)', () => {
  it('todos los manifiestos registrados validan contra el esquema y el catálogo', () => {
    const entries = Object.entries(manifests)
    expect(entries.length).toBeGreaterThanOrEqual(12)
    for (const [path, mod] of entries) {
      const folder = path.split('/').at(-2)!
      expect(validateManifest(mod.default, folder), folder).toEqual([])
    }
  })

  it('cada tipo usado por las plantillas del sistema tiene renderizador', () => {
    const folders = Object.keys(manifests).map((p) => p.split('/').at(-2))
    const used = new Set(TEMPLATES.flatMap((d) => d.widgets.map((w) => w.type)))
    for (const type of used) expect(folders, type).toContain(type)
  })

  const valid = {
    type: 'watched_ports',
    sizes: {
      normal: { default: { w: 4, h: 3 }, min: { w: 4, h: 3 } },
      wall: { default: { w: 4, h: 3 }, min: { w: 4, h: 3 } },
    },
    description: 'widgets.watched_ports.description',
    category: 'security',
    placeholder: 'table',
    wall: { hide: [], enlarge: [] },
  }

  it('rechaza un tipo que no existe en widget-types.json', () => {
    const errors = validateManifest(
      { ...valid, type: 'no_existe', description: 'widgets.no_existe.description' },
      'no_existe',
    )
    expect(errors.join()).toMatch(/no existe en widget-types.json/)
  })

  it('rechaza tamaños fuera de sizes.allowed del catálogo', () => {
    const errors = validateManifest(
      {
        ...valid,
        sizes: { ...valid.sizes, normal: { default: { w: 12, h: 4 }, min: { w: 4, h: 3 } } },
      },
      'watched_ports',
    )
    expect(errors.join()).toMatch(/sizes.normal.default 12×4/)
  })

  it('rechaza campos desconocidos (additionalProperties: false) y carpeta distinta del tipo', () => {
    expect(validateManifest({ ...valid, color: 'red' }, 'watched_ports').length).toBeGreaterThan(0)
    expect(validateManifest(valid, 'otra_carpeta').join()).toMatch(/no coincide con la carpeta/)
  })

  it('el catálogo generado coincide con el contrato', () => {
    expect(WIDGET_CATALOG.map((t) => t.type)).toContain('noc_header')
  })
})

describe('grilla de 12 columnas (frontend.md §5.2)', () => {
  const noc = TEMPLATES[0]!

  it('modo según el ancho disponible', () => {
    expect(gridModeFor(1400)).toBe('designed')
    // 1280×720 con la barra lateral abierta (~980 px de contenedor): se respeta el diseño.
    expect(gridModeFor(980)).toBe('designed')
    expect(gridModeFor(700)).toBe('narrow')
    expect(gridModeFor(400)).toBe('single')
  })

  it('en el layout diseñado cada widget ocupa su posición', () => {
    const placed = placeWidgets(noc.widgets, 'designed')
    const top = placed.find((p) => p.widget.id === 'w-top-categories')!
    expect(top.style).toEqual({ gridColumn: '1 / span 4', gridRow: '7 / span 4' })
  })

  it('en anchos estrechos los widgets pasan a 6 o 12 columnas', () => {
    const narrow = placeWidgets(noc.widgets, 'narrow')
    expect(narrow.find((p) => p.widget.id === 'w-customers')!.style.gridColumn).toBe('span 6')
    expect(narrow.find((p) => p.widget.id === 'w-traffic-24h')!.style.gridColumn).toBe('span 12')
  })

  it('en una columna se respeta el orden de lectura (arriba→abajo, izquierda→derecha)', () => {
    const order = readingOrder(noc.widgets).map((w) => w.id)
    expect(order.slice(0, 4)).toEqual([
      'w-header',
      'w-traffic-now',
      'w-customers',
      'w-findings-summary',
    ])
    expect(placeWidgets(noc.widgets, 'single').every((p) => p.style.gridColumn === '1 / -1')).toBe(
      true,
    )
  })

  it('las plantillas de Horus no tienen huecos ni solapes (el relleno no cambia nada)', () => {
    for (const template of TEMPLATES) {
      const rows = template.widgets.reduce((m, w) => Math.max(m, w.position.y + w.position.h), 0)
      const cells = new Set<string>()
      for (const w of template.widgets) {
        for (let r = w.position.y; r < w.position.y + w.position.h; r++)
          for (let c = w.position.x; c < w.position.x + w.position.w; c++) {
            expect(cells.has(`${r}:${c}`), `${template.name} ${w.id} ${r}:${c}`).toBe(false)
            cells.add(`${r}:${c}`)
          }
      }
      expect(cells.size, template.name).toBe(rows * 12)
      expect(fillGaps(template.widgets).map((w) => w.position)).toEqual(
        readingOrder(template.widgets).map((w) => w.position),
      )
    }
  })

  it('el layout diseñado cierra los huecos de un dashboard sin mover ni solapar widgets', () => {
    // Un dashboard con huecos (el NOC de antes: columna 12 libre en la fila 2 y una fila
    // vacía bajo "Tráfico 24 h").
    const p = (id: string, x: number, y: number, w: number, h: number) => ({
      id,
      position: { x, y, w, h },
    })
    const gappy = [
      p('header', 0, 0, 12, 1),
      p('kpi-a', 0, 1, 4, 2),
      p('kpi-b', 4, 1, 3, 2),
      p('kpi-c', 7, 1, 4, 2),
      p('chart', 0, 3, 8, 3),
      p('side', 8, 3, 4, 4),
      p('bottom', 0, 7, 12, 2),
    ]
    const filled = fillGaps(gappy)
    const cells = new Map<string, string>()
    for (const w of filled) {
      const original = gappy.find((o) => o.id === w.id)!.position
      // Solo crece: contiene su posición original.
      expect(w.position.x).toBeLessThanOrEqual(original.x)
      expect(w.position.y).toBe(original.y)
      expect(w.position.x + w.position.w).toBeGreaterThanOrEqual(original.x + original.w)
      expect(w.position.h).toBeGreaterThanOrEqual(original.h)
      for (let r = w.position.y; r < w.position.y + w.position.h; r++) {
        for (let c = w.position.x; c < w.position.x + w.position.w; c++) {
          expect(cells.get(`${r}:${c}`), `${w.id} ${r}:${c}`).toBeUndefined()
          cells.set(`${r}:${c}`, w.id)
        }
      }
    }
    // Sin celdas vacías: el mural no deja huecos.
    expect(cells.size).toBe(totalRows(gappy) * 12)
    expect(filled.find((w) => w.id === 'kpi-c')!.position).toMatchObject({ x: 7, w: 5 })
    expect(filled.find((w) => w.id === 'chart')!.position).toMatchObject({ y: 3, h: 4 })
  })

  it('filas totales del layout (para repartir la altura en mural)', () => {
    expect(totalRows(noc.widgets)).toBe(10)
  })
})

describe('frescura (frontend.md §7.6, §10.2)', () => {
  it('fresco ≤ 2·T, atrasado 2–5·T, obsoleto > 5·T', () => {
    expect(freshnessLevel(20, 15)).toBe('fresh')
    expect(freshnessLevel(31, 15)).toBe('late')
    expect(freshnessLevel(76, 15)).toBe('stale')
  })

  it('"hace N s / min / h"', () => {
    expect(agoParts(3)).toEqual({ key: 'time.agoSeconds', n: 3 })
    expect(agoParts(125)).toEqual({ key: 'time.agoMinutes', n: 2 })
    expect(agoParts(7300)).toEqual({ key: 'time.agoHours', n: 2 })
  })

  it('el instante del dato descuenta su edad declarada', () => {
    expect(dataTime({ generated_at: '2026-10-08T15:00:10.000Z', freshness_seconds: 4 })).toBe(
      new Date('2026-10-08T15:00:06.000Z').getTime(),
    )
  })
})

describe('formato de cifras (frontend.md §13.4)', () => {
  it('bit/s y bytes en SI decimal, unidad aparte', () => {
    expect(formatBps(4_213_456_789)).toEqual({ value: '4,21', unit: 'Gbit/s' })
    expect(formatBps(null)).toEqual({ value: '—', unit: '' })
    expect(formatBytes(38_200_000_000_000)).toEqual({ value: '38,2', unit: 'TB' })
    expect(formatPercent(0.031, 1)).toBe('3,1 %')
  })

  it('variación sin "−0 %"', () => {
    expect(formatChange(112, 100)).toBe('+12 %')
    expect(formatChange(99.8, 100)).toBe('±0 %')
    expect(formatChange(90, 100)).toBe('−10 %')
    expect(formatChange(5, 0)).toBeNull()
  })
})

describe('tiempo real por ISP (frontend.md §3.2, §10.5)', () => {
  it('al cambiar de ISP se cierran sus suscripciones y no llegan más mensajes', () => {
    const handlers = new Map<string, RealtimeHandler>()
    const close = vi.fn()
    const registry = createRealtimeRegistry({
      subscribe(tenantId, topic, handler) {
        handlers.set(`${tenantId}:${topic}`, handler)
        return close
      },
    })
    const received = vi.fn()
    registry.subscribe('isp-a', 'traffic.summary', received)
    expect(registry.size).toBe(1)
    registry.closeTenant()
    expect(close).toHaveBeenCalledOnce()
    expect(registry.size).toBe(0)
    // Un mensaje tardío del ISP anterior se descarta.
    handlers.get('isp-a:traffic.summary')!({
      type: 'state',
      topic: 'traffic.summary',
      key: 'tenant',
      time: '',
      data: {},
    })
    expect(received).not.toHaveBeenCalled()
  })
})
