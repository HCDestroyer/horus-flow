import { expect, test, type Page } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, PASSWORD, seriousViolations, TOTP } from './helpers'

/**
 * Usuarios y roles del ISP (I1): lista, invitar con enlace para compartir o copiar, generar
 * un enlace nuevo, quitar y cambiar roles. `pnpm e2e --grep @members`.
 */

const PAGE = '/t/fibra-norte/admin/users'

async function openMembers(page: Page) {
  await page.goto(PAGE)
  await expect(page.getByTestId('members-table')).toBeVisible()
}

function rowOf(page: Page, email: string) {
  return page.getByTestId('members-table').locator('tr', { hasText: email })
}

async function invite(page: Page, email: string) {
  await page.getByTestId('invite-open').click()
  await page.getByTestId('invite-email').fill(email)
  await page.getByTestId('invite-submit').click()
}

test.describe('usuarios y roles @members', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('lista con estado, roles y 2FA; búsqueda y filtro de estado', async ({ page }) => {
    await loginAsAdmin(page)
    await openMembers(page)
    const table = page.getByTestId('members-table')
    for (const header of ['Miembro', 'Estado', 'Roles', 'Verificación en dos pasos']) {
      await expect(table.getByRole('columnheader', { name: header })).toBeVisible()
    }
    await expect(rowOf(page, 'ana.ruiz@fibranorte.example')).toContainText('Administrador del ISP')
    await expect(rowOf(page, 'pedro.salas@')).toContainText('Invitación pendiente')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('member-search').fill('marta')
    await page.getByTestId('member-search').press('Enter')
    await expect(page.getByTestId('members-count')).toContainText('Mostrando 1 ')
    await expect(rowOf(page, 'marta.gomez@')).toBeVisible()
    // La búsqueda (correos) no entra en la URL.
    expect(page.url()).not.toContain('marta')

    await page.getByTestId('member-search').fill('')
    await page.getByTestId('member-search').press('Enter')
    await page.getByTestId('member-status').click()
    await page.getByRole('option', { name: 'Invitaciones pendientes' }).click()
    await expect(page).toHaveURL(/status=invited/)
    await expect(page.getByTestId('member-status-badge')).toHaveText(['Invitación pendiente'])
  })

  test('invitar un correo nuevo: enlace, caducidad, aviso de correo y copiar', async ({
    page,
    context,
  }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    // Navegador sin Web Share API: "Compartir" no aparece.
    await page.addInitScript(() => {
      delete (Navigator.prototype as { share?: unknown }).share
      delete (Navigator.prototype as { canShare?: unknown }).canShare
    })
    await loginAsAdmin(page)
    await openMembers(page)

    await invite(page, 'nueva.tecnica@fibranorte.example')
    const panel = page.getByTestId('invitation-panel')
    await expect(panel).toBeVisible()
    await expect(page.getByRole('dialog')).toContainText(
      'Invitación para nueva.tecnica@fibranorte.example',
    )
    const url = await page.getByTestId('invitation-url').inputValue()
    expect(url).toMatch(/\/invitation#token=[\w-]{32,}$/)
    await expect(page.getByTestId('invitation-url')).toHaveAttribute('readonly', '')
    await expect(page.getByTestId('invitation-expires')).not.toBeEmpty()
    await expect(page.getByTestId('invitation-email-note')).toHaveText(
      'No hay correo configurado: comparte el enlace.',
    )
    await expect(panel).toContainText('solo se muestra ahora')
    await expect(page.getByTestId('invitation-share')).toHaveCount(0)
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('invitation-copy').click()
    await expect(page.getByText('Enlace copiado', { exact: true })).toBeVisible()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(url)

    await page.getByTestId('invitation-close').click()
    await expect(rowOf(page, 'nueva.tecnica@fibranorte.example')).toContainText(
      'Invitación pendiente',
    )
  })

  test('con Web Share API: "Compartir" abre la hoja nativa con el enlace', async ({ page }) => {
    await page.addInitScript(() => {
      const w = window as unknown as { __shared: unknown[] }
      w.__shared = []
      window.localStorage.setItem('horus.mock.smtp', '1')
      Object.defineProperty(Navigator.prototype, 'share', {
        configurable: true,
        value: (data: unknown) => {
          w.__shared.push(data)
          return Promise.resolve()
        },
      })
      Object.defineProperty(Navigator.prototype, 'canShare', {
        configurable: true,
        value: () => true,
      })
    })
    await loginAsAdmin(page)
    await openMembers(page)
    await invite(page, 'compartir@fibranorte.example')
    // Con correo configurado (SMTP), se dice a quién se envió.
    await expect(page.getByTestId('invitation-email-note')).toHaveText(
      'Se envió por correo a compartir@fibranorte.example.',
    )
    const url = await page.getByTestId('invitation-url').inputValue()
    await page.getByTestId('invitation-share').click()
    const shared = await page.evaluate(
      () => (window as unknown as { __shared: { url: string; title: string }[] }).__shared,
    )
    expect(shared).toHaveLength(1)
    expect(shared[0]!.url).toBe(url)
    expect(shared[0]!.title).toContain('Fibra Norte')
  })

  test('cancelar la hoja de compartir no muestra ningún error', async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(Navigator.prototype, 'share', {
        configurable: true,
        value: () => Promise.reject(new DOMException('cancel', 'AbortError')),
      })
    })
    await loginAsAdmin(page)
    await openMembers(page)
    await invite(page, 'cancelar@fibranorte.example')
    await page.getByTestId('invitation-share').click()
    await expect(page.getByText('No se pudo abrir el menú de compartir')).toHaveCount(0)
  })

  test('una cuenta que ya existe queda añadida sin enlace', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/t/valle-conecta/admin/users')
    await expect(page.getByTestId('members-table')).toBeVisible()
    await invite(page, 'noc@fibranorte.example')
    await expect(
      page.getByText('noc@fibranorte.example ya tenía cuenta: añadido al ISP', { exact: true }),
    ).toBeVisible()
    await expect(page.getByTestId('invitation-panel')).toHaveCount(0)
    await expect(rowOf(page, 'noc@fibranorte.example')).toContainText('Activo')
  })

  test('correo no válido: no se envía', async ({ page }) => {
    await loginAsAdmin(page)
    await openMembers(page)
    await invite(page, 'no-es-un-correo')
    await expect(page.getByText('Escribe un correo válido')).toBeVisible()
    await expect(page.getByTestId('invitation-panel')).toHaveCount(0)
  })

  test('generar un enlace nuevo para un invitado', async ({ page }) => {
    await loginAsAdmin(page)
    await openMembers(page)
    await rowOf(page, 'pedro.salas@').getByTestId('member-actions').click()
    await page.getByRole('menuitem', { name: 'Generar enlace' }).click()
    await expect(page.getByTestId('invitation-panel')).toBeVisible()
    const first = await page.getByTestId('invitation-url').inputValue()
    expect(first).toContain('/invitation#token=')
    await page.getByTestId('invitation-close').click()

    await rowOf(page, 'pedro.salas@').getByTestId('member-actions').click()
    await page.getByRole('menuitem', { name: 'Generar enlace' }).click()
    await expect(page.getByTestId('invitation-url')).not.toHaveValue(first)
  })

  test('quitar un miembro pide confirmación', async ({ page }) => {
    await loginAsAdmin(page)
    await openMembers(page)
    await rowOf(page, 'marta.gomez@').getByTestId('member-actions').click()
    await page.getByRole('menuitem', { name: 'Quitar del ISP' }).click()
    await expect(page.getByRole('dialog')).toContainText('¿Quitar a Marta Gómez?')
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('remove-confirm').click()
    await expect(
      page.getByText('Marta Gómez ya no tiene acceso al ISP', { exact: true }),
    ).toBeVisible()
    await expect(rowOf(page, 'marta.gomez@')).toHaveCount(0)
  })

  test('no se ofrece quitarse a uno mismo', async ({ page }) => {
    await loginAsAdmin(page)
    await openMembers(page)
    await rowOf(page, 'ana.ruiz@').getByTestId('member-actions').click()
    await expect(page.getByRole('menuitem', { name: 'Cambiar roles' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Quitar del ISP' })).toHaveCount(0)
  })

  test('cambiar roles y re-autenticación requerida', async ({ page }) => {
    await loginAsAdmin(page)
    await openMembers(page)
    await rowOf(page, 'jorge.diaz@').getByTestId('member-actions').click()
    await page.getByRole('menuitem', { name: 'Cambiar roles' }).click()
    await page.getByTestId('role-select').first().click()
    await page.getByRole('option', { name: 'Operador NOC' }).click()
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('roles-save').click()
    await expect(page.getByText('Roles de Jorge Díaz actualizados', { exact: true })).toBeVisible()
    await expect(rowOf(page, 'jorge.diaz@')).toContainText('Operador NOC')

    // El backend exige haberse identificado hace poco (x-reauth): se confirma la identidad en
    // un diálogo (sin cerrar la sesión) y el cambio se reintenta solo.
    await page.evaluate(() => localStorage.setItem('horus.mock.reauthRequired', '1'))
    await rowOf(page, 'jorge.diaz@').getByTestId('member-actions').click()
    await page.getByRole('menuitem', { name: 'Cambiar roles' }).click()
    await page.getByTestId('role-select').first().click()
    await page.getByRole('option', { name: 'Ingeniero de red' }).click()
    await page.getByTestId('roles-save').click()

    const reauth = page.getByRole('dialog', { name: 'Confirma tu identidad' })
    await expect(reauth).toContainText('cambiar roles')
    // Cancelar: no se guarda y se ofrece repetir la confirmación.
    await page.getByTestId('reauth-cancel').click()
    await expect(page.getByTestId('roles-reauth')).toContainText(
      'Confirma tu identidad para cambiar roles',
    )
    await page
      .getByTestId('roles-reauth')
      .getByRole('button', { name: 'Confirmar identidad' })
      .click()
    await expect(reauth).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('reauth-password').fill(PASSWORD)
    await page.getByTestId('reauth-code').fill(TOTP)
    await page.getByTestId('reauth-submit').click()
    await expect(reauth).toHaveCount(0)
    await expect(page.getByText('Roles de Jorge Díaz actualizados', { exact: true })).toBeVisible()
    await expect(rowOf(page, 'jorge.diaz@')).toContainText('Ingeniero de red')
    await expect(page).toHaveURL(/\/admin\/users/)
  })

  test('sin users.read la sección no existe para el operador NOC', async ({ page }) => {
    await loginAsNoc(page)
    await expect(
      page.getByTestId('main-nav').getByRole('link', { name: 'Usuarios y roles' }),
    ).toHaveCount(0)
    await page.goto(PAGE)
    await expect(page.getByTestId('error-page')).toBeVisible()
  })
})
