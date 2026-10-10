// Datos estructurados JSON-LD (schema.org): Organization, SoftwareApplication y Product con
// Offers. Precios y contacto salen de la base de datos (useSite); con "Mostrar importes"
// desactivado, las ofertas no llevan importe.
import { catalogPrice } from '#shared/catalog'
import { site } from '~/config/site'

export function useStructuredData() {
  const { t, locale } = useI18n()
  const base = useRuntimeConfig().public.siteUrl.replace(/\/+$/, '')
  const loc = locale.value === 'en' ? 'en' : 'es'
  const pageUrl = base + (loc === 'en' ? '/en' : '/')
  const buyUrl = base + (loc === 'en' ? '/en/buy' : '/comprar')
  const { catalog: pricing, settings } = useSite().value
  const email = settings.contact.email || site.seller.email

  const organization = {
    '@type': 'Organization',
    '@id': `${base}/#organization`,
    name: site.seller.legalName,
    alternateName: site.seller.shortName,
    email,
    url: base,
    address: {
      '@type': 'PostalAddress',
      streetAddress: site.seller.address.street,
      addressLocality: site.seller.address.locality,
      addressRegion: site.seller.address.region,
      addressCountry: site.seller.country,
    },
    contactPoint: {
      '@type': 'ContactPoint',
      contactType: 'customer support',
      email,
      ...(settings.contact.phone ? { telephone: settings.contact.phone } : {}),
      description: settings.support.text[loc],
      availableLanguage: ['es', 'en'],
      // Soporte 24/7 en todos los planes.
      hoursAvailable: {
        '@type': 'OpeningHoursSpecification',
        dayOfWeek: ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'],
        opens: '00:00',
        closes: '23:59',
      },
    },
  }

  const offers = pricing.plans
    .filter((p) => p.prices && p.visible)
    .flatMap((plan) =>
      pricing.currencies.map((currency) => {
        const amount = catalogPrice(pricing, plan, currency, 'annual')
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
