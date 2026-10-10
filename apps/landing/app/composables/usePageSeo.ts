// SEO por página: <title>, descripción, canonical, hreflang, Open Graph y Twitter.
// La URL base sale de NUXT_PUBLIC_SITE_URL (runtimeConfig.public.siteUrl) en tiempo de ejecución.
import { localizedPaths, site, type PageKey } from '~/config/site'

export function usePageSeo(page: PageKey, titleKey: string, descriptionKey?: string) {
  const { t, locale } = useI18n()
  const base = useRuntimeConfig().public.siteUrl.replace(/\/+$/, '')
  const loc = computed(() => (locale.value === 'en' ? 'en' : 'es'))
  const paths = localizedPaths[page]
  const url = computed(() => base + paths[loc.value])
  const image = base + site.ogImage

  useHead(() => ({
    htmlAttrs: { lang: loc.value === 'es' ? 'es-GT' : 'en' },
    link: [
      { rel: 'canonical', href: url.value },
      { rel: 'alternate', hreflang: 'es', href: base + paths.es },
      { rel: 'alternate', hreflang: 'en', href: base + paths.en },
      { rel: 'alternate', hreflang: 'x-default', href: base + paths.es },
    ],
  }))

  useSeoMeta({
    title: () => t(titleKey),
    description: () => (descriptionKey ? t(descriptionKey) : t('meta.homeDescription')),
    ogTitle: () => t(titleKey),
    ogDescription: () => (descriptionKey ? t(descriptionKey) : t('meta.homeDescription')),
    ogType: 'website',
    ogSiteName: site.product,
    ogUrl: () => url.value,
    ogLocale: () => (loc.value === 'es' ? 'es_GT' : 'en_US'),
    ogLocaleAlternate: () => (loc.value === 'es' ? ['en_US'] : ['es_GT']),
    ogImage: image,
    ogImageWidth: site.ogImageWidth,
    ogImageHeight: site.ogImageHeight,
    ogImageAlt: () => t('meta.ogAlt'),
    twitterCard: 'summary_large_image',
    twitterTitle: () => t(titleKey),
    twitterDescription: () => (descriptionKey ? t(descriptionKey) : t('meta.homeDescription')),
    twitterImage: image,
    twitterImageAlt: () => t('meta.ogAlt'),
  })

  return { url, base }
}
