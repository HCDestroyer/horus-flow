// SEO por página: <title>, descripción, canonical, hreflang, Open Graph y Twitter.
// La URL base sale de NUXT_PUBLIC_SITE_URL (runtimeConfig.public.siteUrl) en tiempo de ejecución.
import { localizedPaths, site, type PageKey } from '~/config/site'

/** Texto de SEO: clave de i18n o, para páginas de la base de datos, una función con el texto. */
type SeoText = string | (() => string)

/**
 * `page`: clave de app/config/site.ts o, para las páginas editables (legales e informativas),
 * una función con las rutas de cada idioma ({ es: '/legal/privacidad', en: '/en/legal/privacy' }).
 */
export function usePageSeo(
  page: PageKey | (() => { es: string; en: string }),
  titleKey: SeoText,
  descriptionKey?: SeoText,
) {
  const { t: translate, locale } = useI18n()
  const t = (key: SeoText) => (typeof key === 'function' ? key() : translate(key))
  const base = useRuntimeConfig().public.siteUrl.replace(/\/+$/, '')
  const loc = computed(() => (locale.value === 'en' ? 'en' : 'es'))
  const pathsRef = computed(() => (typeof page === 'string' ? localizedPaths[page] : page()))
  const url = computed(() => base + pathsRef.value[loc.value])
  const image = base + site.ogImage

  useHead(() => {
    const paths = pathsRef.value
    return {
      htmlAttrs: { lang: loc.value === 'es' ? 'es-GT' : 'en' },
      link: [
        { rel: 'canonical', href: url.value },
        { rel: 'alternate', hreflang: 'es', href: base + paths.es },
        { rel: 'alternate', hreflang: 'en', href: base + paths.en },
        { rel: 'alternate', hreflang: 'x-default', href: base + paths.es },
      ],
    }
  })

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
