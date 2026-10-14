import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/**
 * Administración › Integración LibreNMS (D17): crear el canal con token de API, instrucciones
 * (syslog y transportes por defecto), conectar/sincronizar, objetos con sus ids de LibreNMS,
 * prueba de extremo a extremo y la IP de gestión + modo de cada router.
 * `pnpm e2e --grep @librenms`.
 */
const PAGE = '/t/fibra-norte/admin/librenms'

test.describe('integración con LibreNMS @librenms', () => {
  test('asistente: canal, sincronizar, objetos, prueba y routers', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await loginAsAdmin(page)
    await page.goto(PAGE)

    await expect(page.locator('[data-step="prepare"]')).toContainText(
      'lnms config:set enable_syslog true',
    )
    await expect(page.locator('[data-step="prepare"]')).toContainText('Default Alert')

    // 1. Canal LibreNMS con token de API (write-only).
    await page.getByTestId('librenms-new-channel').click()
    await page.getByTestId('librenms-name').fill('LibreNMS del NOC')
    await page.getByTestId('librenms-base-url').fill('https://librenms.fibranorte.example')
    await page.getByTestId('librenms-token').fill('token-de-api-de-librenms-0001')
    await page.getByTestId('librenms-save-channel').click()
    await expect(page.locator('[data-step="channel"]')).toContainText('con token de API')
    await expect(page.getByText('token-de-api-de-librenms-0001')).toHaveCount(0)
    await page.getByTestId('librenms-connection-test').click()
    await expect(page.getByTestId('librenms-connection-result')).toContainText('Conexión correcta')

    // Router: IP de gestión para LibreNMS (la del túnel no es alcanzable).
    const router = page.getByTestId('librenms-routers').locator('[data-router]').first()
    await router.getByTestId('router-mgmt-ip').fill('192.0.2.10')
    await router.getByTestId('router-save').click()
    await expect(router).toContainText('En LibreNMS')

    // 3. Conectar con LibreNMS.
    await expect(page.getByTestId('librenms-status')).toHaveText('Sin conectar')
    await page.getByTestId('librenms-sync').click()
    await expect(page.getByTestId('librenms-status')).toHaveText('Sincronizado')
    const objects = page.getByTestId('librenms-objects')
    await expect(objects.locator('[data-object="Horus Flow"]')).toContainText('#')
    await expect(objects.locator('[data-object="Horus · Routers"]')).toContainText(
      'Creado por Horus',
    )
    await expect(objects.locator('[data-object="Horus · Evento de seguridad Horus"]')).toHaveCount(
      2,
    )
    await expect(router).toContainText('LibreNMS #')
    expect(await seriousViolations(page)).toEqual([])

    // 4. Prueba de extremo a extremo.
    await page.getByTestId('librenms-test').click()
    const result = page.getByTestId('librenms-test-result')
    await expect(result).toContainText('La integración funciona')
    await expect(result).toContainText('Línea guardada en el syslog de LibreNMS')
    await expect(result).toContainText('HORUS INFO test')

    // SNMPv3 de Horus: exige el consentimiento explícito.
    await router.getByTestId('router-mode').click()
    await page.getByRole('option', { name: 'SNMPv3 de Horus (con consentimiento)' }).click()
    await router.getByTestId('router-save').click()
    await expect(router).toContainText('Confirme que LibreNMS usará')
    await router.getByTestId('router-consent').click()
    await router.getByTestId('router-save').click()
    await expect(router).not.toContainText('Confirme que LibreNMS usará')

    // Borrar el dispositivo creado por Horus (acción explícita).
    await router.getByTestId('router-delete-device').click()
    await page.getByTestId('router-delete-confirm').click()
    await expect(router).not.toContainText('LibreNMS #')
  })

  test('la prueba indica el paso exacto que falla (syslog desactivado)', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await loginAsAdmin(page)
    await page.goto(PAGE)
    await page.getByTestId('librenms-new-channel').click()
    await page.getByTestId('librenms-name').fill('LibreNMS sin syslog')
    await page.getByTestId('librenms-base-url').fill('https://nosyslog.fibranorte.example')
    await page.getByTestId('librenms-token').fill('token-de-api-de-librenms-0002')
    await page.getByTestId('librenms-save-channel').click()
    await page.getByTestId('librenms-sync').click()
    await expect(page.getByTestId('librenms-status')).toHaveText('Sincronizado')
    await page.getByTestId('librenms-test').click()
    const result = page.getByTestId('librenms-test-result')
    await expect(result).toContainText('Falló en: Línea guardada en el syslog de LibreNMS')
    await expect(result).toContainText('enable_syslog')
  })
})
