import { expect, test, type Page } from '@playwright/test'
import { PASSWORD, TOTP, loginAsAdmin, seriousViolations } from './helpers'

/**
 * Nodos y routers: alta guiada (Nodo → Router → Script de conexión → Credenciales), edición
 * con If-Match (412), bajas (SITE_NOT_EMPTY) y confirmación de prefijos.
 * `pnpm e2e --grep @nodes`.
 *
 * La API simulada guarda los cambios en memoria de la pestaña: tras el primer `goto` se navega
 * solo dentro de la SPA.
 */

const T = '/t/fibra-norte'
const CENTRO = `${T}/nodes/0192e111-0000-7000-8000-000000000101`
const NORTE = `${T}/nodes/0192e111-0000-7000-8000-000000000102`

async function confirmIdentity(page: Page) {
  const reauth = page.getByRole('dialog', { name: 'Confirma tu identidad' })
  await expect(reauth).toBeVisible()
  await page.getByTestId('reauth-password').fill(PASSWORD)
  await page.getByTestId('reauth-code').fill(TOTP)
  await page.getByTestId('reauth-submit').click()
  await expect(reauth).toHaveCount(0)
}

async function noHorizontalScroll(page: Page) {
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(0)
}

test.describe('nodos y routers @nodes', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('ISP vacío: nodo → router → script → credenciales (con re-auth) → editar → bajas', async ({
    page,
  }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem('horus.mock.emptyInventory', '1')
      // El router se queda "pendiente" durante la prueba (sin avance simulado del asistente).
      window.localStorage.setItem('horus.mock.onboardingStepMs', '3600000')
    })
    await loginAsAdmin(page)
    await page.goto(`${T}/nodes`)

    // Vacío con llamada a la acción clara.
    const empty = page.getByTestId('nodes-empty')
    await expect(empty).toContainText('Aún no hay nodos')
    await expect(empty.getByTestId('setup-steps')).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])

    // 1. Nodo.
    await empty.getByRole('button', { name: 'Crear el primer nodo' }).click()
    const siteModal = page.getByRole('dialog', { name: 'Nuevo nodo' })
    await expect(siteModal).toBeVisible()
    await page.getByTestId('site-submit').click()
    await expect(siteModal).toContainText('Escribe el nombre del nodo.')
    await page.getByTestId('site-name').fill('Nodo Puerto')
    await page.getByTestId('site-code').fill('PTO')
    await page.getByTestId('site-address').fill('Av. del Puerto 12')
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('site-submit').click()
    await expect(page.getByText('Nodo Nodo Puerto creado', { exact: true })).toBeVisible()

    // 2. Router: el alta se ofrece en seguida.
    const routerModal = page.getByRole('dialog', { name: 'Agregar router' })
    await expect(routerModal).toBeVisible()
    await page.getByTestId('router-name').fill('rt puerto')
    await page.getByTestId('router-version').fill('6.48')
    await page.getByTestId('router-submit').click()
    await expect(routerModal).toContainText('Sin espacios')
    await expect(routerModal).toContainText('Horus solo admite RouterOS 7.x')
    await page.getByTestId('router-name').fill('rt-puerto')
    await page.getByTestId('router-version').fill('7.11')
    await expect(page.getByTestId('router-version-warning')).toContainText('anterior a 7.12')
    await page.getByTestId('router-version').fill('7.16.2')
    await expect(page.getByTestId('router-version-warning')).toHaveCount(0)
    await page.getByTestId('router-model').fill('CCR2004-1G-12S+2XS')
    await page.getByTestId('router-submit').click()

    // 3. Siguiente paso guiado: el script de conexión WireGuard.
    const created = page.getByTestId('router-created')
    await expect(created).toBeVisible()
    await expect(created.locator('[data-step="script"]')).toHaveAttribute('data-state', 'current')
    await page.getByTestId('router-continue').click()
    await expect(page).toHaveURL(/\/routers\/[^/]+\/connection$/)
    await expect(page.getByTestId('router-exporter-state')).toContainText('Pendiente')
    await page.getByTestId('generate-script').click()
    await expect(page.getByTestId('provisioning-script')).toContainText('/interface wireguard add')
    await expect(page.getByTestId('provisioning-script')).toContainText('rt-puerto')

    // 4. Credenciales (x-reauth): se pide confirmar identidad una vez y se reintenta solo.
    await page.evaluate(() => localStorage.setItem('horus.mock.reauthRequired', '1'))
    await page.getByRole('tab', { name: 'Credenciales' }).click()
    await expect(page).toHaveURL(/\/credentials$/)
    const panel = page.getByTestId('router-credentials')
    await expect(panel).toContainText('Horus solo lee del router')
    await expect(page.getByTestId('credentials-snippet')).toContainText('!write')
    await expect(
      page.getByTestId('credentials-snippet').getByRole('button', { name: 'Copiar' }),
    ).toBeVisible()
    const snmp = page.getByTestId('credential-snmp')
    await expect(snmp.getByTestId('credential-status')).toHaveText('Sin configurar')
    await page.getByTestId('snmp-username').fill('horus-snmp')
    await page.getByTestId('snmp-auth-password').fill('corta')
    await page.getByTestId('credential-save-snmp').click()
    await expect(snmp).toContainText('Mínimo 8 caracteres.')
    await page.getByTestId('snmp-auth-password').fill('clave-auth-2026')
    await page.getByTestId('snmp-priv-password').fill('clave-priv-2026')
    // Mostrar/ocultar.
    await expect(page.getByTestId('snmp-priv-password')).toHaveAttribute('type', 'password')
    await snmp.getByRole('button', { name: 'Mostrar contraseña' }).last().click()
    await expect(page.getByTestId('snmp-priv-password')).toHaveAttribute('type', 'text')
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('credential-save-snmp').click()
    await confirmIdentity(page)
    await expect(page.getByText('Credencial SNMPv3 guardada', { exact: true })).toBeVisible()
    await expect(snmp.getByTestId('credential-status')).toHaveText('Configurada')
    // El secreto no se queda en el formulario.
    await expect(page.getByTestId('snmp-auth-password')).toHaveValue('')

    const apiCard = page.getByTestId('credential-routeros_api')
    await page.getByTestId('api-username').fill('horus-ro')
    await page.getByTestId('api-password').fill('una-clave-de-solo-lectura')
    await page.getByTestId('api-fingerprint').fill('no-es-una-huella')
    await page.getByTestId('credential-save-routeros_api').click()
    await expect(apiCard).toContainText('64 dígitos hexadecimales')
    await page.getByTestId('api-fingerprint').fill('ab'.repeat(32))
    await page.getByTestId('credential-save-routeros_api').click()
    // Re-autenticación reciente (5 min): no se vuelve a pedir.
    await expect(
      page.getByText('Credencial API de RouterOS guardada', { exact: true }),
    ).toBeVisible()
    await expect(apiCard.getByTestId('credential-status')).toHaveText('Configurada')

    // 5. Editar el router.
    await page.getByTestId('edit-router').click()
    const edit = page.getByRole('dialog', { name: 'Editar router' })
    await expect(edit).toBeVisible()
    await expect(page.getByTestId('router-name')).toHaveValue('rt-puerto')
    await page.getByTestId('router-display-name').fill('Borde Puerto')
    await page.getByTestId('router-submit').click()
    await expect(page.getByText('Router actualizado', { exact: true })).toBeVisible()
    await expect(page.getByTestId('router-header')).toContainText('Borde Puerto')

    // 6. Eliminar el router (se recuerda desinstalarlo antes) y luego el nodo.
    await page.getByTestId('router-more').click()
    await page.getByRole('menuitem', { name: 'Eliminar router' }).click()
    const delRouter = page.getByRole('dialog', { name: '¿Eliminar rt-puerto?' })
    await expect(delRouter).toBeVisible()
    await page.getByTestId('delete-confirm').click()
    await expect(page.getByText('Router rt-puerto eliminado', { exact: true })).toBeVisible()
    await expect(page).toHaveURL(/\/nodes\/[^/]+$/)
    await expect(page.getByTestId('node-routers-empty')).toContainText(
      'Este nodo aún no tiene routers',
    )

    await page.getByTestId('node-more').click()
    await page.getByRole('menuitem', { name: 'Eliminar nodo' }).click()
    await expect(page.getByRole('dialog', { name: '¿Eliminar Nodo Puerto?' })).toBeVisible()
    await page.getByTestId('delete-confirm').click()
    await expect(page.getByText('Nodo Nodo Puerto eliminado', { exact: true })).toBeVisible()
    await expect(page).toHaveURL(/\/nodes$/)
    await expect(page.getByTestId('nodes-empty')).toBeVisible()
  })

  test('nodo con routers: no se puede eliminar (SITE_NOT_EMPTY) y se explica por qué', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(CENTRO)
    await page.getByTestId('node-more').click()
    await page.getByRole('menuitem', { name: 'Eliminar nodo' }).click()
    await page.getByTestId('delete-confirm').click()
    const blocked = page.getByTestId('delete-blocked')
    await expect(blocked).toContainText('Este nodo todavía tiene routers')
    await expect(blocked).toContainText('script de desinstalación')
    await expect(blocked.getByRole('link', { name: 'rt-centro' })).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])
  })

  test('412: otra sesión cambió el nodo; se carga la versión actual y se guarda de nuevo', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(CENTRO)
    await page.evaluate(() => localStorage.setItem('horus.mock.conflictOnce', '1'))
    await page.getByTestId('edit-node').click()
    await page.getByTestId('site-name').fill('Nodo Centro Histórico')
    await page.getByTestId('site-submit').click()
    await expect(page.getByTestId('site-conflict')).toContainText('Otra persona hizo cambios')
    await expect(page.getByTestId('site-address')).toHaveValue(/editado en otra sesión/)
    await expect(page.getByTestId('site-name')).toHaveValue('Nodo Centro')
    await page.getByTestId('site-name').fill('Nodo Centro Histórico')
    await page.getByTestId('site-submit').click()
    await expect(page.getByText('Nodo actualizado', { exact: true })).toBeVisible()
    await expect(page.getByTestId('app-navbar')).toContainText('Nodo Centro Histórico')
    await expect(page.getByTestId('node-facts')).toContainText('editado en otra sesión')
  })

  test('segundo router principal: ROUTER_PRIMARY_EXISTS y registrarlo como secundario', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(CENTRO)
    await page.getByTestId('add-router').click()
    const modal = page.getByRole('dialog', { name: 'Agregar router' })
    await expect(modal).toContainText('ya tiene un router principal')
    await page.getByTestId('router-name').fill('rt-centro-2')
    await page.getByTestId('router-primary').click()
    await page.getByTestId('router-submit').click()
    await expect(page.getByTestId('router-primary-exists')).toContainText(
      'Este nodo ya tiene un router principal',
    )
    await page.getByTestId('router-save-secondary').click()
    await expect(page.getByTestId('router-created')).toBeVisible()
    await page.getByTestId('router-later').click()
    await expect(page.locator('[data-router="rt-centro-2"]')).toContainText('Secundario')
  })

  test('prefijo sin confirmar: Confirmar', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(NORTE)
    const row = page.getByTestId('prefixes-list').locator('[data-prefix="10.20.48.0/24"]')
    await expect(row.getByTestId('prefix-unconfirmed')).toHaveText('Sin confirmar')
    await row.getByTestId('prefix-confirm').click()
    await expect(page.getByText('Prefijo 10.20.48.0/24 confirmado', { exact: true })).toBeVisible()
    await expect(row.getByTestId('prefix-unconfirmed')).toHaveCount(0)
  })

  test('móvil: vacío, alta de nodo y ficha sin scroll horizontal', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.emptyInventory', '1'))
    await loginAsAdmin(page)
    await page.goto(`${T}/nodes`)
    await expect(page.getByTestId('nodes-empty')).toBeVisible()
    await noHorizontalScroll(page)
    await page.getByRole('button', { name: 'Crear el primer nodo' }).click()
    await page.getByTestId('site-name').fill('Nodo Móvil')
    await noHorizontalScroll(page)
    await page.getByTestId('site-submit').click()
    const routerModal = page.getByRole('dialog', { name: 'Agregar router' })
    await expect(routerModal).toBeVisible()
    await noHorizontalScroll(page)
    expect(await seriousViolations(page)).toEqual([])
    await routerModal.getByRole('button', { name: 'Cancelar' }).click()
    const card = page.locator('[data-node="Nodo Móvil"]')
    await expect(card.getByTestId('node-no-routers')).toContainText('Sin routers todavía.')
    await expect(card.getByTestId('node-add-router')).toBeVisible()
    await card.getByRole('link', { name: 'Nodo Móvil' }).click()
    await expect(page.getByTestId('node-routers-empty')).toBeVisible()
    await noHorizontalScroll(page)
  })
})
