import { describe, expect, it } from 'vitest'
import type {
  BotnetSignalsValues,
  FindingFeedRow,
  FindingsSummaryValues,
  SecurityByNodeRow,
} from '~/widgets/shapes'
import type { DashboardWidget, WidgetData } from '~~/types/api'
import { ALL_TENANTS, type MockTenant } from '~~/mocks/data'
import { TEMPLATES } from '~~/mocks/server'
import { openFindings, TREND_KINDS, widgetData } from '~~/mocks/widget-data'

/**
 * Coherencia de la API simulada entre widgets de seguridad (I0-16): el resumen, el feed, la
 * tendencia, la seguridad por nodo y las señales de botnet salen de la misma base de
 * hallazgos abiertos y nunca se contradicen (p. ej. "Crítica 0" con una crítica en el feed).
 */

const [noc, security] = TEMPLATES as [(typeof TEMPLATES)[0], (typeof TEMPLATES)[1]]
const NOWS = ['2026-10-08T15:00:00Z', '2026-10-09T00:29:00Z', '2026-12-31T23:59:30Z'].map(
  (d) => new Date(d),
)

function data(tenant: MockTenant, widget: DashboardWidget, now: Date): WidgetData['data'] {
  const out = widgetData(
    { tenant, widget, now, canSeePersonalData: true, empty: false },
    { degraded: false },
  )
  if (!out) throw new Error(`sin datos para ${widget.id}`)
  return out.data
}
const pick = (template: typeof noc, id: string) => template.widgets.find((w) => w.id === id)!
const summary = (t: MockTenant, w: DashboardWidget, now: Date) => {
  const d = data(t, w, now)
  return (d.kind === 'state' ? d.values : {}) as unknown as FindingsSummaryValues
}
const rows = <T>(t: MockTenant, w: DashboardWidget, now: Date) => {
  const d = data(t, w, now)
  return (d.kind === 'table' ? d.rows : []) as unknown as T[]
}

describe('hallazgos simulados coherentes entre widgets', () => {
  for (const tenant of ALL_TENANTS) {
    for (const now of NOWS) {
      const label = `${tenant.tenant_slug} @ ${now.toISOString()}`

      it(`resumen y feed cuentan las mismas severidades (${label})`, () => {
        for (const template of [noc, security]) {
          const s = summary(tenant, pick(template, 'w-findings-summary'), now)
          const feed = rows<FindingFeedRow>(tenant, pick(template, 'w-findings-feed'), now)
          const bySeverity = s.open_by_severity as Record<string, number>
          expect(Object.values(bySeverity).reduce((a, b) => a + b, 0)).toBe(s.open_total)
          for (const row of feed) {
            // Toda severidad del feed está en el resumen con al menos tantos hallazgos.
            const inFeed = feed.filter((r) => r.severity === row.severity).length
            if (bySeverity[row.severity] !== undefined) {
              expect(
                bySeverity[row.severity],
                `${template.name} ${row.severity}`,
              ).toBeGreaterThanOrEqual(inFeed)
            }
          }
          expect(feed.length).toBeLessThanOrEqual(s.open_total)
        }
        // La plantilla NOC filtra desde "media": la crítica del feed también cuenta allí.
        const nocSummary = summary(tenant, pick(noc, 'w-findings-summary'), now)
        const nocFeed = rows<FindingFeedRow>(tenant, pick(noc, 'w-findings-feed'), now)
        expect(nocFeed.some((r) => r.severity === 'critical')).toBe(true)
        expect(nocSummary.open_by_severity.critical).toBeGreaterThan(0)
      })

      it(`clientes por estado: Infectado en el resumen ⇔ Infectado en el feed (${label})`, () => {
        const s = summary(tenant, pick(security, 'w-findings-summary'), now)
        const feed = rows<FindingFeedRow>(tenant, pick(security, 'w-findings-feed'), now)
        const states = s.by_security_state as Record<string, number>
        expect((states.infected ?? 0) + (states.suspected ?? 0)).toBe(s.affected_customers)
        const feedInfected = new Set(
          feed.filter((r) => r.security_state === 'infected').map((r) => r.customer_ip),
        )
        expect(states.infected ?? 0).toBeGreaterThanOrEqual(feedInfected.size)
        if (feedInfected.size) expect(states.infected).toBeGreaterThan(0)
        // Un mismo cliente tiene el mismo estado en todas sus filas.
        for (const row of feed) {
          const same = feed.filter((r) => r.customer_ip === row.customer_ip)
          expect(new Set(same.map((r) => r.security_state)).size).toBe(1)
        }
      })

      it(`seguridad por nodo suma el total de hallazgos y clientes (${label})`, () => {
        const s = summary(tenant, pick(security, 'w-findings-summary'), now) // min_severity low
        const nodes = rows<SecurityByNodeRow>(tenant, pick(security, 'w-security-by-node'), now)
        expect(nodes.reduce((a, r) => a + r.open_findings, 0)).toBe(s.open_total)
        expect(nodes.reduce((a, r) => a + r.customers_with_signals, 0)).toBe(s.affected_customers)
        const feed = rows<FindingFeedRow>(tenant, pick(security, 'w-findings-feed'), now)
        for (const row of feed) expect(nodes.map((n) => n.site)).toContain(row.site)
      })

      it(`tendencia: hallazgos abiertos por día de la misma base (${label})`, () => {
        const d = data(tenant, pick(security, 'w-findings-trend'), now)
        if (d.kind !== 'series') throw new Error('serie esperada')
        const base = openFindings(tenant, now)
        const from = new Date(d.series[0]!.points[0]![0]).getTime()
        for (const series of d.series) {
          const total = series.points.reduce((a, [, v]) => a + (v ?? 0), 0)
          const expected = base.filter(
            (f) => f.kind === series.group && new Date(f.opened_at).getTime() >= from,
          ).length
          expect(total, String(series.group)).toBe(expected)
        }
        expect(d.series.map((s) => s.group)).toEqual(TREND_KINDS)
        const s = summary(tenant, pick(security, 'w-findings-summary'), now)
        const trendTotal = d.series.reduce(
          (a, ser) => a + ser.points.reduce((b, [, v]) => b + (v ?? 0), 0),
          0,
        )
        expect(trendTotal).toBeLessThanOrEqual(s.open_total)
      })

      it(`señales de botnet: clientes de la misma base (${label})`, () => {
        const d = data(tenant, pick(security, 'w-botnet-signals'), now)
        const v = (d.kind === 'state' ? d.values : {}) as unknown as BotnetSignalsValues
        const s = summary(tenant, pick(security, 'w-findings-summary'), now)
        expect(v.affected_customers).toBeGreaterThan(0)
        expect(v.affected_customers).toBeLessThanOrEqual(s.affected_customers)
        for (const n of Object.values(v.by_signal)) {
          expect(n).toBeLessThanOrEqual(v.affected_customers)
        }
        // Infectado (C2 confirmado) ⇔ hay contacto con C2 entre las señales.
        expect((v.by_signal.c2_contact ?? 0) > 0).toBe((s.by_security_state.infected ?? 0) > 0)
      })
    }
  }
})
