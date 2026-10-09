import { expect, test } from '@playwright/test'
import { loginAsAdmin, loginAsNoc, seriousViolations } from './helpers'

/**
 * I1-18 · Seguridad: hallazgos y detalle con evidencia. `pnpm e2e --grep @security`.
 */

const LIST = '/t/fibra-norte/security/findings'

test.describe('hallazgos @security', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.findingEveryMs', '0'))
  })

  test('lista: severidad, resumen, cliente, nodo, tipo, confianza, estado y última vez, por severidad', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(LIST)
    const table = page.getByTestId('findings-table')
    await expect(table).toBeVisible()
    for (const h of [
      'Severidad',
      'Hallazgo',
      'Cliente',
      'Nodo',
      'Confianza',
      'Estado',
      'Última vez',
    ]) {
      await expect(table.getByRole('columnheader', { name: h })).toBeVisible()
    }
    const severities = await table
      .locator('[data-severity]')
      .evaluateAll((els) => els.map((e) => e.getAttribute('data-severity')))
    const rank = { critical: 4, high: 3, medium: 2, low: 1 } as Record<string, number>
    const ranks = severities.map((s) => rank[s ?? ''] ?? 0)
    expect(ranks).toEqual([...ranks].sort((a, b) => b - a))
    await expect(page.getByTestId('findings-kpis')).toContainText('Nuevos en 24 h')
    expect(await seriousViolations(page)).toEqual([])
  })

  test('detalle: narrativa, ≥ 2 razones, línea de tiempo, acciones y comandos con deshacer (D11)', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(LIST)
    await page.getByTestId('finding-link').first().click()
    await expect(page.getByTestId('finding-why')).toContainText('Por qué lo marcamos')
    expect(await page.getByTestId('finding-reasons').locator('li').count()).toBeGreaterThanOrEqual(
      2,
    )
    await expect(page.getByTestId('finding-timeline')).toBeVisible()
    for (const name of ['Reconocer', 'Resolver', 'Falso positivo']) {
      await expect(page.getByTestId('finding-actions').getByRole('button', { name })).toBeVisible()
    }
    const actions = page.getByTestId('recommended-actions')
    await expect(actions).toContainText('Horus no ejecuta nada en tus routers')
    await expect(actions.getByText('Para deshacerlo').first()).toBeVisible()
    await expect(actions.locator('pre').first()).toBeVisible()
    // Ningún botón actúa sobre el router: solo copiar.
    await expect(
      actions.getByRole('button', { name: /aplicar en el router|ejecutar/i }),
    ).toHaveCount(0)
    await expect(page.getByTestId('evidence-flows')).toBeVisible()
    await page.getByTestId('evidence-flows').click()
    await expect(page.getByTestId('evidence-flows-table')).toBeVisible()
  })

  test('reconocer y falso positivo con comentario obligatorio que explica qué hará Horus', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(`${LIST}?state=open`)
    await page.getByTestId('finding-link').first().click()
    await page.getByTestId('ack').click()
    await expect(page.getByTestId('finding-header')).toContainText('Reconocido')
    await page.getByTestId('false-positive').click()
    await expect(page.getByTestId('fp-explanation')).toContainText('Qué hará Horus con esto')
    await page.getByTestId('transition-submit').click()
    await expect(page.getByText('Explica por qué es un falso positivo')).toBeVisible()
    await page.getByTestId('transition-comment').fill('Es el servidor de copias del cliente')
    await page.getByTestId('transition-submit').click()
    await expect(page.getByTestId('finding-resolution')).toContainText('falso positivo')
    await expect(page.getByTestId('finding-actions')).toHaveCount(0)
  })

  test('sin security.evidence.read no aparece "Ver flujos de evidencia"', async ({ page }) => {
    await loginAsNoc(page)
    await page.goto(LIST)
    await page.getByTestId('finding-link').first().click()
    await expect(page.getByTestId('finding-why')).toBeVisible()
    await expect(page.getByTestId('evidence-flows')).toHaveCount(0)
    // El NOC no gestiona hallazgos: sin acciones.
    await expect(page.getByTestId('finding-actions')).toHaveCount(0)
  })

  test('un hallazgo nuevo por tiempo real entra arriba sin mover el scroll y se anuncia', async ({
    page,
  }) => {
    await page.addInitScript(() => window.localStorage.setItem('horus.mock.findingEveryMs', '4000'))
    await loginAsAdmin(page)
    await page.goto(LIST)
    const rows = page.getByTestId('findings-table').locator('tbody tr')
    await expect(rows.first()).toBeVisible()
    const before = await rows.count()
    // Leyendo más abajo: la fila que se mira no debe moverse al llegar el nuevo.
    const id = await rows.nth(12).getAttribute('data-finding-id')
    const watched = page.locator(`tr[data-finding-id="${id}"]`)
    await watched.scrollIntoViewIfNeeded()
    const y0 = (await watched.boundingBox())!.y
    await expect(page.locator('tr.finding-fresh')).toHaveCount(1, { timeout: 15_000 })
    expect(await rows.count()).toBe(before + 1)
    await expect(rows.first()).toHaveClass(/finding-fresh/)
    const y1 = (await watched.boundingBox())!.y
    expect(Math.abs(y1 - y0)).toBeLessThan(4)
    await expect(page.getByTestId('findings-live')).toHaveText(/Hallazgos nuevos: \d/, {
      timeout: 35_000,
    })
  })
})
