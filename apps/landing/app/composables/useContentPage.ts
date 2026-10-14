// Página legal o informativa de la base de datos (GET /api/pages/:slug), para las rutas
// /legal/[slug] y /info/[slug] (y sus equivalentes /en/…). 404 si no existe o no está
// publicada; si la URL usa el slug del otro idioma o el prefijo de otro tipo (p. ej.
// /en/legal/privacidad), redirige (301) a la URL canónica. Fija el SEO (canonical y hreflang con
// los slugs de los dos idiomas) y los parámetros del selector de idioma.
//
// Todos los composables se llaman antes del primer `await` (después se pierde el contexto de
// Nuxt fuera de <script setup>); lo que depende de la respuesta es reactivo.
import type { PublicPage } from '#shared/pages'

export async function useContentPage(): Promise<Ref<PublicPage>> {
  const nuxtApp = useNuxtApp()
  const route = useRoute()
  const { t, locale } = useI18n()
  const setI18nParams = useSetI18nParams()
  const loc = locale.value === 'en' ? 'en' : 'es'
  const slug = String(route.params.slug ?? '')

  const request = useFetch<PublicPage>(`/api/pages/${encodeURIComponent(slug)}`, {
    query: { locale: loc },
    key: `content-page:${loc}:${slug}`,
  })
  const data = request.data

  usePageSeo(
    () => data.value?.paths ?? { es: route.path, en: route.path },
    () => `${data.value?.title ?? ''} · Horus Flow`,
    () => data.value?.summary || t('meta.homeDescription'),
  )

  await request
  if (!data.value) {
    const status = request.error.value?.statusCode ?? 404
    throw createError({
      statusCode: status,
      statusMessage: status === 404 ? 'Page Not Found' : 'Error',
      fatal: true,
    })
  }

  const page = data as Ref<PublicPage>
  nuxtApp.runWithContext(() =>
    setI18nParams({ es: { slug: page.value.slugs.es }, en: { slug: page.value.slugs.en } }),
  )

  const canonical = page.value.paths[loc]
  if (route.path.replace(/\/+$/, '') !== canonical) {
    await nuxtApp.runWithContext(() => navigateTo(canonical, { redirectCode: 301, replace: true }))
  }
  return page
}
