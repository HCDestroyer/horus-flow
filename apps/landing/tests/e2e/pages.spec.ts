// Páginas interiores (producto, IA, monitoreo, pantallas NOC) en español e inglés, la barra de
// navegación con sus estados y el movimiento como mejora progresiva (reduced motion, sin JS).
import { expect, test } from '@playwright/test'
import { hydrated } from './helpers'

const PAGES = [
  {
    es: '/producto',
    en: '/en/product',
    nav: ['Producto', 'Product'],
    h1: [/pantalla por pantalla/, /screen by screen/],
  },
  {
    es: '/inteligencia-artificial',
    en: '/en/ai',
    nav: ['Asistente de IA', 'AI assistant'],
    h1: [/sin entregar los datos/, /without handing over/],
  },
  {
    es: '/monitoreo',
    en: '/en/monitoring',
    nav: ['Monitoreo', 'Monitoring'],
    h1: [/salud de cada MikroTik/, /health of every MikroTik/],
  },
  {
    es: '/pantallas-noc',
    en: '/en/noc-screens',
    nav: ['Pantallas NOC', 'NOC screens'],
    h1: [/cualquier televisor/, /any TV/],
  },
] as const

const BASE = 'https://horusflow.kns.gt'

for (const p of PAGES) {
  test(`${p.es} y ${p.en}: titular, idioma, SEO y datos estructurados`, async ({ page }) => {
    for (const [i, path] of [p.es, p.en].entries()) {
      const res = await page.goto(path)
      expect(res?.status(), path).toBe(200)
      await expect(page.getByRole('heading', { level: 1 })).toHaveText(p.h1[i]!)
      await expect(page.locator('html')).toHaveAttribute('lang', i === 0 ? 'es-GT' : 'en')
      await expect(page).toHaveTitle(/Horus Flow/)
      await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', BASE + path)
      await expect(page.locator('link[hreflang="es"]')).toHaveAttribute('href', BASE + p.es)
      await expect(page.locator('link[hreflang="en"]')).toHaveAttribute('href', BASE + p.en)
      const ld = JSON.parse(
        (await page.locator('script[type="application/ld+json"]').first().textContent()) ?? '{}',
      )
      const types = ld['@graph'].map((n: { '@type': string }) => n['@type'])
      expect(types).toEqual(expect.arrayContaining(['WebPage', 'BreadcrumbList']))
      // Ruta visible: Inicio › Página.
      await expect(
        page.getByRole('navigation', { name: i === 0 ? 'Ruta de navegación' : 'Breadcrumb' }),
      ).toContainText(i === 0 ? 'Inicio' : 'Home')
    }
  })
}

test('la página de IA publica sus preguntas como FAQPage', async ({ page }) => {
  await page.goto('/inteligencia-artificial')
  const ld = JSON.parse(
    (await page.locator('script[type="application/ld+json"]').first().textContent()) ?? '{}',
  )
  const faq = ld['@graph'].find((n: { '@type': string }) => n['@type'] === 'FAQPage')
  expect(faq.mainEntity.length).toBe(5)
  await expect(page.getByText('¿La IA puede cambiar algo en mis routers?')).toBeVisible()
})

test('el sitemap incluye las páginas nuevas en los dos idiomas', async ({ request }) => {
  const sitemap = await (await request.get('/sitemap.xml')).text()
  for (const p of PAGES) {
    expect(sitemap).toContain(`<loc>${BASE}${p.es}</loc>`)
    expect(sitemap).toContain(`<loc>${BASE}${p.en}</loc>`)
  }
})

test('la barra lleva a cada página y marca la actual', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  for (const [locale, home] of [
    [0, '/'],
    [1, '/en'],
  ] as const) {
    await page.goto(home)
    await hydrated(page)
    const nav = page.getByRole('navigation', { name: locale === 0 ? 'Principal' : 'Main' })
    for (const p of PAGES) {
      await nav.getByRole('link', { name: p.nav[locale], exact: true }).click()
      await expect(page).toHaveURL(new RegExp(`${locale === 0 ? p.es : p.en}$`))
      await expect(page.getByRole('heading', { level: 1 })).toHaveText(p.h1[locale])
      await expect(nav.getByRole('link', { name: p.nav[locale], exact: true })).toHaveAttribute(
        'aria-current',
        'page',
      )
    }
  }
})

test('menú móvil: abre, navega, se cierra con Escape y devuelve el foco', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  await hydrated(page)
  const toggle = page.getByRole('button', { name: 'Menú' })
  await toggle.click()
  const menu = page.locator('#menu-movil')
  await expect(menu).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(menu).toBeHidden()
  await expect(toggle).toBeFocused()

  await toggle.click()
  await menu.getByRole('link', { name: 'Monitoreo' }).click()
  await expect(page).toHaveURL(/\/monitoreo$/)
  await expect(menu).toBeHidden()
  await expect(page.getByRole('heading', { level: 1 })).toContainText('MikroTik')
})

