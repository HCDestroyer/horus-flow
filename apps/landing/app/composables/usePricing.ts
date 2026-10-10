import {
  catalogPrice,
  type Catalog,
  type CatalogPlan,
  type Currency,
  type Localized,
  type Period,
} from '#shared/catalog'

/**
 * Estado compartido del selector de precios (periodo y moneda) y utilidades de formato. El
 * catálogo viene de la base de datos (useSite); el panel pasa un borrador para la vista previa.
 */
export function usePricing(override?: Ref<Catalog | null | undefined>) {
  const { locale } = useI18n()
  const site = useSiteState()
  const pricing = computed<Catalog>(() => override?.value ?? site.value!.catalog)
  const period = useState<Period>('pricing-period', () => pricing.value.defaultPeriod)
  const currency = useState<Currency>('pricing-currency', () => pricing.value.defaultCurrency)

  const loc = computed<'es' | 'en'>(() => (locale.value === 'en' ? 'en' : 'es'))
  const l = (text: Localized) => text[loc.value]

  function money(amount: number, cur: Currency = currency.value) {
    return new Intl.NumberFormat(loc.value === 'es' ? 'es-GT' : 'en-US', {
      style: 'currency',
      currency: cur,
      minimumFractionDigits: Number.isInteger(amount) ? 0 : 2,
      maximumFractionDigits: 2,
    }).format(amount)
  }

  function price(plan: CatalogPlan) {
    return catalogPrice(pricing.value, plan, currency.value, period.value)
  }

  /** Planes que se muestran (visibles), en orden. */
  const plans = computed(() => pricing.value.plans.filter((p) => p.visible))

  return { pricing, plans, period, currency, l, money, price, loc }
}
