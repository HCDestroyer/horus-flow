// Ayudas del e2e del panel: credenciales del primer administrador (ADMIN_EMAIL +
// ADMIN_PASSWORD_FILE en playwright.config.ts) y login con TOTP. El secreto TOTP y el último
// paso usado se guardan en tests/e2e/.data/totp.json para poder volver a entrar sin repetir un
// código (el servidor rechaza un código ya usado).
import { readFileSync, writeFileSync } from 'node:fs'
import { expect, type Page } from '@playwright/test'
import { totpAt, totpStep } from '../../server/lib/auth/crypto'

export const E2E_ADMIN = { email: 'admin@kns.gt', password: 'contraseña-de-pruebas-e2e' }
const STATE = 'tests/e2e/.data/totp.json'

function readState(): { secret: string; lastStep: number } | null {
  try {
    return JSON.parse(readFileSync(STATE, 'utf8'))
  } catch {
    return null
  }
}

/** Código TOTP aún no usado (paso actual o el siguiente, que el servidor también acepta). */
export function nextCode(secret: string): string {
  const prev = readState()
  let step = totpStep(Date.now())
  if (prev && prev.secret === secret && prev.lastStep >= step) step = prev.lastStep + 1
  writeFileSync(STATE, JSON.stringify({ secret, lastStep: step }))
  return totpAt(secret, step)
}

/** Entra al panel: alta del TOTP la primera vez (lee la clave de la página) o código TOTP. */
export async function loginAdmin(page: Page): Promise<{ recoveryCodes: string[] }> {
  await page.goto('/admin/login')
  await page.getByLabel('Correo').fill(E2E_ADMIN.email)
  await page.getByLabel('Contraseña').fill(E2E_ADMIN.password)
  await page.getByRole('button', { name: 'Continuar' }).click()
  const heading = page.getByRole('heading', { level: 1 })
  await expect(heading).toHaveText(/Activa la verificación|Verificación en dos pasos/)
  let recoveryCodes: string[] = []
  if ((await heading.textContent())?.includes('Activa')) {
    await expect(page.getByTestId('totp-qr')).toBeVisible()
    await page.getByText('¿No puedes escanear?').click()
    const secret = (await page.getByTestId('totp-secret').textContent())!.trim()
    await page.getByLabel('Código de 6 dígitos').fill(nextCode(secret))
    await page.getByRole('button', { name: 'Activar y entrar' }).click()
    await expect(page.getByTestId('recovery-codes')).toBeVisible()
    recoveryCodes = (await page.getByTestId('recovery-codes').locator('li').allTextContents()).map(
      (s) => s.trim(),
    )
    expect(recoveryCodes).toHaveLength(10)
    await page.getByRole('link', { name: 'He guardado los códigos' }).click()
  } else {
    const state = readState()
    if (!state) throw new Error('Sin secreto TOTP guardado')
    await page.getByLabel('Código', { exact: true }).fill(nextCode(state.secret))
    await page.getByRole('button', { name: 'Verificar' }).click()
  }
  await expect(page).toHaveURL(/\/admin\/solicitudes/)
  await expect(page.getByRole('heading', { level: 1, name: 'Solicitudes' })).toBeVisible()
  return { recoveryCodes }
}
