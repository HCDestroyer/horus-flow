// /sitemap.xml con las dos versiones de idioma de cada página (hreflang).
import { SITE_PAGES, siteBase } from '../utils/site-pages'

export default defineEventHandler((event) => {
  const base = siteBase(useRuntimeConfig(event).public.siteUrl)
  const urls = SITE_PAGES.flatMap((page) =>
    (['es', 'en'] as const).map((locale) => {
      const alternates = [
        `<xhtml:link rel="alternate" hreflang="es" href="${base}${page.es}"/>`,
        `<xhtml:link rel="alternate" hreflang="en" href="${base}${page.en}"/>`,
        `<xhtml:link rel="alternate" hreflang="x-default" href="${base}${page.es}"/>`,
      ].join('')
      return `<url><loc>${base}${page[locale]}</loc>${alternates}<priority>${page.priority}</priority></url>`
    }),
  )
  setResponseHeader(event, 'Content-Type', 'application/xml; charset=utf-8')
  setResponseHeader(event, 'Cache-Control', 'public, max-age=3600')
  return (
    '<?xml version="1.0" encoding="UTF-8"?>' +
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">' +
    urls.join('') +
    '</urlset>'
  )
})
