import { expect, test } from '@playwright/test'
import { hydrated } from './helpers'

test('la portada tiene título, secciones y CTA', async ({ page }) => {
  await page.goto('/')
  await expect(page).toHaveTitle(/Horus Flow/)
  await expect(page.locator('html')).toHaveAttribute('lang', 'es-GT')
  await expect(page.getByRole('heading', { level: 1 })).toContainText('botnet')
  for (const id of ['features', 'how', 'reliability', 'pricing', 'demo', 'faq']) {
    await expect(page.locator(`#${id}`)).toBeAttached()
  }
  await expect(page.getByRole('link', { name: 'Solicitar demo' }).first()).toBeVisible()
  await expect(page.getByRole('link', { name: 'Ver precios' })).toBeVisible()
})

test('la navegación lleva a las secciones', async ({ page }) => {
  await page.goto('/')
  await hydrated(page)
  await page
    .getByRole('navigation', { name: 'Principal' })
    .getByRole('link', { name: 'Precios' })
    .click()
  await expect(page).toHaveURL(/#pricing$/)
  await expect(page.locator('#pricing-title')).toBeInViewport()
})

test('precios aprobados desde la base de datos (anual en USD por defecto)', async ({ page }) => {
  await page.goto('/')
  const pricing = page.locator('#pricing')
  await expect(pricing.getByTestId('launch-price')).toHaveCount(0)
  await expect(pricing.locator('[data-plan="small"]')).toContainText('1,490')
  await expect(pricing.locator('[data-plan="medium"]')).toContainText('3,990')
  await expect(pricing.locator('[data-plan="large"]')).toContainText('9,900')
  await expect(pricing.locator('[data-plan="enterprise"]')).toContainText('A medida')
  await expect(pricing).toContainText('Soporte técnico 24/7')
})

test('el selector mensual/anual cambia el periodo y se lleva a la compra', async ({ page }) => {
  await page.goto('/')
  await hydrated(page)
  const pricing = page.locator('#pricing')
  // Precios se hidrata al acercarse a la pantalla: se desplaza antes de interactuar.
  await pricing.scrollIntoViewIfNeeded()
  await page.waitForTimeout(500)
  await expect(pricing.locator('[data-plan="small"]')).toContainText('Pago anual')
  await pricing.getByText('Mensual', { exact: true }).click()
  await expect(pricing.locator('[data-plan="small"]')).toContainText('Pago mensual')
  await pricing.getByRole('link', { name: 'Comprar licencia: Grande' }).click()
  await expect(page).toHaveURL(/\/comprar\?plan=large&period=monthly/)
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Comprar una licencia')
})

test('las páginas legales son borradores marcados para revisión', async ({ page }) => {
  for (const [path, title] of [
    ['/legal/aviso-legal', 'Aviso legal'],
    ['/legal/privacidad', 'Política de privacidad'],
    ['/legal/terminos', 'Términos y condiciones'],
  ]) {
    await page.goto(path)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(title)
    await expect(page.getByRole('note')).toContainText('Borrador pendiente de revisión legal')
  }
  await expect(page.getByRole('contentinfo')).toContainText('C&S Company')
  await expect(page.getByRole('contentinfo')).toContainText('info@kns.gt')
})

test('páginas legales de la base de datos: inglés, nuevas políticas, redirecciones y 404', async ({
  page,
  request,
}) => {
  // URLs en inglés de siempre, con el texto en español y el aviso.
  for (const [path, title] of [
    ['/en/legal/notice', 'Legal notice'],
    ['/en/legal/privacy', 'Privacy policy'],
    ['/en/legal/terms', 'Terms and conditions'],
  ]) {
    expect((await page.goto(path))?.status(), path).toBe(200)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(title)
    await expect(page.getByRole('note')).toContainText('only available in Spanish')
  }
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute(
    'href',
    'https://horusflow.kns.gt/en/legal/terms',
  )
  await expect(page.locator('link[hreflang="es"]')).toHaveAttribute(
    'href',
    'https://horusflow.kns.gt/legal/terminos',
  )

  // Políticas nuevas (borradores), enlazadas en el pie.
  for (const [path, title] of [
    ['/legal/cookies', 'Política de cookies'],
    ['/legal/uso-aceptable', 'Política de uso aceptable'],
    ['/legal/soporte', 'Soporte y niveles de servicio'],
    ['/legal/inteligencia-artificial', 'Inteligencia artificial y tratamiento de datos'],
    ['/legal/seguridad', 'Seguridad y divulgación responsable'],
  ]) {
    expect((await page.goto(path))?.status(), path).toBe(200)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(title)
    await expect(page.getByRole('note')).toContainText('Borrador pendiente de revisión legal')
    await expect(page.getByRole('contentinfo').getByRole('link', { name: title })).toHaveAttribute(
      'href',
      path,
    )
  }
  await expect(page.locator('article')).toContainText('[POR CONFIRMAR')

  // El slug del otro idioma redirige a la URL canónica; uno desconocido da 404.
  await page.goto('/en/legal/privacidad')
  await expect(page).toHaveURL(/\/en\/legal\/privacy$/)
  expect((await request.get('/legal/no-existe', { maxRedirects: 0 })).status()).toBe(404)
  expect((await request.get('/en/legal/no-such-page', { maxRedirects: 0 })).status()).toBe(404)
  expect((await request.get('/api/pages/no-existe')).status()).toBe(404)

  const sitemap = await (await request.get('/sitemap.xml')).text()
  expect(sitemap).toContain('<loc>https://horusflow.kns.gt/legal/cookies</loc>')
  expect(sitemap).toContain('<loc>https://horusflow.kns.gt/en/legal/ai</loc>')
})

test('SEO: canonical, Open Graph, JSON-LD, sitemap y robots', async ({ page, request }) => {
  await page.goto('/')
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute(
    'href',
    'https://horusflow.kns.gt/',
  )
  await expect(page.locator('link[hreflang="en"]')).toHaveAttribute(
    'href',
    'https://horusflow.kns.gt/en',
  )
  await expect(page.locator('meta[property="og:image"]')).toHaveAttribute(
    'content',
    'https://horusflow.kns.gt/img/og.png',
  )
  await expect(page.locator('meta[name="twitter:card"]')).toHaveAttribute(
    'content',
    'summary_large_image',
  )
  const ld = JSON.parse(
    (await page.locator('script[type="application/ld+json"]').textContent()) ?? '{}',
  )
  const types = ld['@graph'].map((n: { '@type': string }) => n['@type'])
  expect(types).toEqual(expect.arrayContaining(['Organization', 'SoftwareApplication', 'Product']))
  const product = ld['@graph'].find((n: { '@type': string }) => n['@type'] === 'Product')
  expect(product.offers.length).toBeGreaterThan(0)
  expect(product.offers[0]).toMatchObject({ price: 1490, priceCurrency: 'USD' })

  const sitemap = await (await request.get('/sitemap.xml')).text()
  expect(sitemap).toContain('<loc>https://horusflow.kns.gt/en/buy</loc>')
  const robots = await (await request.get('/robots.txt')).text()
  expect(robots).toContain('Sitemap: https://horusflow.kns.gt/sitemap.xml')
})
