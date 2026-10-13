import { expect, test, type Page } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, PASSWORD, seriousViolations, TOTP } from './helpers'

/**
 * Usuarios de plataforma (consola de plataforma): lista con estado, 2FA, roles de plataforma y
 * membresías; bloquear / activar y roles de plataforma con re-autenticación reciente; siempre
 * queda un superadministrador activo. `pnpm e2e --grep @platform-users`.
 */

const PAGE = '/platform/users'

async function openUsers(page: Page) {
  await page.goto(PAGE)
  await expect(page.getByTestId('platform-users-table')).toBeVisible()
}

const rowOf = (page: Page, email: string) =>
  page.getByTestId('platform-users-table').locator('tr', { hasText: email })

async function action(page: Page, email: string, name: string) {
  await rowOf(page, email).getByTestId('platform-user-actions').click()
  await page.getByRole('menuitem', { name }).click()
}

test.describe('usuarios de plataforma @platform-users', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('lista con estado, 2FA, roles de plataforma y membresías; búsqueda', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/platform/isps')
    await page.getByTestId('main-nav').getByRole('link', { name: 'Usuarios de plataforma' }).click()
    await expect(page).toHaveURL(/\/platform\/users$/)
    const table = page.getByTestId('platform-users-table')
    for (const header of ['Usuario', 'Estado', 'Roles de plataforma', 'ISP y roles']) {
      await expect(table.getByRole('columnheader', { name: header })).toBeVisible()
    }
    const ana = rowOf(page, 'ana.ruiz@')
    await expect(ana).toContainText('(tú)')
    await expect(ana).toContainText('Superadministrador')
    await expect(ana).toContainText('fibra-norte')
    await expect(ana).toContainText('tenant_admin')
    await expect(rowOf(page, 'pablo.ortega@')).toContainText('Bloqueado')
    await expect(rowOf(page, 'marta.gomez@')).toContainText('Bloqueado temporalmente')
    await expect(rowOf(page, 'pedro.salas@')).toContainText('Pendiente')
    await expect(rowOf(page, 'noc@fibranorte')).toContainText('Sin activar')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('platform-user-search').fill('carlos')
    await page.getByTestId('platform-user-search').press('Enter')
    await expect(page.getByTestId('platform-users-count')).toHaveText('Mostrando 1 usuario')
    await expect(rowOf(page, 'carlos.mendez@')).toContainText('Operador de plataforma')
    expect(page.url()).not.toContain('carlos')
  })

  test('no se ofrece bloquearse a uno mismo', async ({ page }) => {
    await loginAsAdmin(page)
    await openUsers(page)
    await rowOf(page, 'ana.ruiz@').getByTestId('platform-user-actions').click()
    await expect(page.getByRole('menuitem', { name: 'Roles de plataforma' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Bloquear' })).toHaveCount(0)
  })

  test('bloquear y activar con re-autenticación', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.reauthRequired', '1'))
    await loginAsAdmin(page)
    await openUsers(page)

    await action(page, 'carlos.mendez@', 'Bloquear')
    const confirm = page.getByRole('dialog', { name: '¿Bloquear a Carlos Méndez?' })
    await expect(confirm).toContainText('sus sesiones abiertas se cierran al momento')
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('platform-status-confirm').click()

    const reauth = page.getByRole('dialog', { name: 'Confirma tu identidad' })
    await expect(reauth).toContainText('usuarios de plataforma')
    await page.getByTestId('reauth-password').fill(PASSWORD)
    await page.getByTestId('reauth-code').fill(TOTP)
    await page.getByTestId('reauth-submit').click()
    await expect(
      page.getByText('Carlos Méndez está bloqueado y sus sesiones se han cerrado', { exact: true }),
    ).toBeVisible()
    await expect(rowOf(page, 'carlos.mendez@').getByTestId('platform-user-status')).toHaveText(
      'Bloqueado',
    )

    // Re-autenticación reciente: activar ya no la pide.
    await action(page, 'carlos.mendez@', 'Activar')
    await expect(page.getByRole('dialog', { name: '¿Activar a Carlos Méndez?' })).toBeVisible()
    await page.getByTestId('platform-status-confirm').click()
    await expect(
      page.getByText('Carlos Méndez puede volver a iniciar sesión', { exact: true }),
    ).toBeVisible()
    await expect(rowOf(page, 'carlos.mendez@').getByTestId('platform-user-status')).toHaveText(
      'Activo',
    )
  })

  test('cambiar los roles de plataforma', async ({ page }) => {
    await loginAsAdmin(page)
    await openUsers(page)
    await action(page, 'sofia.herrera@', 'Roles de plataforma')
    const form = page.getByTestId('platform-roles-form')
    await expect(page.getByTestId('platform-roles-totp')).toContainText(
      'Necesita la verificación en dos pasos',
    )
    await expect(form).toContainText('Lee la auditoría de plataforma')
    await expect(
      page.getByRole('checkbox', { name: 'Auditor de plataforma', exact: true }),
    ).toBeChecked()
    await page.getByRole('checkbox', { name: 'Operador de plataforma', exact: true }).click()
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('platform-roles-save').click()
    await expect(
      page.getByText('Roles de plataforma de Sofía Herrera actualizados', { exact: true }),
    ).toBeVisible()
    const roles = rowOf(page, 'sofia.herrera@').getByTestId('platform-user-roles')
    await expect(roles).toContainText('Operador de plataforma')
    await expect(roles).toContainText('Auditor de plataforma')
  })

  test('siempre queda un superadministrador activo (LAST_PLATFORM_ADMIN)', async ({ page }) => {
    await loginAsAdmin(page)
    await openUsers(page)
    await action(page, 'ana.ruiz@', 'Roles de plataforma')
    await page.getByRole('checkbox', { name: 'Superadministrador', exact: true }).click()
    await expect(page.getByTestId('platform-roles-form')).toContainText(
      'Te estás quitando el rol de superadministrador',
    )
    await page.getByTestId('platform-roles-save').click()
    await expect(page.getByTestId('platform-roles-error')).toHaveText(
      'Debe quedar al menos un superadministrador activo',
    )
    await page.getByRole('button', { name: 'Cancelar' }).click()
    await expect(rowOf(page, 'ana.ruiz@')).toContainText('Superadministrador')
  })

  test('editar nombre y correo; correo en uso', async ({ page }) => {
    await loginAsAdmin(page)
    await openUsers(page)
    await action(page, 'carlos.mendez@', 'Editar')
    const dialog = page.getByRole('dialog', { name: 'Editar a Carlos Méndez' })
    await expect(dialog).toContainText('usuario para iniciar sesión')
    await page.getByTestId('platform-user-edit-name').fill('Carlos Méndez Rojas')
    await page.getByTestId('platform-user-edit-email').fill('carlos.rojas@horus.example')
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('platform-user-edit-save').click()
    await expect(
      page.getByText('Datos de Carlos Méndez Rojas actualizados', { exact: true }),
    ).toBeVisible()
    await expect(rowOf(page, 'carlos.rojas@horus.example')).toContainText('Carlos Méndez Rojas')
    await expect(rowOf(page, 'carlos.mendez@')).toHaveCount(0)

    await action(page, 'sofia.herrera@', 'Editar')
    await page.getByTestId('platform-user-edit-email').fill('ana.ruiz@fibranorte.example')
    await page.getByTestId('platform-user-edit-save').click()
    await expect(page.getByTestId('platform-user-edit-form')).toContainText(
      'Ya existe una cuenta con ese correo.',
    )
  })

  test('restablecer contraseña: enlace de un solo uso', async ({ page }) => {
    await loginAsAdmin(page)
    await openUsers(page)
    await action(page, 'sofia.herrera@', 'Restablecer contraseña')
    const confirm = page.getByRole('dialog', {
      name: '¿Restablecer la contraseña de Sofía Herrera?',
    })
    await expect(confirm).toContainText('válido 30 minutos')
    await page.getByTestId('platform-reset-confirm').click()
    const panel = page.getByTestId('reset-link-panel')
    await expect(panel).toBeVisible()
    const url = await page.getByTestId('reset-link-url').inputValue()
    expect(url).toMatch(/\/reset-password#token=[\w-]{32,}$/)
    await expect(page.getByTestId('reset-link-expires')).not.toBeEmpty()
    await expect(page.getByTestId('reset-link-email-note')).toContainText(
      'No hay correo configurado',
    )
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('platform-reset-close').click()
    await expect(panel).toHaveCount(0)
  })

  test('eliminar una cuenta con re-autenticación; nunca la propia', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.reauthRequired', '1'))
    await loginAsAdmin(page)
    await openUsers(page)
    await rowOf(page, 'ana.ruiz@').getByTestId('platform-user-actions').click()
    await expect(page.getByRole('menuitem', { name: 'Eliminar cuenta' })).toBeDisabled()
    await page.keyboard.press('Escape')

    await action(page, 'pablo.ortega@', 'Eliminar cuenta')
    const confirm = page.getByRole('dialog', { name: '¿Eliminar la cuenta de Pablo Ortega?' })
    await expect(confirm).toContainText('Se cierran todas sus sesiones')
    await expect(confirm).toContainText('La auditoría conserva lo que hizo')
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('platform-delete-confirm').click()

    const reauth = page.getByRole('dialog', { name: 'Confirma tu identidad' })
    await expect(reauth).toContainText('usuarios de plataforma')
    await page.getByTestId('reauth-password').fill(PASSWORD)
    await page.getByTestId('reauth-code').fill(TOTP)
    await page.getByTestId('reauth-submit').click()
    await expect(
      page.getByText('La cuenta de Pablo Ortega se ha eliminado', { exact: true }),
    ).toBeVisible()
    await expect(rowOf(page, 'pablo.ortega@')).toHaveCount(0)
  })

  test('sin permisos de plataforma la sección no existe', async ({ page }) => {
    await loginAsNoc(page)
    await page.goto(PAGE)
    await expect(page.getByTestId('error-page')).toContainText('No encontrado')
  })
})
