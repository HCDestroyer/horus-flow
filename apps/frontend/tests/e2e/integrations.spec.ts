import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/**
 * Integraciones de ecosistema (ADR-0030) contra la API simulada: código de conexión para
 * ArgosISP (se muestra una vez), conector ArgosISP con token de solo escritura, identidad
 * externa en la ficha del cliente y consulta 360 por IP sin la IP en la URL.
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
    await expect(modal.getByTestId('ip360-external')).not.toContainText('no está conectado')
    await expect(modal.getByTestId('ip360-company')).toContainText(/Fibra (Sur|Norte)/)
    const { pathname, search, hash } = new URL(page.url())
    expect(pathname + search + hash).not.toMatch(/\d+\.\d+\.\d+\.\d+/)
  })
})
