import { planPrice, pricing, type Currency, type Localized, type Period, type Plan } from '~/config/pricing'

/** Estado compartido del selector de precios (periodo y moneda) y utilidades de formato. */
export function usePricing() {
  const { locale } = useI18n()
  const period = useState<Period>('pricing-period', () => pricing.defaultPeriod)
  const currency = useState<Currency>('pricing-currency', () => pricing.defaultCurrency)

  const loc = computed<'es' | 'en'>(() => (locale.value === 'en' ? 'en' : 'es'))
  const l = (text: Localized) => text[loc.value]

  function money(amount: number, cur: Currency = currency.value) {
    return new Intl.NumberFormat(loc.value === 'es' ? 'es-GT' : 'en-US', {
      style: 'currency',
      currency: cur,
      maximumFractionDigits: 0,
    }).format(amount)
  }

  function price(plan: Plan) {
    return planPrice(plan, currency.value, period.value)
  }

  return { pricing, period, currency, l, money, price, loc }
}