test('la portada enlaza las novedades y el recorrido', async ({ page }) => {
  await page.goto('/')
  const teasers = page.locator('#novedades')
  await expect(teasers.getByRole('link', { name: 'Analizar con IA' })).toHaveAttribute(
    'href',
    '/inteligencia-artificial',
  )
  await expect(teasers.getByRole('link', { name: 'Salud de routers' })).toHaveAttribute(
    'href',
    '/monitoreo',
  )
  await expect(page.getByRole('link', { name: 'Ver el recorrido completo' })).toHaveAttribute(
    'href',
    '/producto',
  )
})

// ─── Movimiento: mejora progresiva ─────────────────────────────────────────────────────────

const hiddenReveals = () =>
  [...document.querySelectorAll<HTMLElement>('[data-reveal]')].filter(
    (el) => getComputedStyle(el).opacity !== '1',
  ).length

test.describe('con prefers-reduced-motion', () => {
  test.beforeEach(async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
  })

  test('todo el contenido se ve sin desplazarse ni esperar', async ({ page }) => {
    for (const path of [
      '/',
      '/producto',
      '/inteligencia-artificial',
      '/monitoreo',
      '/pantallas-noc',
    ]) {
      await page.goto(path)
      await hydrated(page)
      await expect(page.locator('html')).not.toHaveClass(/(^|\s)rv(\s|$)/)
      expect(await page.evaluate(hiddenReveals), path).toBe(0)
    }
  })

  test('las animaciones en bucle no arrancan y no ofrecen pausa', async ({ page }) => {
    await page.goto('/monitoreo')
    await hydrated(page)
    const grid = page.locator('[data-cycle="routers"]').first()
    const before = await grid
      .locator('[data-tile]')
      .evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.state))
    await page.waitForTimeout(2600)
    const after = await grid
      .locator('[data-tile]')
      .evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.state))
    expect(after).toEqual(before)
    await expect(grid.locator('[data-cycle-toggle]')).toBeHidden()
  })
})

test.describe('sin reduced motion', () => {
  test.beforeEach(async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'no-preference' })
  })

  test('lo de abajo aparece al llegar a la vista', async ({ page }) => {
    await page.goto('/producto')
    await hydrated(page)
    await expect(page.locator('html')).toHaveClass(/(^|\s)rv(\s|$)/)
    const target = page.locator('#operations [data-reveal]').first()
    await expect(target).toHaveCSS('opacity', '0')
    await target.scrollIntoViewIfNeeded()
    await expect(target).toHaveClass(/rv-in/)
    await expect(target).toHaveCSS('opacity', '1')
    // El titular (LCP) nunca se oculta.
    await expect(page.getByRole('heading', { level: 1 })).toHaveCSS('opacity', '1')
  })

  test('la salud de routers cambia de estado y se puede pausar', async ({ page }) => {
    await page.goto('/monitoreo')
    await hydrated(page)
    const grid = page.locator('[data-cycle="routers"]').first()
    const states = () =>
      grid
        .locator('[data-tile]')
        .evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.state).join())
    const initial = await states()
    await expect.poll(states, { timeout: 8000 }).not.toBe(initial)
    const pause = grid.getByRole('button', { name: 'Pausar animación' })
    await pause.click()
    await expect(grid.getByRole('button', { name: 'Reproducir animación' })).toBeVisible()
    const frozen = await states()
    await page.waitForTimeout(2600)
    expect(await states()).toBe(frozen)
  })

  test('el contador termina en el valor del HTML', async ({ page }) => {
    await page.goto('/')
    await hydrated(page)
    const n = page.locator('[data-count="10000"]')
    await n.scrollIntoViewIfNeeded()
    await expect(n).toHaveText('10 000', { timeout: 4000 })
  })
})

test.describe('sin JavaScript', () => {
  test.use({ javaScriptEnabled: false })

  test('el HTML del servidor es legible: nada oculto', async ({ page }) => {
    for (const path of ['/', '/inteligencia-artificial', '/monitoreo']) {
      await page.goto(path)
      expect((await page.locator('html').getAttribute('class')) ?? '').not.toMatch(/(^|\s)rv(\s|$)/)
      const hidden = await page
        .locator('[data-reveal]')
        .evaluateAll((els) => els.filter((el) => getComputedStyle(el).opacity !== '1').length)
      expect(hidden, path).toBe(0)
      // El análisis de IA completo está en el HTML.
      if (path === '/inteligencia-artificial') {
        await expect(page.getByText('Verificada').first()).toBeVisible()
      }
    }
  })
})
