// Datos estructurados JSON-LD (schema.org): Organization, SoftwareApplication y Product con
// Offers. Mientras los precios no estén confirmados, las ofertas no llevan importe.
import { planPrice, pricing } from '~/config/pricing'
import { site } from '~/config/site'

export function useStructuredData() {
  const { t, locale } = useI18n()
  const base = useRuntimeConfig().public.siteUrl.replace(/\/+$/, '')
  const loc = locale.value === 'en' ? 'en' : 'es'
  const pageUrl = base + (loc === 'en' ? '/en' : '/')
  const buyUrl = base + (loc === 'en' ? '/en/buy' : '/comprar')

  const organization = {
    '@type': 'Organization',
    '@id': `${base}/#organization`,
    name: site.seller.legalName,
    alternateName: site.seller.shortName,
    email: site.seller.email,
    url: base,
    address: { '@type': 'PostalAddress', addressCountry: site.seller.country },
  }

  const offers = pricing.plans
    .filter((p) => p.prices)
    .flatMap((plan) =>
      pricing.currencies.map((currency) => {
        const amount = planPrice(plan, currency, 'annual')
        return {
          '@type': 'Offer',
          name: `${site.product} ${plan.name[loc]}`,
          url: `${buyUrl}?plan=${plan.id}`,
          priceCurrency: currency,
          ...(amount !== null
            ? {
                price: amount,
                priceSpecification: {
                  '@type': 'UnitPriceSpecification',
                  price: amount,
                  priceCurrency: currency,
                  unitCode: 'ANN',
                },
              }
            : { description: t('pricing.launch') }),
          availability: 'https://schema.org/InStock',
          seller: { '@id': `${base}/#organization` },
        }
      }),
    )

  const graph = {
    '@context': 'https://schema.org',
    '@graph': [
      organization,
      {
        '@type': 'SoftwareApplication',
        '@id': `${base}/#software`,
        name: site.product,
        applicationCategory: 'SecurityApplication',
        applicationSubCategory: 'Network monitoring',
        operatingSystem: 'Debian 12, Debian 13',
        description: t('meta.homeDescription'),
        url: pageUrl,
        image: base + site.ogImage,
        inLanguage: ['es', 'en'],
        publisher: { '@id': `${base}/#organization` },
        offers,
      },
      {
        '@type': 'Product',
        '@id': `${base}/#product`,
        name: site.product,
        description: t('meta.homeDescription'),
        image: base + site.ogImage,
        brand: { '@type': 'Brand', name: site.product },
        manufacturer: { '@id': `${base}/#organization` },
        offers,
      },
    ],
  }

  useHead({
    script: [
      {
        type: 'application/ld+json',
        // JSON.stringify no escapa "<": se evita cerrar el <script> por accidente.
        innerHTML: JSON.stringify(graph).replaceAll('<', '\\u003c'),
      },
    ],
  })
}
