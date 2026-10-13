import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/**
 * Administración › Detección (I1, lectura de umbrales): reglas vigentes con sus umbrales,
 * allowlist del ISP (añadir y quitar) y feeds de reputación. `pnpm e2e --grep @detection`.
 */
const PAGE = '/t/fibra-norte/admin/detection'

test.describe('detección del ISP @detection', () => {
  test('umbrales legibles, allowlist y feeds', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await loginAsAdmin(page)
    await page.goto(PAGE)

    const scan = page.locator('[data-detector="scanning"]')
    await expect(scan).toContainText('Escaneo saliente')
    await expect(scan).toContainText('SYN sin respuesta (mínimo)')
    await expect(scan).toContainText('70 %')
    await expect(scan).toContainText('5 min')
    await expect(page.locator('[data-detector="sustained_upload"]')).toContainText(
      'Ajustado para el ISP',
    )
    await expect(
      page.locator('[data-detector="sustained_upload"]').getByTestId('detector-state'),
    ).toHaveText('Desactivado')
    await expect(page.getByTestId('detection-sources')).toContainText('abuse.ch Feodo Tracker')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('allow-add').click()
    await page.getByTestId('allow-prefix').fill('203.0.113.0/24')
    await page.getByTestId('allow-reason').fill('Escáner de vulnerabilidades del NOC')
    await page.getByTestId('allow-submit').click()
    const entry = page.locator('[data-allow="203.0.113.0/24"]')
    await expect(entry).toContainText('Escáner de vulnerabilidades del NOC')

    await entry.getByTestId('allow-remove').click()
    await page.getByTestId('allow-remove-confirm').click()
    await expect(entry).toHaveCount(0)
  })
})
