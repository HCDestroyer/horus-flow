import { expect, test, type Page } from '@playwright/test'
import { fillCredentials, loginAsAdmin, PASSWORD, seriousViolations, TOTP } from './helpers'

/**
 * Alta de ISP desde la consola de plataforma (`POST /platform/tenants`) con su primer
 * administrador, y re-autenticación reciente en un diálogo (sin cerrar la sesión).
 * `pnpm e2e --grep @platform-isps`.
 */

const SUPERADMIN = 'admin@horus.example'
const SUPERADMIN_ID = '01926b3e-1111-7000-8000-000000000003'

/** Superadministrador que ya cambió la contraseña inicial y activó el TOTP (account.spec). */
async function loginAsSuperadmin(page: Page) {
  await page.addInitScript(
    ([key, value]) => window.sessionStorage.setItem(key!, value!),
    [
      `horus.mock.account.${SUPERADMIN_ID}`,
      JSON.stringify({ password: PASSWORD, mfa: true, mustChange: false }),
    ],
  )
  await page.goto('/login')
  await fillCredentials(page, SUPERADMIN)
  await page.getByLabel('Código de verificación').fill(TOTP)
  await page.getByTestId('mfa-submit').click()
  // Sin ningún ISP: entra en la consola de plataforma.
  await expect(page).toHaveURL(/\/platform\/isps$/)
}

async function openCreate(page: Page) {
  await page.getByTestId('tenant-create-open').click()
  await expect(page.getByTestId('tenant-form')).toBeVisible()
}

