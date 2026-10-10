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

test('precios sin confirmar: "solicita cotización" y ninguna cifra', async ({ page }) => {
  await page.goto('/')
  const pricing = page.locator('#pricing')
  await expect(pricing.getByTestId('launch-price')).toHaveCount(3)
  await expect(pricing.getByTestId('launch-price').first()).toHaveText(
    'Precio de lanzamiento: solicita cotización',
  )
  await expect(pricing).not.toContainText(/(US\$|\$|Q)\s?\d/)
})

test('el selector mensual/anual cambia el periodo y se lleva a la compra', async ({ page }) => {
  await page.goto('/')
  await hydrated(page)
  const pricing = page.locator('#pricing')
  await expect(pricing.locator('[data-plan="small"]')).toContainText('Pago anual')
  await pricing.getByText('Mensual', { exact: true }).click()
  await expect(pricing.locator('[data-plan="small"]')).toContainText('Pago mensual')
  await pricing.getByRole('link', { name: 'Solicitar cotización: Grande' }).click()
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

test('SEO: canonical, Open Graph, JSON-LD, sitemap y robots', async ({ page, request }) => {
  await page.goto('/')
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute(
    'href',
    'https://horusflow.example/',
  )
  await expect(page.locator('link[hreflang="en"]')).toHaveAttribute(
    'href',
    'https://horusflow.example/en',
  )
  await expect(page.locator('meta[property="og:image"]')).toHaveAttribute(
    'content',
    'https://horusflow.example/img/og.png',
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
  expect(product.offers[0].price).toBeUndefined() // precios sin confirmar

  const sitemap = await (await request.get('/sitemap.xml')).text()
  expect(sitemap).toContain('<loc>https://horusflow.example/en/buy</loc>')
  const robots = await (await request.get('/robots.txt')).text()
  expect(robots).toContain('Sitemap: https://horusflow.example/sitemap.xml')
})
