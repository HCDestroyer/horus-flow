import { describe, expect, it } from 'vitest'
import { planPrice, pricing } from '../../app/config/pricing'
import es from '../../i18n/locales/es.json'
import en from '../../i18n/locales/en.json'

describe('app/config/pricing.ts', () => {
  it('mientras confirmed sea false no hay ninguna cifra publicable', () => {
    expect(pricing.confirmed).toBe(false)
    for (const plan of pricing.plans) {
      expect(planPrice(plan, 'USD', 'annual')).toBeNull()
      expect(planPrice(plan, 'GTQ', 'monthly')).toBeNull()
    }
  })

  it('con confirmed true devuelve el importe del periodo y la moneda', () => {
    const confirmed = { ...pricing, confirmed: true }
    const medium = pricing.plans.find((p) => p.id === 'medium')!
    expect(planPrice(medium, 'USD', 'annual', confirmed)).toBe(medium.prices!.USD.annual)
    expect(planPrice(medium, 'GTQ', 'monthly', confirmed)).toBe(medium.prices!.GTQ.monthly)
    const enterprise = pricing.plans.find((p) => p.id === 'enterprise')!
    expect(planPrice(enterprise, 'USD', 'annual', confirmed)).toBeNull()
  })

  it('los planes siguen los tamaños del instalador y tienen precio en todas las monedas', () => {
    expect(pricing.plans.map((p) => [p.id, p.installerSize, p.clients])).toEqual([
      ['small', 'small', 300],
      ['medium', 'medium', 2000],
      ['large', 'large', 10000],
      ['enterprise', null, null],
    ])
    for (const plan of pricing.plans.filter((p) => p.prices)) {
      for (const c of pricing.currencies) {
        expect(plan.prices![c].monthly).toBeGreaterThan(0)
        expect(plan.prices![c].annual).toBeGreaterThan(0)
      }
    }
    expect(pricing.plans.filter((p) => p.highlighted)).toHaveLength(1)
  })

  it('todo texto existe en los dos idiomas', () => {
    for (const plan of pricing.plans) {
      for (const text of [plan.name, plan.summary, ...plan.includes]) {
        expect(text.es.length).toBeGreaterThan(0)
        expect(text.en.length).toBeGreaterThan(0)
      }
    }
  })
})

describe('traducciones', () => {
  function keys(obj: Record<string, unknown>, prefix = ''): string[] {
    return Object.entries(obj).flatMap(([k, v]) =>
      v && typeof v === 'object'
        ? keys(v as Record<string, unknown>, `${prefix}${k}.`)
        : [`${prefix}${k}`],
    )
  }
  it('es.json y en.json tienen las mismas claves', () => {
    expect(keys(en).sort()).toEqual(keys(es).sort())
  })
})
