import { expect, test } from '@playwright/test'
import { loginAsAdmin } from './helpers'

/**
 * Canales de notificación por categoría (seguridad, routers, infraestructura, exportador y
 * túnel), asistente de Telegram (crear bot con @BotFather, validar token, detectar chat con
 * bot propio o con código del bot de Horus), registro de envíos y umbrales de routers por ISP.
 * `pnpm e2e --grep @notifications`.
 */

test.describe('canales de notificación y umbrales @notifications', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await loginAsAdmin(page)
  })

  test('Telegram con bot propio: validar token, detectar chat y suscribir por categoría', async ({
    page,
  }) => {
    await page.goto('/t/fibra-norte/admin/notifications')
    // Las suscripciones se resumen por categoría.
    await expect(
      page.locator('[data-channel="Guardia NOC por correo"]').getByTestId('channel-subscription'),
    ).toContainText('Routers')

    await page.getByTestId('add-channel').click()
    await page.getByTestId('channel-kind').getByText('Telegram').click()
    await page.getByTestId('channel-name').fill('Alertas de red')
    await page.getByTestId('telegram-source').getByText('Bot propio').click()
    await expect(page.getByRole('link', { name: '@BotFather' })).toHaveAttribute(
      'href',
      'https://t.me/BotFather',
    )
    await page
      .getByTestId('channel-bot-token')
      .fill('7012345678:AAH-token-de-prueba-largo-0123456789')
    await page.getByTestId('telegram-validate').click()
    await expect(page.getByTestId('telegram-bot-ok')).toContainText('@noc_fibranorte_bot')
    await page.getByTestId('telegram-detect').click()
    await page.getByTestId('telegram-chats').getByText('Alertas NOC').click()
    await expect(page.getByTestId('channel-chat')).toHaveValue('-1002233445566')

    const infra = page.locator('[data-category="infrastructure"]')
    await infra.getByRole('checkbox').first().check()
    // Afinar: de Seguridad solo los hallazgos reabiertos.
    const security = page.locator('[data-category="security"]')
    await security.getByRole('checkbox').first().uncheck()
    await security.getByRole('button', { name: 'Tipos' }).click()
    await security.getByText('Hallazgo reabierto').click()
    await page.getByTestId('channel-submit').click()

    const channel = page.locator('[data-channel="Alertas de red"]')
    await expect(channel.getByTestId('channel-subscription')).toContainText('Infraestructura')
    await expect(channel.getByTestId('channel-subscription')).toContainText('Hallazgo reabierto')
    await expect(channel.getByTestId('channel-credentials')).toContainText('no se muestran')
    await expect(page.getByText('AAH-token-de-prueba')).toHaveCount(0)
    await channel.getByTestId('test-connection').click()
    await expect(channel.getByTestId('test-result')).toContainText('@horus_alertas_bot')
  })

  test('bot de Horus: código de un solo uso para detectar el chat', async ({ page }) => {
    await page.goto('/t/fibra-norte/admin/notifications')
    await page.getByTestId('add-channel').click()
    await page.getByTestId('channel-kind').getByText('Telegram').click()
    await expect(page.getByTestId('telegram-detect')).toBeDisabled()
    await page.getByTestId('telegram-create-code').click()
    const code = page.getByTestId('telegram-link-code')
    await expect(code).toContainText(/hf[a-z0-9]{10}/)
    await expect(code.getByRole('link', { name: '@horus_alertas_bot' })).toHaveAttribute(
      'href',
      /^https:\/\/t\.me\/horus_alertas_bot\?start=hf/,
    )
    await page.getByTestId('telegram-detect').click()
    await page.getByTestId('telegram-chats').getByText('NOC Fibra Norte').click()
    await expect(page.getByTestId('channel-chat')).toHaveValue('-1004455667788')
  })

  test('registro de envíos con severidad y filtro por estado', async ({ page }) => {
    await page.goto('/t/fibra-norte/admin/notifications')
    const log = page.getByTestId('deliveries')
    await expect(log).toContainText('Router «rt-norte-02» fuera de línea')
    await expect(log).toContainText('Router «rt-norte-02» recuperado')
    await expect(log).toContainText('Crítica')
    await log.getByTestId('deliveries-filter').click()
    await page.getByRole('option', { name: 'Fallidos' }).click()
    await expect(log).toContainText('Todavía no se ha enviado nada')
  })

  test('umbrales de routers por ISP: editar, validar y volver a los de la instalación', async ({
    page,
  }) => {
    await page.goto('/t/fibra-norte/routers')
    await page.getByTestId('open-thresholds').click()
    const modal = page.getByTestId('router-thresholds')
    await expect(page.getByTestId('router-thresholds-source')).toHaveText(
      'Umbrales de la instalación',
    )
    const cpuWarning = modal.locator(
      '[data-testid="threshold-cpu-warning"] input, input[data-testid="threshold-cpu-warning"]',
    )
    const cpuCritical = modal.locator(
      '[data-testid="threshold-cpu-critical"] input, input[data-testid="threshold-cpu-critical"]',
    )
    await cpuWarning.fill('97')
    await cpuWarning.blur()
    await page.getByTestId('router-thresholds-save').click()
    await expect(modal).toContainText('El aviso no puede superar al crítico')
    await cpuWarning.fill('70')
    await cpuCritical.fill('90')
    await cpuCritical.blur()
    await page.getByTestId('router-thresholds-save').click()
    await expect(modal).toBeHidden()
    await page.getByTestId('open-thresholds').click()
    await expect(page.getByTestId('router-thresholds-source')).toHaveText(
      'Umbrales propios del ISP',
    )
    await expect(cpuWarning).toHaveValue('70')
    await page.getByTestId('router-thresholds-reset').click()
    await expect(modal).toBeHidden()
  })
})
