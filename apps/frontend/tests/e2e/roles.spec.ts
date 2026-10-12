import { expect, test, type Page } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, seriousViolations } from './helpers'

/**
 * Roles y permisos del ISP (I1): roles de sistema de solo lectura, roles propios (crear,
 * editar con If-Match, eliminar), matriz de permisos y asignación desde Usuarios y roles.
 * `pnpm e2e --grep @roles`.
 */

const PAGE = '/t/fibra-norte/admin/roles'

async function openRoles(page: Page) {
  await page.goto(PAGE)
  await expect(page.getByTestId('system-roles')).toBeVisible()
}

const card = (page: Page, key: string) =>
  page.locator(`[data-testid="role-card"][data-role="${key}"]`)

async function roleAction(page: Page, key: string, action: string) {
  await card(page, key).getByTestId('role-actions').click()
  await page.getByRole('menuitem', { name: action }).click()
}

/** Crea un rol propio desde "Nuevo rol" copiando los permisos de otro rol. */
async function createRole(page: Page, name: string, copyFrom: string) {
  await page.getByTestId('role-create-open').click()
  await page.getByTestId('role-name').fill(name)
  await page.getByTestId('role-copy').click()
  await page.getByRole('option', { name: copyFrom }).click()
  await page.getByTestId('role-save').click()
  await expect(page.getByText(`Rol «${name}» creado`, { exact: true })).toBeVisible()
}

