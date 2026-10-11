import { expect, test } from '@playwright/test'
import { fillCredentials, PASSWORD, seriousViolations } from './helpers'

/**
 * Superadministrador recién instalado (install.sh): contraseña inicial obligatoria de cambiar y
 * sin TOTP. Antes la consola de plataforma mostraba TOKEN_SCOPE_INVALID porque el backend
 * negaba el token de plataforma y la UI seguía sin él; ahora lleva a "Mi cuenta".
 */
const SUPERADMIN = 'admin@horus.example'
const NEW_PASSWORD = 'Una-contraseña-nueva-2026'

test('superadmin nuevo: cambia la contraseña, activa el TOTP y entra en la plataforma', async ({
  page,
}) => {
  await page.goto('/login')
  await fillCredentials(page, SUPERADMIN)

  await expect(page).toHaveURL(/\/account\?required=password/)
  await expect(page.getByTestId('account-required')).toContainText('Cambia la contraseña inicial')
  expect(await seriousViolations(page)).toEqual([])

  // Mientras no cambie la contraseña, ninguna sección: siempre vuelve a "Mi cuenta".
  await page.goto('/platform/system')
  await expect(page).toHaveURL(/\/account\?required=password/)

  await page.getByLabel('Contraseña actual').fill(PASSWORD)
  await page.getByLabel('Nueva contraseña', { exact: true }).fill(NEW_PASSWORD)
  await page.getByLabel('Repite la nueva contraseña').fill(NEW_PASSWORD)
  await page.getByTestId('pw-submit').click()
  await expect(page.getByTestId('pw-done')).toBeVisible()

  // La plataforma exige TOTP.
  await page.goto('/platform/system')
  await expect(page).toHaveURL(/\/account\?required=mfa/)

  await page.getByTestId('totp-start').click()
  await expect(page.getByTestId('totp-qr')).toBeVisible()
  await expect(page.getByTestId('totp-secret')).toHaveText('JBSWY3DPEHPK3PXP')
  await page.getByLabel('Código de 6 dígitos').fill('123456')
  await page.getByTestId('totp-confirm').click()

  await expect(page.getByTestId('recovery-codes').locator('li')).toHaveCount(10)
  await expect(page.getByTestId('mfa-status')).toContainText('Activada')
  expect(await seriousViolations(page)).toEqual([])

  await page.getByTestId('account-continue').click()
  await expect(page).toHaveURL(/\/platform\/system$/)
  await expect(page.getByTestId('installation-access')).not.toContainText('TOKEN_SCOPE_INVALID')
  await expect(page.getByTestId('access-mode')).toBeVisible()
})

test('contraseña nueva corta o distinta: no se envía', async ({ page }) => {
  await page.goto('/login')
  await fillCredentials(page, SUPERADMIN)
  await expect(page).toHaveURL(/\/account\?required=password/)
  await page.getByLabel('Contraseña actual').fill(PASSWORD)
  await page.getByLabel('Nueva contraseña', { exact: true }).fill('corta')
  await expect(page.getByText('Debe tener al menos 12 caracteres.')).toBeVisible()
  await expect(page.getByTestId('pw-submit')).toBeDisabled()
})