test.describe('alta de ISP @platform-isps', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('superadmin sin ISP: lo crea con su correo, entra e invita al equipo', async ({ page }) => {
    await page.addInitScript(() => {
      delete (Navigator.prototype as { share?: unknown }).share
    })
    await loginAsSuperadmin(page)
    await expect(page.getByTestId('platform-isps').locator('li')).toHaveCount(3)

    await openCreate(page)
    const dialog = page.getByRole('dialog')
    await expect(page.getByTestId('tenant-email')).toHaveValue(SUPERADMIN)
    await expect(dialog).toContainText(
      'Si pones tu propio correo, quedarás como administrador del ISP y podrás invitar al resto.',
    )
    await page.getByTestId('tenant-name').fill('Fibra Óptica del Lago')
    // Identificador derivado del nombre: minúsculas, sin acentos, espacios → guiones.
    await expect(page.getByTestId('tenant-slug')).toHaveValue('fibra-optica-del-lago')
    await expect(page.getByTestId('tenant-country')).toContainText('Guatemala')
    await expect(page.getByTestId('tenant-timezone')).toContainText('America/Guatemala')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('tenant-submit').click()
    await expect(
      page.getByText('ISP «Fibra Óptica del Lago» creado. Ya eres su administrador.', {
        exact: true,
      }),
    ).toBeVisible()
    await expect(page.getByTestId('tenant-created')).toBeVisible()
    // La lista se refresca y el ISP nuevo ya es un enlace (ahora es miembro).
    const card = page.locator('[data-isp="fibra-optica-del-lago"]')
    // (El diálogo sigue abierto: el fondo queda inerte, se busca el enlace por su destino.)
    await expect(card.locator('a[href="/t/fibra-optica-del-lago"]')).toHaveCount(1)

    await page.getByTestId('tenant-enter').click()
    await expect(page).toHaveURL(/\/t\/fibra-optica-del-lago$/)
    await page.getByTestId('main-nav').getByRole('link', { name: 'Usuarios y roles' }).click()
    await expect(page).toHaveURL(/\/t\/fibra-optica-del-lago\/admin\/users$/)
    const table = page.getByTestId('members-table')
    await expect(table.locator('tr', { hasText: SUPERADMIN })).toContainText(
      'Administrador del ISP',
    )

    await page.getByTestId('invite-open').click()
    await page.getByTestId('invite-email').fill('tecnica@lago.example')
    await page.getByTestId('invite-submit').click()
    await expect(page.getByTestId('invitation-panel')).toBeVisible()
    expect(await page.getByTestId('invitation-url').inputValue()).toMatch(
      /\/invitation#token=[\w-]{32,}$/,
    )
  })

  test('con el correo de otra persona: enlace de invitación para el primer administrador', async ({
    page,
    context,
    browser,
  }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    await page.addInitScript(() => {
      delete (Navigator.prototype as { share?: unknown }).share
      delete (Navigator.prototype as { canShare?: unknown }).canShare
    })
    await loginAsAdmin(page)
    await page.goto('/platform/isps')
    await openCreate(page)
    await page.getByTestId('tenant-name').fill('Cable Sur')
    await page.getByTestId('tenant-email').fill('jefa@cablesur.example')
    await page.getByTestId('tenant-submit').click()

    const panel = page.getByTestId('invitation-panel')
    await expect(panel).toBeVisible()
    await expect(page.getByRole('dialog')).toContainText('ISP «Cable Sur» creado')
    await expect(page.getByRole('dialog')).toContainText('jefa@cablesur.example')
    const url = await page.getByTestId('invitation-url').inputValue()
    expect(url).toMatch(/\/invitation#token=[\w-]{32,}$/)
    await expect(page.getByTestId('invitation-email-note')).toHaveText(
      'No hay correo configurado: comparte el enlace.',
    )
    await expect(page.getByTestId('invitation-share')).toHaveCount(0)
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('invitation-copy').click()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(url)

    await page.getByTestId('tenant-close').click()
    // Ana no es miembro del ISP nuevo: aparece en la lista, sin enlace.
    const card = page.locator('[data-isp="cable-sur"]')
    await expect(card).toContainText('Cable Sur')
    await expect(card.locator('a')).toHaveCount(0)

    // La invitada abre el enlace en su navegador (que no conoce el ISP recién creado).
    const guest = await browser.newContext()
    const g = await guest.newPage()
    await g.goto(url)
    await expect(g.getByTestId('invitation-card')).toContainText('Te invitaron a Cable Sur')
    await guest.close()
  })

  test('identificador no válido o repetido: error en el campo', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/platform/isps')
    await openCreate(page)
    await page.getByTestId('tenant-name').fill('Fibra Norte')
    await expect(page.getByTestId('tenant-slug')).toHaveValue('fibra-norte')

    await page.getByTestId('tenant-slug').fill('-mal')
    await page.getByTestId('tenant-submit').click()
    await expect(page.getByText('Usa solo minúsculas, números y guiones')).toBeVisible()

    await page.getByTestId('tenant-slug').fill('fibra-norte')
    await page.getByTestId('tenant-submit').click()
    await expect(
      page.getByText('Ya existe un ISP con este identificador. Elige otro.'),
    ).toBeVisible()
    await expect(page.getByTestId('tenant-form')).toBeVisible()
    // Editar el nombre ya no cambia un identificador escrito a mano.
    await page.getByTestId('tenant-slug').fill('fibra-norte-2')
    await page.getByTestId('tenant-name').fill('Fibra Norte Dos')
    await expect(page.getByTestId('tenant-slug')).toHaveValue('fibra-norte-2')
  })

  test('re-autenticación requerida: se confirma en un diálogo y el alta se reintenta sola', async ({
    page,
  }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.reauthRequired', '1'))
    await loginAsAdmin(page)
    await page.goto('/platform/isps')
    await openCreate(page)
    await page.getByTestId('tenant-name').fill('Red Volcán')
    await page.getByTestId('tenant-submit').click()

    const reauth = page.getByRole('dialog', { name: 'Confirma tu identidad' })
    await expect(reauth).toBeVisible()
    await expect(reauth).toContainText('dar de alta un ISP')
    await expect(page.getByTestId('reauth-code')).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('reauth-password').fill('incorrecta')
    await page.getByTestId('reauth-code').fill(TOTP)
    await page.getByTestId('reauth-submit').click()
    await expect(page.getByTestId('reauth-error')).toContainText(
      'La contraseña o el código no son correctos.',
    )
    // Sigue con sesión: un 401 al re-autenticarse no la cierra.
    await expect(page).toHaveURL(/\/platform\/isps$/)

    await page.getByTestId('reauth-password').fill(PASSWORD)
    await page.getByTestId('reauth-submit').click()
    await expect(reauth).toHaveCount(0)
    await expect(page.getByTestId('tenant-created')).toBeVisible()
    await expect(page.locator('[data-isp="red-volcan"]')).toBeVisible()
  })

  test('cancelar la re-autenticación: no se crea y se explica', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.reauthRequired', '1'))
    await loginAsAdmin(page)
    await page.goto('/platform/isps')
    await openCreate(page)
    await page.getByTestId('tenant-name').fill('Red Volcán')
    await page.getByTestId('tenant-submit').click()
    await page.getByTestId('reauth-cancel').click()
    await expect(page.getByTestId('tenant-error')).toContainText(
      'No se creó el ISP: confirma tu identidad para continuar.',
    )
    await expect(page.locator('[data-isp="red-volcan"]')).toHaveCount(0)
  })
})
