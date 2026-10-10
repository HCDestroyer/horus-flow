// /robots.txt. ROBOTS_DISALLOW_ALL=1 en entornos de pruebas para que no se indexen.
import { siteBase } from '../utils/site-pages'

export default defineEventHandler((event) => {
  const base = siteBase(useRuntimeConfig(event).public.siteUrl)
  setResponseHeader(event, 'Content-Type', 'text/plain; charset=utf-8')
  if (process.env.ROBOTS_DISALLOW_ALL === '1') return 'User-agent: *\nDisallow: /\n'
  return `User-agent: *\nAllow: /\nDisallow: /api/\n\nSitemap: ${base}/sitemap.xml\n`
})
