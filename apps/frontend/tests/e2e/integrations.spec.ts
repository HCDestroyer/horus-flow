import { expect, test } from '@playwright/test'
import {
  ADMIN as ADMIN_EMAIL,
  fillCredentials,
  loginAsAdmin,
  seriousViolations,
  TOTP,
} from './helpers'

/**
 * Integraciones de ecosistema (ADR-0030) contra la API simulada: código de conexión para
 * ArgosISP (se muestra una vez), conector ArgosISP con token de solo escritura, identidad
 * externa en la ficha del cliente y consulta 360 por IP sin la IP en la URL; enlace de conexión
 * (contrato §1.4) y autologin desde ArgosISP con vinculación de cuentas (§1.5).
 * `pnpm e2e --grep @integrations`.
 */
const ADMIN = '/t/fibra-norte/admin/integrations'
const CLIENTS = '/t/fibra-norte/clients'

test.describe('integraciones @integrations', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await loginAsAdmin(page)
  })

  test('código de conexión ArgosISP y conector', async ({ page }) => {
    await page.goto(ADMIN)
    await expect(page.getByTestId('api-tokens')).toBeVisible()
    await expect(page.getByTestId('api-token-template')).toContainText(
      'Código de conexión para ArgosISP',
    )
    await page.getByTestId('api-token-create').click()
    await expect(page.getByTestId('api-token-issued')).toContainText('no se volverá a mostrar')
    await expect(page.getByText(/^hf_pat_/).first()).toBeVisible()
    await expect(page.getByTestId('api-token-list')).toContainText('ArgosISP')

    const card = page.getByTestId('argos-connector')
    await page.getByTestId('argos-base-url').fill('https://argos.isp.example/api/v1')
    await page.getByTestId('argos-token').fill('atk_prueba_123456')
    await page.getByTestId('argos-save').click()
    await expect(card).toContainText('/api/v1/integrations/argosisp/webhook/')
    // Token de solo escritura: el campo vuelve vacío y solo se indica que hay uno guardado.
    await expect(page.getByTestId('argos-token')).toHaveValue('')
    await page.getByTestId('argos-test').click()
    await expect(page.getByTestId('argos-test-result')).toContainText('Conexión correcta')
    await page.getByTestId('argos-rotate').click()
    await expect(card).toContainText('no se volverá a mostrar')
    expect(await seriousViolations(page)).toEqual([])
  })

  test('identidad de ArgosISP en la ficha y consulta 360 sin IP en la URL', async ({ page }) => {
    // Conector activo.
    await page.goto(ADMIN)
    await page.getByTestId('argos-base-url').fill('https://argos.isp.example/api/v1')
    await page.getByTestId('argos-token').fill('atk_prueba_123456')
    await page.getByTestId('argos-save').click()
    await expect(page.getByTestId('argos-sync')).toBeVisible()

    await page.goto(CLIENTS)
    await expect(page.getByTestId('clients-table')).toBeVisible()
    await page.getByTestId('client-address').first().click()
    await expect(page.getByTestId('client-external')).toBeVisible()
    await page.getByTestId('client-external-refresh').click()
    await expect(page.getByTestId('client-external-name')).not.toHaveText('—')
    // Multiempresa (contrato 1.2.0): la empresa del servicio con su NIT.
    await expect(page.getByTestId('client-external-company')).toContainText(/Fibra (Sur|Norte)/)
    await expect(page.getByTestId('client-external-company')).toContainText('NIT')

    await page.getByTestId('client-ip360').click()
    const modal = page.getByTestId('ip360')
    await expect(modal.getByTestId('ip360-customer')).toBeVisible()
    await expect(modal.getByTestId('ip360-consumption')).toContainText('30 días')
    // El router que lleva el tráfico del cliente (sus flujos recientes) y el último flujo del exportador.
    await expect(modal.getByTestId('ip360-router-source')).toContainText('flujos recientes')
    await expect(modal.getByTestId('ip360-last-flow')).toContainText('último flujo del router hace')
    await expect(modal.getByTestId('ip360-external')).not.toContainText('no está conectado')
    await expect(modal.getByTestId('ip360-company')).toContainText(/Fibra (Sur|Norte)/)
    const { pathname, search, hash } = new URL(page.url())
    expect(pathname + search + hash).not.toMatch(/\d+\.\d+\.\d+\.\d+/)

    // Ver en ArgosISP: Horus pide el ticket de servidor a servidor y abre la URL de un uso.
    await page.keyboard.press('Escape')
    await page
      .context()
      .route('https://panel.argos.example/**', (route) =>
        route.fulfill({ status: 200, contentType: 'text/html', body: '<title>ArgosISP</title>' }),
      )
    const popup = page.waitForEvent('popup')
    await page.getByTestId('client-external-open').click()
    const argos = await popup
    await argos.waitForURL(/panel\.argos\.example\/sso\/entrar#/)
  })

  test('enlace de conexión: generar con cuenta atrás y canjear el de Argos', async ({ page }) => {
    await page.goto(ADMIN)
    const panel = page.getByTestId('argos-link')
    await expect(panel).toBeVisible()
    await page.getByTestId('argos-link-generate').click()
    await expect(page.getByTestId('argos-link-countdown')).toContainText(/Caduca en 1[45]:\d\d/)
    await expect(page.getByTestId('argos-link-url')).toContainText('/conectar#')
    // Un enlace que no es de ArgosISP se señala antes de enviarlo.
    await page.getByTestId('argos-link-peer').fill('https://panel.argos.example/otra')
    await expect(panel).toContainText('No parece un enlace de conexión')
    await page
      .getByTestId('argos-link-peer')
      .fill(`https://panel.argos.example/conectar#${'Q'.repeat(43)}`)
    await page.getByTestId('argos-link-redeem').click()
    await expect(page.getByTestId('argos-link-redeemed')).toContainText('apagados')
    await expect(page.getByTestId('argos-connection')).toContainText(
      'Conectado con un enlace de ArgosISP',
    )
    await expect(page.getByTestId('argos-enabled')).not.toBeChecked()
    expect(await seriousViolations(page)).toEqual([])
  })
})

