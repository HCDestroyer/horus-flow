import { expect, test } from '@playwright/test'
import { fillCredentials, NOC, seriousViolations } from './helpers'

/**
 * Recuperar contraseña (páginas públicas `/forgot-password` y `/reset-password#token=…`).
 * `pnpm e2e --grep @password-reset`.
 *
 * La API simulada tiene un token fijo para el operador NOC de demo
 * (`mocks/password-reset.ts`): sirve una sola vez, también tras recargar la página.
 */

const DEMO_TOKEN = 'reset-demo-token-0000000000000000'
const NEW_PASSWORD = 'Contraseña-recuperada-2026'

test.describe('recuperar contraseña @password-reset', () => {
  test('desde el login: el formulario responde igual exista o no la cuenta', async ({ page }) => {
    await page.goto('/login')
    await page.getByTestId('login-forgot').click()
    await expect(page).toHaveURL(/\/forgot-password$/)
    await expect(page.getByTestId('forgot-card')).toContainText('Recuperar contraseña')
    expect(await seriousViolations(page)).toEqual([])

    // Correo no válido: no se envía.
    await page.getByTestId('forgot-email').fill('no-es-un-correo')
    await page.getByTestId('forgot-submit').click()
    await expect(page.getByText('Escribe un correo válido')).toBeVisible()
    await expect(page.getByTestId('forgot-sent')).toHaveCount(0)

    await page.getByTestId('forgot-email').fill(NOC)
    await page.getByTestId('forgot-email').press('Enter')
    const sent = page.getByTestId('forgot-sent')
    await expect(sent).toContainText('Si el correo está registrado')
    await expect(sent).toContainText('30 minutos')
    expect(await seriousViolations(page)).toEqual([])
    const known = await sent.textContent()

    // Cuenta inexistente: misma respuesta (no revela si existe).
    await page.goto('/forgot-password')
    await page.getByTestId('forgot-email').fill('nadie@fibranorte.example')
    await page.getByTestId('forgot-submit').click()
    await expect(page.getByTestId('forgot-sent')).toHaveText(known ?? '')

    await page.getByTestId('forgot-back').click()
    await expect(page).toHaveURL(/\/login$/)
  })

  test('sin SMTP: explica que debe pedir el enlace a un administrador', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.smtp', '0'))
    await page.goto('/forgot-password')
    await page.getByTestId('forgot-email').fill(NOC)
    await page.getByTestId('forgot-submit').click()
    const notice = page.getByTestId('forgot-no-email')
    await expect(notice).toContainText('Esta instalación no envía correo')
    await expect(notice).toContainText('administrador de tu ISP')
    await expect(page.getByTestId('forgot-sent')).toHaveCount(0)
    expect(await seriousViolations(page)).toEqual([])
  })

  test('token válido: fija la contraseña, entra con ella y el enlace no vuelve a servir', async ({
    page,
  }) => {
    await page.goto(`/reset-password#token=${DEMO_TOKEN}`)
    const summary = page.getByTestId('reset-summary')
    await expect(summary).toContainText('Luis Prieto')
    await expect(summary).toContainText(NOC)
    // El token sale de la barra de direcciones en cuanto se lee.
    expect(new URL(page.url()).hash).toBe('')
    expect(page.url()).not.toContain('token')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('reset-password').fill('corta')
    await expect(page.getByText('Debe tener al menos 12 caracteres.')).toBeVisible()
    await expect(page.getByTestId('reset-submit')).toBeDisabled()
    await page.getByTestId('reset-password').fill(NEW_PASSWORD)
    await page.getByTestId('reset-repeat').fill(`${NEW_PASSWORD}x`)
    await expect(page.getByText('Las contraseñas no coinciden.')).toBeVisible()
    await page.getByTestId('reset-repeat').fill(NEW_PASSWORD)
    await page.getByTestId('reset-submit').click()

    await expect(page.getByTestId('reset-done')).toContainText(NOC)
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('reset-go-login').click()
    await expect(page).toHaveURL(/\/login$/)
    await fillCredentials(page, NOC, NEW_PASSWORD)
    await expect(page).toHaveURL(/\/t\/fibra-norte$/)

    // Segundo uso del mismo enlace (en otra carga de la página): ya se usó.
    await page.goto('about:blank')
    await page.goto(`/reset-password#token=${DEMO_TOKEN}`)
    await expect(page.getByTestId('reset-invalid')).toContainText('Este enlace ya se usó')
    await expect(page.getByTestId('reset-invalid')).toContainText('Pide uno nuevo')
    await page.getByTestId('reset-again').click()
    await expect(page).toHaveURL(/\/forgot-password$/)
  })

  test('token no válido o ausente: lo dice y ofrece pedir otro', async ({ page }) => {
    await page.goto(`/reset-password#token=${'x'.repeat(48)}`)
    await expect(page.getByTestId('reset-invalid')).toContainText('no es válido')
    expect(page.url()).not.toContain('token')
    expect(await seriousViolations(page)).toEqual([])

    await page.goto('about:blank')
    await page.goto('/reset-password')
    await expect(page.getByTestId('reset-invalid')).toBeVisible()
  })
})
