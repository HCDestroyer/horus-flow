import { expect, test } from '@playwright/test'
import { hydrated } from './helpers'

test('cambia a inglés y vuelve a español conservando la página', async ({ page }) => {
  await page.goto('/comprar?plan=small')
  await hydrated(page)
  await page.getByRole('link', { name: 'Idioma: English' }).click()
  await expect(page).toHaveURL(/\/en\/buy/)
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Buy a license')
  await expect(page).toHaveTitle('Buy a license · Horus Flow')

  await page.getByRole('link', { name: 'Language: Español' }).click()
  await expect(page).toHaveURL(/\/comprar/)
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Comprar una licencia')
})

test('la portada en inglés', async ({ page }) => {
  await page.goto('/en')
  await expect(page.getByRole('heading', { level: 1 })).toContainText('botnet')
  await expect(page.locator('#pricing [data-plan="medium"]')).toContainText('$3,990')
  await expect(page.locator('#pricing')).toContainText('24/7 technical support')
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', /\/en$/)
})
