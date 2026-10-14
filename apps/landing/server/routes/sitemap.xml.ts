// /sitemap.xml con las dos versiones de idioma de cada página (hreflang): las fijas de
// server/utils/site-pages.ts y las páginas legales e informativas publicadas (base de datos).
import { useApp } from '../lib/context'
import { publishedPagePaths } from '../lib/pages'
import { SITE_PAGES, isDbPagePath, siteBase } from '../utils/site-pages'

const escapeXml = (s: string) => s.replace(/[<>&'"]/g, (c) => `&#${c.charCodeAt(0)};`)

export default defineEventHandler((event) => {
  const base = siteBase(useRuntimeConfig(event).public.siteUrl)
  const pages = [
    ...SITE_PAGES.filter((p) => !isDbPagePath(p.es)),
    ...publishedPagePaths(useApp().db).map((p) => ({ es: p.es, en: p.en, priority: 0.2 })),
  ]
  const urls = pages.flatMap((page) =>
    (['es', 'en'] as const).map((locale) => {
      const es = escapeXml(base + page.es)
      const en = escapeXml(base + page.en)
      const alternates = [
        `<xhtml:link rel="alternate" hreflang="es" href="${es}"/>`,
        `<xhtml:link rel="alternate" hreflang="en" href="${en}"/>`,
        `<xhtml:link rel="alternate" hreflang="x-default" href="${es}"/>`,
      ].join('')
      return `<url><loc>${escapeXml(base + page[locale])}</loc>${alternates}<priority>${page.priority}</priority></url>`
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
