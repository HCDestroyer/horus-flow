import { expect, test } from '@playwright/test'
import { fillCredentials, loginAsNoc, NOC, PASSWORD, seriousViolations } from './helpers'

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

test.describe('datos de la cuenta', () => {
  test('editar nombre y zona horaria', async ({ page }) => {
    await loginAsNoc(page)
    await page.goto('/account')
    const card = page.getByTestId('profile-card')
    await expect(card).toBeVisible()
    await expect(page.getByTestId('profile-name')).toHaveValue('Luis Prieto')
    // Sin cambios no hay nada que guardar.
    await expect(page.getByTestId('profile-submit')).toBeDisabled()

    await page.getByTestId('profile-name').fill('Luis Prieto Gómez')
    await page.getByTestId('profile-timezone').click()
    await page.getByPlaceholder('Buscar zona horaria…').fill('America/Lima')
    await page.getByRole('option', { name: 'America/Lima', exact: true }).click()
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('profile-submit').click()

    await expect(page.getByText('Datos de la cuenta guardados.').first()).toBeVisible()
    await expect(page.getByTestId('profile-done')).toBeVisible()
    // La cabecera, el menú de usuario y la zona horaria reflejan `GET /me` recargado.
    await expect(page.locator('dd', { hasText: 'America/Lima' })).toBeVisible()
    await expect(page.getByTestId('user-menu')).toContainText('Luis Prieto Gómez')
  })

  test('cambiar el correo exige re-autenticación; correo duplicado', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.reauthRequired', '1'))
    await loginAsNoc(page)
    await page.goto('/account')
    const card = page.getByTestId('email-card')
    await expect(page.getByTestId('email-current')).toHaveValue(NOC)

    // El mismo correo o uno mal escrito no se envían.
    await page.getByTestId('email-new').fill(NOC)
    await expect(card).toContainText('Es el mismo correo que ya tienes.')
    await expect(page.getByTestId('email-submit')).toBeDisabled()

    // Correo de otra cuenta: primero confirma la identidad (sin TOTP: solo contraseña) y
    // luego el backend responde ALREADY_EXISTS.
    await page.getByTestId('email-new').fill('ana.ruiz@fibranorte.example')
    await page.getByTestId('email-submit').click()
    const reauth = page.getByRole('dialog', { name: 'Confirma tu identidad' })
    await expect(reauth).toContainText('cambiar el correo')
    await expect(page.getByTestId('reauth-code')).toHaveCount(0)
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('reauth-password').fill(PASSWORD)
    await page.getByTestId('reauth-submit').click()
    await expect(reauth).toHaveCount(0)
    await expect(page.getByTestId('email-error')).toHaveText('Ya existe una cuenta con ese correo.')

    // Re-autenticación reciente: el cambio real ya no la pide.
    await page.getByTestId('email-new').fill('luis.prieto@fibranorte.example')
    await page.getByTestId('email-submit').click()
    await expect(page.getByTestId('email-done')).toContainText('luis.prieto@fibranorte.example')
    await expect(page.getByTestId('email-current')).toHaveValue('luis.prieto@fibranorte.example')
    await expect(page.getByTestId('email-new')).toHaveValue('')

    // El correo nuevo es el usuario para iniciar sesión.
    await page.getByTestId('user-menu').click()
    await page.getByRole('menuitem', { name: 'Cerrar sesión' }).click()
    await expect(page).toHaveURL(/\/login/)
    await fillCredentials(page, 'luis.prieto@fibranorte.example')
    await expect(page).toHaveURL(/\/t\/fibra-norte$/)
  })
})
