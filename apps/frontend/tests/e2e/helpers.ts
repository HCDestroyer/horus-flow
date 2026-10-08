import AxeBuilder from '@axe-core/playwright'
import { expect, type Page } from '@playwright/test'

export const PASSWORD = 'horus-demo-2026'
export const TOTP = '123456'
export const ADMIN = 'ana.ruiz@fibranorte.example'
export const NOC = 'noc@fibranorte.example'

export async function fillCredentials(page: Page, email: string, password = PASSWORD) {
  await page.getByLabel('Correo electrónico').fill(email)
  await page.getByLabel('Contraseña', { exact: true }).fill(password)
  await page.getByTestId('login-submit').click()
}

export async function loginAsNoc(page: Page) {
  await page.goto('/login')
  await fillCredentials(page, NOC)
  await expect(page).toHaveURL(/\/t\/fibra-norte$/)
}

export async function loginAsAdmin(page: Page) {
  await page.goto('/login')
  await fillCredentials(page, ADMIN)
  await page.getByLabel('Código de verificación').fill(TOTP)
  await page.getByTestId('mfa-submit').click()
  await expect(page).toHaveURL(/\/t\/fibra-norte$/)
}

/** Fija la preferencia de tema como lo hace el menú de usuario (localStorage de color-mode). */
export async function setTheme(page: Page, theme: 'system' | 'light' | 'dark') {
  await page.addInitScript((value) => {
    window.localStorage.setItem('horus-color-mode', value)
  }, theme)
}

/** Violaciones serias o críticas de axe (WCAG 2.1 A/AA). */
export async function seriousViolations(page: Page) {
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    .analyze()
  return results.violations
    .filter((v) => v.impact === 'serious' || v.impact === 'critical')
    .map((v) => ({ id: v.id, impact: v.impact, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}
