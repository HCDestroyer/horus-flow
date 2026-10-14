// Animaciones neón (MotionVideo): póster estático con reduced motion, vídeo diferido sin él,
// pausa fuera de pantalla o con la pestaña oculta, póster como LCP del hero y axe sin
// violaciones con el vídeo en marcha.
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { hydrated } from './helpers'

const hero = '[data-motion]:has(source[data-src-small*="hero-network"])'
const how = '[data-motion]:has(source[data-src-small*="how-it-works"])'

function videoState(page: Page, selector: string) {
  return page.locator(`${selector} video`).evaluate((v: HTMLVideoElement) => ({
    src: v.currentSrc,
    paused: v.paused,
    time: v.currentTime,
    ready: v.readyState,
  }))
}

test.describe('con prefers-reduced-motion', () => {
  test('solo el póster y un botón "Reproducir animación"; nada se descarga', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(
      true,
    )
    const videoRequests: string[] = []
    page.on('request', (r) => {
      if (/\/motion\/.*\.(webm|mp4)$/.test(r.url())) videoRequests.push(r.url())
    })
    await page.goto('/')
    await hydrated(page)

    const poster = page.locator(`${hero} img`)
    await expect(poster).toBeVisible()
    await expect
      .poll(() => poster.evaluate((i: HTMLImageElement) => i.naturalWidth))
      .toBeGreaterThan(0)

    const play = page.locator(hero).getByRole('button', { name: 'Reproducir animación' })
    await expect(play).toBeVisible()
    await expect(play).toContainText('Reproducir animación')
    await page.waitForTimeout(800)
    expect((await videoState(page, hero)).src).toBe('')
    expect(videoRequests).toEqual([])

    // Bajo demanda sí se reproduce, y se puede volver a pausar.
    await play.click()
    await expect.poll(async () => (await videoState(page, hero)).paused).toBe(false)
    const pause = page.locator(hero).getByRole('button', { name: 'Pausar animación' })
    await expect(pause).toBeVisible()
    await pause.click()
    await expect.poll(async () => (await videoState(page, hero)).paused).toBe(true)
  })
})

test.describe('sin reduced motion', () => {
  test.beforeEach(async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'no-preference' })
  })

  test('el vídeo del hero se carga y reproduce; el de "cómo funciona" espera a verse', async ({
    page,
  }) => {
    await page.goto('/')
    await hydrated(page)

    await expect
      .poll(async () => (await videoState(page, hero)).src)
      .toMatch(/\/motion\/hero-network-(960|1920)\.(webm|mp4)$/)
    await expect.poll(async () => (await videoState(page, hero)).time).toBeGreaterThan(0)
    await expect(page.locator(`${hero} video`)).toHaveAttribute('aria-hidden', 'true')
    for (const attr of ['autoplay', 'muted', 'loop', 'playsinline']) {
      await expect(page.locator(`${hero} video`)).toHaveAttribute(attr, '')
    }
    await expect(page.locator(`${hero} video`)).toHaveAttribute('preload', /none|auto/)
    // Nombre accesible equivalente en el contenedor.
    await expect(page.locator(hero).getByRole('img')).toHaveAccessibleName(/10\.20\.1\.47/)

    // Fuera de pantalla todavía no hay fuente.
    expect((await videoState(page, how)).src).toBe('')
    await page.locator(how).scrollIntoViewIfNeeded()
    await expect.poll(async () => (await videoState(page, how)).paused).toBe(false)
    // El hero, ya fuera de pantalla, queda en pausa.
    await expect.poll(async () => (await videoState(page, hero)).paused).toBe(true)
  })

  test('el botón pausa y la pestaña oculta también', async ({ page }) => {
    await page.goto('/')
    await hydrated(page)
    await expect.poll(async () => (await videoState(page, hero)).paused).toBe(false)

    await page.locator(hero).getByRole('button', { name: 'Pausar animación' }).click()
    await expect.poll(async () => (await videoState(page, hero)).paused).toBe(true)
    await page.locator(hero).getByRole('button', { name: 'Reproducir animación' }).click()
    await expect.poll(async () => (await videoState(page, hero)).paused).toBe(false)

    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await expect.poll(async () => (await videoState(page, hero)).paused).toBe(true)
    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await expect.poll(async () => (await videoState(page, hero)).paused).toBe(false)
  })

  test('el póster del hero es el LCP en escritorio', async ({ page }) => {
    await page.goto('/')
    const lcp = await page.evaluate(
      () =>
        new Promise<string>((resolve) => {
          new PerformanceObserver((list) => {
            const entries = list.getEntries() as (PerformanceEntry & { element?: Element })[]
            const last = entries[entries.length - 1]
            resolve(last?.element?.className?.toString() ?? '')
          }).observe({ type: 'largest-contentful-paint', buffered: true })
        }),
    )
    expect(lcp).toContain('motion-poster')
  })

  test('axe sin violaciones con el vídeo en marcha', async ({ page }) => {
    await page.goto('/')
    await hydrated(page)
    await expect.poll(async () => (await videoState(page, hero)).paused).toBe(false)
    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice'])
      .analyze()
    expect(results.violations.map((v) => v.id)).toEqual([])
  })
})

// Capturas de las animaciones (solo con `pnpm screenshots`): vídeo en marcha en claro y oscuro,
// y el póster con el botón "Reproducir animación" (reduced motion) en móvil.
const resilience = '[data-motion]:has(source[data-src-small*="resilience"])'
const shots = [
  {
    name: 'motion-hero-1440-oscuro',
    width: 1440,
    scheme: 'dark',
    motion: 'no-preference',
    at: hero,
  },
  {
    name: 'motion-hero-1440-claro',
    width: 1440,
    scheme: 'light',
    motion: 'no-preference',
    at: hero,
  },
  {
    name: 'motion-como-funciona-1440-oscuro',
    width: 1440,
    scheme: 'dark',
    motion: 'no-preference',
    at: how,
  },
  {
    name: 'motion-fiabilidad-1440-claro',
    width: 1440,
    scheme: 'light',
    motion: 'no-preference',
    at: resilience,
  },
  {
    name: 'motion-hero-390-reduced-oscuro',
    width: 390,
    scheme: 'dark',
    motion: 'reduce',
    at: hero,
  },
] as const

for (const s of shots) {
  test(`@screenshots ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.width === 390 ? 844 : 900 })
    await page.emulateMedia({ colorScheme: s.scheme, reducedMotion: s.motion })
    await page.goto('/')
    await hydrated(page)
    const target = page.locator(s.at)
    await target.scrollIntoViewIfNeeded()
    if (s.motion === 'no-preference') {
      await expect
        .poll(async () => (await videoState(page, s.at)).time, { timeout: 15_000 })
        .toBeGreaterThan(2)
    } else {
      await page.waitForTimeout(500)
    }
    await target
      .locator('xpath=ancestor::section[1]')
      .screenshot({ path: `docs/screenshots/${s.name}.jpg`, type: 'jpeg', quality: 78 })
  })
}
