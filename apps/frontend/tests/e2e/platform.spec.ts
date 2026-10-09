import { expect, test } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, seriousViolations } from './helpers'

/**
 * I1-31 · Consola de plataforma mínima (+ D19 acceso, D20 fuentes de reputación) y canales
 * de notificación del ISP (D13, D17). `pnpm e2e --grep @platform`.
 */

test.describe('consola de plataforma @platform', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('sin destino remoto: aviso permanente en Almacenamiento y en Estado del sistema', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto('/platform/storage')
    await expect(page.getByTestId('no-remote-copy')).toContainText('Sin copia remota configurada')
    await expect(page.getByTestId('no-remote-copy')).toContainText('si el disco falla')
    await page.goto('/platform/system')
    await expect(page.getByTestId('no-remote-copy')).toContainText('Sin copia remota configurada')
    await expect(page.getByTestId('system-components')).toContainText('remote_storage')
    expect(await seriousViolations(page)).toEqual([])
  })

  test('acceso solo por IP (D19): aviso y huella del certificado', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/platform/system')
    const access = page.getByTestId('installation-access')
    await expect(access.getByTestId('access-mode')).toHaveText('Solo IP del servidor')
    await expect(access.getByTestId('access-warning').first()).toContainText('Acceso solo por IP')
    await expect(access.getByTestId('cert-fingerprint')).toContainText('SHA256:')
  })

  test('acceso con dominio: sin avisos ni huella manual', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.accessMode', 'domain'))
    await loginAsAdmin(page)
    await page.goto('/platform/system')
    const access = page.getByTestId('installation-access')
    await expect(access.getByTestId('access-mode')).toHaveText('Dominio propio')
    await expect(access.getByTestId('access-warning')).toHaveCount(0)
    await expect(access.getByTestId('cert-fingerprint')).toHaveCount(0)
  })

  test('disco local ≥ 85 %: aviso en Almacenamiento y en Estado del sistema', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.diskRatio', '0.91'))
    await loginAsAdmin(page)
    await page.goto('/platform/system')
    await expect(page.getByTestId('disk-usage-notice')).toContainText('Disco local al 91 %')
    await page.goto('/platform/storage')
    await expect(page.getByTestId('disk-usage-notice')).toBeVisible()
  })

  test('disco local por debajo del 85 %: sin aviso', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/platform/system')
    await expect(page.getByTestId('system-components')).toBeVisible()
    await expect(page.getByTestId('disk-usage-notice')).toHaveCount(0)
  })

  test('WireGuard: cada hub con endpoint, rango y ocupación', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/platform/wireguard')
    const hub = page.getByTestId('wg-hub').first()
    await expect(hub.getByTestId('wg-occupancy')).toHaveText(/\d+ de 65[.\s\u202f]534 direcciones/)
    await expect(hub).toContainText('10.255.0.0/16')
    await expect(hub).toContainText(':51820')
  })

  test('ISP de la instalación con sus conteos', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto('/platform/isps')
    await expect(page.getByTestId('platform-isps').locator('li')).toHaveCount(3)
    await expect(page.getByTestId('platform-isps')).toContainText('Valle Conecta')
  })

  test('fuentes de reputación (D20): alta de una lista personalizada con validación', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto('/platform/reputation')
    await expect(page.locator('[data-source="abuse_ch_feodo"]')).toContainText('Catálogo base')
    await page.getByTestId('add-source').click()
    await page.getByTestId('source-name').fill('Lista del CERT')
    await page.getByTestId('source-key').fill('cert_local')
    await page.getByTestId('source-url').fill('https://10.0.0.5/lista.txt')
    await page.getByTestId('source-submit').click()
    await expect(page.getByText('Confirma que puedes usar esta lista')).toBeVisible()
    await page.getByTestId('source-terms').click()
    await page.getByTestId('source-submit').click()
    await expect(page.getByText('Usa una URL https pública')).toBeVisible()
    await page.getByTestId('source-url').fill('https://cert.example.org/lista.txt')
    await page.getByTestId('source-submit').click()
    const created = page.locator('[data-source="cert_local"]')
    await expect(created).toContainText('Personalizada')
    await expect(created.getByTestId('source-status')).toHaveText('Pendiente')
  })

  test('un usuario que no es superadmin recibe "No encontrado" en /platform/*', async ({
    page,
  }) => {
    await loginAsNoc(page)
    for (const path of [
      '/platform/system',
      '/platform/wireguard',
      '/platform/storage',
      '/platform/reputation',
    ]) {
      await page.goto(path)
      await expect(page.getByTestId('error-page')).toContainText('No encontrado')
    }
  })
})

test.describe('canales de notificación @platform', () => {
  test('LibreNMS por API (D17): credenciales write-only y prueba de conexión', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await loginAsAdmin(page)
    await page.goto('/t/fibra-norte/admin/notifications')
    await expect(
      page.locator('[data-channel="Guardia NOC por correo"]').getByTestId('channel-status'),
    ).toHaveText('Funciona')
    // Telegram sin credenciales: la prueba lo explica.
    const telegram = page.locator('[data-channel="Grupo de seguridad (Telegram)"]')
    await telegram.getByTestId('test-connection').click()
    await expect(telegram.getByTestId('test-result')).toContainText('Faltan las credenciales')

    await page.getByTestId('add-channel').click()
    await page.getByTestId('channel-kind').getByText('LibreNMS').click()
    await page.getByTestId('channel-name').fill('LibreNMS del NOC')
    await page.getByTestId('channel-base-url').fill('https://librenms.fibranorte.example')
    await page.getByTestId('channel-password').fill('s3creta')
    await page.getByTestId('channel-submit').click()
    const libre = page.locator('[data-channel="LibreNMS del NOC"]')
    await expect(libre.getByTestId('channel-status')).toHaveText('Sin probar')
    await expect(libre.getByTestId('channel-credentials')).toContainText('no se muestran')
    await expect(page.getByText('s3creta')).toHaveCount(0)
    await libre.getByTestId('test-connection').click()
    await expect(libre.getByTestId('test-result')).toContainText('Conexión correcta')
    await expect(libre.getByTestId('channel-status')).toHaveText('Funciona')
  })
})
