import { expect, test, type Page } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/**
 * Asistente de IA (docs/assist.md) contra la API simulada: configurar el proveedor en
 * Administración › Inteligencia artificial, analizar un hallazgo y un router, aviso cuando está
 * desactivado y error del proveedor. `pnpm e2e --grep @assist`.
 */
const ADMIN = '/t/fibra-norte/admin/ai'
const FINDINGS = '/t/fibra-norte/security/findings'
const ROUTER = '/t/fibra-norte/routers/0192e333-0000-7000-8000-000000000101'

async function openFirstFinding(page: Page) {
  await page.goto(FINDINGS)
  await page.getByTestId('finding-link').first().click()
  await expect(page.getByTestId('recommended-actions')).toBeVisible()
}

async function enableClaude(page: Page) {
  await page.goto(ADMIN)
  await page.getByTestId('assist-api-key').fill('sk-ant-prueba-123456')
  await page.getByTestId('assist-enabled').click()
  await page.getByTestId('assist-save').click()
  await expect(page.getByTestId('assist-state')).toHaveText('Activado')
}

test.describe('asistente de IA @assist', () => {
  test('desactivado: el hallazgo explica cómo activarlo', async ({ page }) => {
    await loginAsAdmin(page)
    await openFirstFinding(page)
    const box = page.getByTestId('assist-analysis')
    await expect(box).toBeVisible()
    await expect(page.getByTestId('assist-disabled')).toContainText('no está activado')
    await expect(page.getByTestId('assist-run')).toHaveCount(0)
    await page.getByTestId('assist-configure').click()
    await expect(page).toHaveURL(/\/admin\/ai$/)
    await expect(page.getByTestId('assist-state')).toHaveText('Desactivado')
  })

  test('configurar Claude, probar conexión y analizar un hallazgo', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await loginAsAdmin(page)
    await page.goto(ADMIN)
    await expect(page.getByText('Qué ve la IA')).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])

    // Activar Claude sin clave: validación del servidor.
    await page.getByTestId('assist-enabled').click()
    await page.getByTestId('assist-save').click()
    await expect(page.getByTestId('assist-settings')).toContainText('hace falta una clave de API')

    await page.getByTestId('assist-api-key').fill('sk-ant-prueba-123456')
    await page.getByTestId('assist-save').click()
    await expect(page.getByTestId('assist-state')).toHaveText('Activado')
    // La clave es write-only: el campo vuelve vacío y solo se indica que hay una guardada.
    await expect(page.getByTestId('assist-api-key')).toHaveValue('')
    await expect(page.getByTestId('assist-settings')).toContainText('Hay una clave guardada')

    await page.getByTestId('assist-test').click()
    await expect(page.getByTestId('assist-test-result')).toContainText('Conexión correcta')

    await openFirstFinding(page)
    await page.getByTestId('assist-run').click()
    await expect(page.getByTestId('assist-progress')).toBeVisible()
    const result = page.getByTestId('assist-result')
    await expect(result).toBeVisible()
    await expect(page.getByTestId('assist-disclaimer')).toContainText(
      'Generado por IA; revisa antes de aplicar. Horus no ejecuta nada en tus routers.',
    )
    await expect(result.getByRole('heading', { name: 'Qué está pasando' })).toBeVisible()
    await expect(result.getByRole('heading', { name: 'Para deshacer' })).toBeVisible()
    await expect(result).toContainText('Comando RouterOS sugerido')
    await expect(result).toContainText('Cliente 1 (10.20.x.x)')
    await expect(page.getByTestId('assist-meta')).toContainText('claude-opus-5-5')
    await expect(page.getByTestId('assist-grounding')).toHaveAttribute('data-status', 'verified')
    expect(await seriousViolations(page)).toEqual([])

    // La consulta queda en la auditoría.
    await page.goto(ADMIN)
    await expect(page.getByTestId('assist-audit-row').first()).toContainText('Análisis de hallazgo')

    // Historial: al volver al hallazgo se ve el análisis guardado sin gastar otra consulta.
    await openFirstFinding(page)
    await expect(page.getByTestId('assist-result')).toContainText('Qué está pasando')
    await expect(page.getByTestId('assist-run')).toHaveText('Volver a analizar')
    await expect(page.getByTestId('assist-history')).toHaveCount(0)
    await page.getByTestId('assist-run').click()
    await expect(page.getByTestId('assist-result')).toBeVisible()
    const items = page.getByTestId('assist-history-item')
    await expect(items).toHaveCount(2)
    await expect(page.getByTestId('assist-history')).toContainText('2 análisis guardados')
    await items.nth(1).click()
    await expect(items.nth(1)).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('assist-result')).toContainText('Qué está pasando')
  })

  test('diagnóstico de router y error del proveedor', async ({ page }) => {
    await loginAsAdmin(page)
    await enableClaude(page)
    await page.goto(ROUTER)
    await page.getByTestId('assist-run').click()
    await expect(page.getByTestId('assist-result')).toContainText('exportador del router')

    // Un proveedor que no responde se explica sin romper la página.
    await page.goto(ADMIN)
    const settings = page.getByTestId('assist-settings')
    const version = Number(await settings.getAttribute('data-version'))
    await page.getByTestId('assist-model').fill('modelo-fail')
    await page.getByTestId('assist-save').click()
    await expect(settings).toHaveAttribute('data-version', String(version + 1))
    await page.goto(ROUTER)
    await page.getByTestId('assist-run').click()
    await expect(page.getByTestId('assist-error')).toContainText(
      'No se pudo obtener respuesta del proveedor de IA',
    )
  })

  test('modelo local y token MCP', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(ADMIN)
    await page.getByTestId('assist-provider').click()
    await page.getByRole('option', { name: /compatible con OpenAI/ }).click()
    await expect(page.getByTestId('assist-model')).toHaveValue('llama3.1')
    await page.getByTestId('assist-enabled').click()
    await page.getByTestId('assist-save').click()
    await expect(page.getByTestId('assist-settings')).toContainText('Indica la URL del servidor')
    await page.getByTestId('assist-base-url').fill('http://10.0.0.5:11434')
    await page.getByTestId('assist-save').click()
    await expect(page.getByTestId('assist-state')).toHaveText('Activado')

    await page.getByTestId('assist-mcp-name').fill('Claude Desktop de prueba')
    await page.getByTestId('assist-mcp-create').click()
    await expect(page.getByTestId('assist-mcp-issued')).toContainText('no se volverá a mostrar')
    await expect(page.getByTestId('assist-mcp')).toContainText('hmcp_')
    await expect(page.getByTestId('assist-mcp')).toContainText('Claude Desktop de prueba')
  })
})