test.describe('autologin desde ArgosISP @integrations', () => {
  test('primer uso: vincular iniciando sesión; después entra con su 2FA', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await page.goto('/sso/entrar#demo-ticket-de-prueba-e2e1')
    await expect(page.getByTestId('sso-link-required')).toContainText('Ana López')
    // El ticket sale de la barra de direcciones.
    expect(new URL(page.url()).hash).toBe('')
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('sso-link-login').click()
    await fillCredentials(page, ADMIN_EMAIL)
    await page.getByLabel('Código de verificación').fill(TOTP)
    await page.getByTestId('mfa-submit').click()
    await expect(page).toHaveURL(/\/sso\/vincular$/)
    await expect(page.getByTestId('sso-link-external')).toContainText('Ana López')
    await page.getByTestId('sso-link-confirm').click()
    await expect(page).toHaveURL(/\/t\/fibra-norte\/clients\/[0-9a-f-]+$/)

    // El mismo ticket no sirve dos veces.
    await page.goto('/sso/entrar#demo-ticket-de-prueba-e2e1')
    await expect(page.getByTestId('sso-invalid')).toBeVisible()

    // Con la cuenta vinculada: segundo factor de Horus y directo al recurso (ArgosISP abre la
    // URL en una pestaña nueva: carga completa, no solo un cambio de fragmento).
    await page.goto('/account')
    await page.goto('/sso/entrar#demo-ticket-de-prueba-e2e2')
    await page.getByTestId('sso-mfa-code').fill(TOTP)
    await page.getByTestId('sso-mfa-submit').click()
    await expect(page).toHaveURL(/\/t\/fibra-norte\/clients\/[0-9a-f-]+$/)

    // Mi cuenta: ver y quitar la vinculación.
    await page.goto('/account')
    await expect(page.getByTestId('sso-card-row')).toContainText('Ana López')
    await page.getByTestId('sso-card-remove').click()
    await expect(page.getByTestId('sso-card-empty')).toBeVisible()
  })

  test('ticket caducado o mal formado', async ({ page }) => {
    await page.goto('/sso/entrar#vencido-ticket-de-prueba-01')
    await expect(page.getByTestId('sso-invalid')).toBeVisible()
    await page.goto('/sso/entrar')
    await expect(page.getByTestId('sso-invalid')).toBeVisible()
    await page.goto('/conectar#' + 'A'.repeat(43))
    await expect(page.getByTestId('connect-page')).toContainText('pégalo en ArgosISP')
    expect(new URL(page.url()).hash).toBe('')
  })
})
