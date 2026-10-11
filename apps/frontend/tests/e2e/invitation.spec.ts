import { expect, test } from '@playwright/test'
import { fillCredentials, loginAsAdmin, seriousViolations } from './helpers'

/**
 * Aceptar una invitación (página pública `/invitation#token=…`). El enlace se abre en otro
 * navegador (contexto nuevo, sin sesión), como lo haría la persona invitada.
 * `pnpm e2e --grep @invitation`.
 */

const NEW_PASSWORD = 'Contraseña-de-invitada-2026'

/**
 * Token de invitación de la API simulada (mocks/members.ts lleva los datos codificados en el
 * token para que cualquier pestaña lo valide). Solo para el caso "cuenta existente".
 */
function mockToken(payload: Record<string, unknown>) {
  return Buffer.from(JSON.stringify(payload)).toString('base64url')
}

test.describe('invitación @invitation', () => {
  test('cuenta nueva: elige contraseña, va al login con el correo escrito y entra', async ({
    page,
    browser,
  }) => {
    await loginAsAdmin(page)
    await page.goto('/t/fibra-norte/admin/users')
    await page.getByTestId('invite-open').click()
    await page.getByTestId('invite-email').fill('invitada@fibranorte.example')
    await page.getByTestId('invite-submit').click()
    const link = await page.getByTestId('invitation-url').inputValue()

    const guest = await browser.newContext()
    const g = await guest.newPage()
    await g.goto(link)
    await expect(g.getByTestId('invitation-summary')).toContainText('invitada@fibranorte.example')
    await expect(g.getByTestId('invitation-card')).toContainText('Te invitaron a Fibra Norte')
    // El token sale de la barra de direcciones en cuanto se lee.
    expect(new URL(g.url()).hash).toBe('')
    expect(g.url()).not.toContain('token')
    expect(await seriousViolations(g)).toEqual([])

    await g.getByTestId('invitation-name').fill('Invitada Prueba')
    await g.getByLabel('Contraseña', { exact: true }).fill('corta')
    await expect(g.getByText('Debe tener al menos 12 caracteres.')).toBeVisible()
    await expect(g.getByTestId('invitation-submit')).toBeDisabled()
    await g.getByLabel('Contraseña', { exact: true }).fill(NEW_PASSWORD)
    await g.getByLabel('Repite la contraseña').fill(`${NEW_PASSWORD}x`)
    await expect(g.getByText('Las contraseñas no coinciden.')).toBeVisible()
    await g.getByLabel('Repite la contraseña').fill(NEW_PASSWORD)
    await g.getByTestId('invitation-submit').click()

    await expect(g).toHaveURL(/\/login$/)
    await expect(g.getByTestId('login-notice')).toContainText('Cuenta creada')
    await expect(g.getByLabel('Correo electrónico')).toHaveValue('invitada@fibranorte.example')
    expect(await seriousViolations(g)).toEqual([])

    await g.getByLabel('Contraseña', { exact: true }).fill(NEW_PASSWORD)
    await g.getByTestId('login-submit').click()
    await expect(g).toHaveURL(/\/t\/fibra-norte$/)
    await guest.close()
  })

  test('cuenta existente: "Aceptar invitación" y al login con esa cuenta', async ({ page }) => {
    const token = mockToken({
      e: 'noc@fibranorte.example',
      t: '01926b3e-0000-7000-8000-000000000002',
      u: '01926b3e-1111-7000-8000-000000000002',
      r: [{ role_id: '0192a000-0000-7000-8000-000000000004', role_key: 'noc', scope: 'tenant' }],
      x: Date.now() + 86_400_000,
      k: 'e2e-existing-account-0000',
    })
    await page.goto(`/invitation#token=${token}`)
    await expect(page.getByTestId('invitation-card')).toContainText('Te invitaron a Valle Conecta')
    await expect(page.getByTestId('invitation-form')).toHaveCount(0)
    await page.getByTestId('invitation-accept').click()
    await expect(page).toHaveURL(/\/login$/)
    await expect(page.getByTestId('login-notice')).toContainText('Invitación aceptada')
    await expect(page.getByTestId('login-notice')).toContainText('noc@fibranorte.example')
    await fillCredentials(page, 'noc@fibranorte.example')
    await expect(page).toHaveURL(/\/t\/fibra-norte$/)
    await page.getByTestId('tenant-switcher').click()
    await expect(page.getByRole('menuitemcheckbox', { name: 'Valle Conecta' })).toBeVisible()
  })

  test('enlace no válido o caducado: lo dice y aconseja pedir otro', async ({ page }) => {
    await page.goto(`/invitation#token=${'x'.repeat(48)}`)
    await expect(page.getByTestId('invitation-invalid')).toContainText(
      'no es válido, ya se usó o ha caducado',
    )
    await expect(page.getByTestId('invitation-invalid')).toContainText('enlace nuevo')
    expect(page.url()).not.toContain('token')
    expect(await seriousViolations(page)).toEqual([])

    const expired = mockToken({
      e: 'alguien@fibranorte.example',
      t: '01926b3e-0000-7000-8000-000000000001',
      u: '0195b100-0000-7000-8000-000000000999',
      r: [],
      x: Date.now() - 1000,
      k: 'e2e-expired-0000000000000',
    })
    // Solo cambia el fragmento: se sale antes para que la página se cargue de nuevo.
    await page.goto('about:blank')
    await page.goto(`/invitation#token=${expired}`)
    await expect(page.getByTestId('invitation-invalid')).toBeVisible()
  })

  test('sin token: mismo aviso', async ({ page }) => {
    await page.goto('/invitation')
    await expect(page.getByTestId('invitation-invalid')).toBeVisible()
  })
})