test.describe('roles y permisos @roles', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('roles de sistema de solo lectura y sección en la navegación', async ({ page }) => {
    await loginAsAdmin(page)
    await page.getByTestId('main-nav').getByRole('link', { name: 'Roles y permisos' }).click()
    await expect(page).toHaveURL(/\/admin\/roles$/)
    const admin = card(page, 'tenant_admin')
    await expect(admin).toContainText('Administrador del ISP')
    await expect(admin.getByTestId('role-system-badge')).toHaveText('Sistema')
    await expect(page.getByTestId('custom-roles-empty')).toContainText(
      'Todavía no hay roles propios',
    )
    expect(await seriousViolations(page)).toEqual([])

    await card(page, 'noc').getByTestId('role-actions').click()
    await expect(page.getByRole('menuitem', { name: 'Ver permisos' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Duplicar' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Editar' })).toHaveCount(0)
    await expect(page.getByRole('menuitem', { name: 'Eliminar' })).toHaveCount(0)
    await page.getByRole('menuitem', { name: 'Ver permisos' }).click()
    const detail = page.getByTestId('role-detail')
    await expect(page.getByTestId('role-read-only')).toContainText('solo lectura')
    await expect(detail).toContainText('Ver routers')
    await expect(detail).not.toContainText('Lista y ficha de clientes')
    expect(await seriousViolations(page)).toEqual([])

    // "Duplicar" abre "Nuevo rol" con los permisos y un nombre de partida.
    await page.getByRole('button', { name: 'Duplicar' }).click()
    await expect(page.getByTestId('role-name')).toHaveValue('Operador NOC (copia)')
    await expect(page.getByTestId('role-key')).toHaveValue('operador_noc_copia')
    await expect(page.getByRole('checkbox', { name: 'Ver routers', exact: true })).toBeChecked()
  })

  test('crear un rol propio, verlo en la matriz, asignarlo y eliminarlo', async ({ page }) => {
    await loginAsAdmin(page)
    await openRoles(page)

    await page.getByTestId('role-create-open').click()
    await page.getByTestId('role-name').fill('Técnico de campo')
    // Clave derivada del nombre: minúsculas, sin acentos, espacios → _.
    await expect(page.getByTestId('role-key')).toHaveValue('tecnico_de_campo')
    await page.getByTestId('role-description').fill('Instala y revisa routers en los nodos')
    await page.getByTestId('role-copy').click()
    await page.getByRole('option', { name: 'Operador NOC · Sistema' }).click()
    await expect(page.getByRole('checkbox', { name: 'Ver routers', exact: true })).toBeChecked()
    await page.getByRole('checkbox', { name: 'Registrar routers', exact: true }).click()
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('role-save').click()
    await expect(page.getByText('Rol «Técnico de campo» creado', { exact: true })).toBeVisible()
    const created = card(page, 'tecnico_de_campo')
    await expect(created).toContainText('Propio')
    await expect(created.getByTestId('role-counts')).toHaveText('12 permisos · sin asignaciones')

    // Matriz: columna del rol nuevo con sus permisos.
    await page.getByRole('tab', { name: 'Matriz de permisos' }).click()
    await expect(page).toHaveURL(/view=matrix/)
    const matrix = page.getByTestId('matrix-table')
    await expect(matrix.locator('[data-role-col="tecnico_de_campo"]')).toContainText(
      'Técnico de campo',
    )
    await expect(
      matrix.locator('[data-permission="devices.create"] [data-role="tecnico_de_campo"]'),
    ).toHaveText('Sí')
    await expect(
      matrix.locator('[data-permission="customers.read"] [data-role="tecnico_de_campo"]'),
    ).toHaveText('No')
    await expect(matrix.locator('[data-area="devices"]')).toContainText('Routers')
    expect(await seriousViolations(page)).toEqual([])

    // Asignarlo en Usuarios y roles (el selector de roles incluye los propios).
    await page.getByTestId('main-nav').getByRole('link', { name: 'Usuarios y roles' }).click()
    const row = page.getByTestId('members-table').locator('tr', { hasText: 'marta.gomez@' })
    await row.getByTestId('member-actions').click()
    await page.getByRole('menuitem', { name: 'Cambiar roles' }).click()
    await page.getByTestId('role-select').first().click()
    await page.getByRole('option', { name: 'Técnico de campo' }).click()
    await expect(page.getByTestId('roles-manage-link')).toHaveText('Gestionar roles')
    await page.getByTestId('roles-save').click()
    await expect(row).toContainText('Técnico de campo')

    // En uso: no se puede eliminar.
    await page.getByTestId('manage-roles').click()
    await expect(page).toHaveURL(/\/admin\/roles$/)
    await expect(created.getByTestId('role-counts')).toHaveText('12 permisos · 1 asignación')
    await roleAction(page, 'tecnico_de_campo', 'Eliminar')
    await expect(page.getByTestId('role-delete-count')).toHaveText('Está asignado a 1 miembro.')
    await page.getByTestId('role-delete-confirm').click()
    await expect(page.getByTestId('role-delete-error')).toContainText(
      'No se puede eliminar: está asignado a 1 miembro',
    )
    await expect(
      page.getByTestId('role-delete-error').getByRole('link', { name: 'Ir a Usuarios y roles' }),
    ).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])
    await page.getByRole('button', { name: 'Cancelar' }).click()

    // Uno sin asignaciones sí.
    await createRole(page, 'Temporal', 'Solo lectura · Sistema')
    await roleAction(page, 'temporal', 'Eliminar')
    await expect(page.getByTestId('role-delete-count')).toHaveText('Nadie lo tiene asignado.')
    await page.getByTestId('role-delete-confirm').click()
    await expect(page.getByText('Rol «Temporal» eliminado', { exact: true })).toBeVisible()
    await expect(card(page, 'temporal')).toHaveCount(0)
    await expect(created).toBeVisible()
  })

  test('editar con If-Match: si otra persona lo cambió (412) se carga la versión actual', async ({
    page,
  }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.roleConflict', '1'))
    await loginAsAdmin(page)
    await openRoles(page)
    await createRole(page, 'Soporte N1', 'Operador NOC · Sistema')

    await roleAction(page, 'soporte_n1', 'Editar')
    await expect(page.getByTestId('role-key')).toBeDisabled()
    await page.getByTestId('role-name').fill('Soporte nivel 1')
    await page.getByTestId('role-save').click()
    await expect(page.getByTestId('role-conflict')).toContainText(
      'Otra persona cambió este rol mientras lo editabas',
    )
    await expect(page.getByTestId('role-description')).toHaveValue(
      'Actualizado por otro administrador',
    )
    await expect(page.getByTestId('role-name')).toHaveValue('Soporte N1')
    await page.getByTestId('role-name').fill('Soporte nivel 1')
    await page.getByTestId('role-save').click()
    await expect(page.getByText('Rol «Soporte nivel 1» actualizado', { exact: true })).toBeVisible()
    await expect(card(page, 'soporte_n1')).toContainText('Soporte nivel 1')
    await expect(card(page, 'soporte_n1')).toContainText('Actualizado por otro administrador')
  })

  test('los roles propios sobreviven a recargar la página', async ({ page }) => {
    await loginAsAdmin(page)
    await openRoles(page)
    await createRole(page, 'Facturación', 'Analista · Sistema')
    await page.reload()
    await expect(card(page, 'facturacion')).toContainText('Facturación')
  })

  test('móvil: la matriz muestra un rol cada vez con áreas plegables', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await loginAsAdmin(page)
    await page.goto(`${PAGE}?view=matrix`)
    await expect(page.getByTestId('matrix-table')).toBeHidden()
    const mobile = page.getByTestId('matrix-mobile')
    await expect(mobile).toBeVisible()
    await page.getByTestId('matrix-role-select').click()
    await page.getByRole('option', { name: 'Operador NOC · Sistema' }).click()
    await mobile.getByRole('button', { name: /Routers/ }).click()
    await expect(mobile.locator('[data-permission="devices.read"]')).toContainText('Sí')
    await expect(mobile.locator('[data-permission="devices.create"]')).toContainText('No')
    expect(await seriousViolations(page)).toEqual([])
  })

  test('sin roles.read la sección no existe para el operador NOC', async ({ page }) => {
    await loginAsNoc(page)
    await expect(
      page.getByTestId('main-nav').getByRole('link', { name: 'Roles y permisos' }),
    ).toHaveCount(0)
    await page.goto(PAGE)
    await expect(page.getByTestId('error-page')).toBeVisible()
  })
})
