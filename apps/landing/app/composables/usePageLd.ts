// Datos estructurados de las páginas interiores: WebPage + BreadcrumbList (Inicio › Página) y,
// si se pasan, FAQPage con las preguntas visibles de la página.
import { localizedPaths, site, type PageKey } from '~/config/site'

export function usePageLd(
  page: PageKey,
  nameKey: string,
  descriptionKey: string,
  faq?: { q: string; a: string }[],
) {
  const { t, locale } = useI18n()
  const base = useRuntimeConfig().public.siteUrl.replace(/\/+$/, '')
  const loc = locale.value === 'en' ? 'en' : 'es'
  const url = base + localizedPaths[page][loc]
  const homeUrl = base + localizedPaths.home[loc]

  const graph: Record<string, unknown>[] = [
    {
      '@type': 'WebPage',
      '@id': `${url}#webpage`,
      url,
      name: t(nameKey),
      description: t(descriptionKey),
      inLanguage: loc === 'en' ? 'en' : 'es-GT',
      isPartOf: { '@type': 'WebSite', name: site.product, url: base },
      about: { '@type': 'SoftwareApplication', name: site.product },
    },
    {
      '@type': 'BreadcrumbList',
      itemListElement: [
        { '@type': 'ListItem', position: 1, name: site.product, item: homeUrl },
        { '@type': 'ListItem', position: 2, name: t(nameKey), item: url },
      ],
    },
  ]
  if (faq?.length) {
    graph.push({
      '@type': 'FAQPage',
      mainEntity: faq.map((f) => ({
        '@type': 'Question',
        name: f.q,
        acceptedAnswer: { '@type': 'Answer', text: f.a },
      })),
    })
  }

  useHead({
    script: [
      {
        type: 'application/ld+json',
        innerHTML: JSON.stringify({ '@context': 'https://schema.org', '@graph': graph }).replaceAll(
          '<',
          '\\u003c',
        ),
      },
    ],
  })
}
