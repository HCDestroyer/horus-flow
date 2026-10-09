import { beforeEach, describe, expect, it } from 'vitest'
import type { TopCustomerRow } from '~/widgets/shapes'
import { ALL_TENANTS, TENANTS } from '~~/mocks/data'
import { findingsOf, securitySummary } from '~~/mocks/findings'
import {
  customersOf,
  overlapping,
  prefixImportPreview,
  prefixesOf,
  routersOf,
  sitesOf,
} from '~~/mocks/inventory'
import { contains, overlaps, parseCidr } from '~~/mocks/net'
import { TEMPLATES } from '~~/mocks/server'
import { resetMockState } from '~~/mocks/state'
import { widgetData } from '~~/mocks/widget-data'

/**
 * API simulada de I1: una sola base para widgets, clientes, hallazgos e inventario
 * (README › Decisiones). Lo que dice un widget lo confirma la lista correspondiente.
 */

const NOW = new Date('2026-10-08T15:00:00Z')
const noc = TEMPLATES[0]!
const widget = (id: string) => noc.widgets.find((w) => w.id === id)!

beforeEach(() => resetMockState())

function data(tenant: (typeof ALL_TENANTS)[number], id: string) {
  return widgetData(
    { tenant, widget: widget(id), now: NOW, canSeePersonalData: true, empty: false },
    { degraded: false },
  )!.data
}

describe('base común de la API simulada (I1)', () => {
  for (const tenant of ALL_TENANTS) {
    it(`customers_active cuenta los mismos clientes que la lista (${tenant.tenant_slug})`, () => {
      const d = data(tenant, 'w-customers')
      const values = (d.kind === 'state' ? d.values : {}) as { total: number; active: number }
      const list = customersOf(tenant, NOW)
      expect(values.total).toBe(list.length)
      expect(values.active).toBe(list.filter((c) => c.status === 'active').length)
    })

    it(`top_customers son clientes de la lista, con su IP y su volumen (${tenant.tenant_slug})`, () => {
      const d = data(tenant, 'w-top-customers')
      const rows = (d.kind === 'table' ? d.rows : []) as unknown as TopCustomerRow[]
      const list = customersOf(tenant, NOW)
      expect(rows.length).toBeGreaterThan(0)
      for (const row of rows) {
        const c = list.find((x) => x.address === row.customer_ip)
        expect(c, row.customer_ip).toBeDefined()
        expect(Number(c!.traffic_24h!.down_bytes)).toBe(row.down_bytes)
      }
    })

    it(`el resumen de seguridad y la lista de hallazgos activos coinciden (${tenant.tenant_slug})`, () => {
      const s = securitySummary(tenant, NOW)
      const active = findingsOf(tenant, NOW, true).filter(
        (f) => f.state === 'open' || f.state === 'acknowledged',
      )
      const total = Object.values(s.open_by_severity).reduce((a, b) => a + b, 0)
      expect(total).toBe(active.length)
      // Cada cliente con hallazgos activos tiene estado de seguridad y cuenta coherente.
      const customers = customersOf(tenant, NOW)
      for (const f of active) {
        const c = customers.find((x) => x.id === f.customer_id)!
        expect(c.open_findings).toBeGreaterThan(0)
        expect(c.security_state).not.toBe('clean')
      }
      // D18: "Infectado" siempre sostenido por un hallazgo de C2 con su confianza.
      for (const c of customers.filter((x) => x.security_state === 'infected')) {
        expect(
          active.some((f) => f.customer_id === c.id && f.kind === 'botnet_c2_communication'),
        ).toBe(true)
      }
    })

    it(`todo hallazgo trae ≥ 2 razones y acciones con deshacer (D11) (${tenant.tenant_slug})`, () => {
      for (const f of findingsOf(tenant, NOW, true)) {
        expect(f.reasons.length, f.id).toBeGreaterThanOrEqual(2)
        expect(f.recommended_actions.length).toBeGreaterThan(0)
        for (const a of f.recommended_actions) {
          expect(a.execution).toBe('manual')
          if (a.routeros) {
            expect(a.routeros.undo_commands.length).toBeGreaterThan(0)
            expect(a.routeros.rendered_undo_commands?.length).toBe(a.routeros.undo_commands.length)
          }
        }
      }
    })

    it(`exporters_status refleja los routers del inventario (${tenant.tenant_slug})`, () => {
      const d = data(tenant, 'w-exporters')
      const rows = d.kind === 'table' ? d.rows : []
      expect(rows.map((r) => r.router)).toEqual(routersOf(tenant, NOW).map((r) => r.router.name))
    })
  }

  it('IPv6 (D22): cada prefijo delegado es un cliente independiente', () => {
    const v6 = customersOf(TENANTS.fibraNorte, NOW).filter((c) => c.address.includes(':'))
    expect(v6.length).toBeGreaterThan(0)
    const pd = prefixesOf(TENANTS.fibraNorte, sitesOf(TENANTS.fibraNorte)[0]!).find((p) =>
      p.prefix.includes(':'),
    )!
    expect(pd.ipv6_client_len).toBe(56)
    for (const c of v6) expect(c.address.endsWith('00::')).toBe(true)
  })
})

describe('prefijos e importación (I1-19, E-IPv6-1)', () => {
  it('detecta solapes en IPv4 e IPv6', () => {
    expect(overlaps('10.20.0.0/20', '10.20.4.0/24')).toBe(true)
    expect(overlaps('10.20.0.0/20', '10.21.0.0/24')).toBe(false)
    expect(overlaps('2001:db8:4a00::/40', '2001:db8:4af0::/44')).toBe(true)
    expect(contains(parseCidr('2001:db8::/32')!, parseCidr('2001:db8:1::/48')!)).toBe(true)
    expect(parseCidr('10.0.0.300/24')).toBeNull()
  })

  it('la vista previa marca nuevos, existentes y solapes y propone el tamaño IPv6', () => {
    const tenant = TENANTS.fibraNorte
    const item = routersOf(tenant, NOW)[0]!
    const preview = prefixImportPreview(tenant, item, NOW)
    const by = (p: string) => preview.items.find((i) => i.prefix === p)!
    expect(by('10.20.0.0/20').diff).toBe('exists')
    expect(by('10.20.0.0/16').diff).toBe('overlaps')
    expect(by('10.20.32.0/24').diff).toBe('new')
    const shared = preview.items.find((i) => i.ipv6_pool_usage === 'ppp_link_shared')!
    expect(shared.suggested_role).toBe('infrastructure')
    expect(overlapping(tenant, item.site, '10.20.4.0/24')?.prefix).toBe('10.20.0.0/20')
  })
})
