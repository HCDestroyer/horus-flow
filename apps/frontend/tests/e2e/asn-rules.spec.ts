import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/**
 * Administración › ASN propios: nombrar un ASN (con lo que dice hoy el dataset), validación del
 * servidor (ASN repetido), edición con un servicio del catálogo, búsqueda y borrado; el nombre
 * aparece en el desglose de Tráfico, que filtra por ASN o nombre y enlaza "Nombrar AS<n>".
 * `pnpm e2e --grep @asn-rules`.
 */
const PAGE = '/t/fibra-norte/admin/asn'
const TRAFFIC = '/t/fibra-norte/traffic?by=asns'

test.describe('ASN propios @asn-rules', () => {
  test('alta, validación, edición, búsqueda y borrado', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await loginAsAdmin(page)
    await page.goto(PAGE)
    await expect(page.getByTestId('asn-scope')).toContainText('Se aplican al tráfico nuevo')
    await expect(page.getByText('Todavía no nombraste ningún ASN')).toBeVisible()

    // Alta: el dataset no conoce el ASN; el nombre y la categoría los pone el ISP.
    await page.getByTestId('asn-add').click()
    await page.getByTestId('asn-input').fill('AS263237')
    await expect(page.getByTestId('asn-lookup-name')).toHaveText('sin nombre en el dataset público')
    await page.getByTestId('asn-submit').click()
    await expect(page.getByText('Escribe el nombre que verás en los tops.')).toBeVisible()
    await page.getByTestId('asn-name').fill('Fibra Andina')
    await page.getByTestId('asn-category').click()
    await page.getByRole('option', { name: 'CDN e infraestructura' }).click()
    await page.getByTestId('asn-note').fill('Tránsito regional')
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('asn-submit').click()
    const row = page.locator('[data-asn-rule="263237"]')
    await expect(row).toContainText('AS263237')
    await expect(row).toContainText('Fibra Andina')
    await expect(row).toContainText('CDN e infraestructura · Servicio propio con este nombre')
    await expect(row).toContainText('Tránsito regional')

    // Un ASN conocido propone su nombre del dataset; ASN repetido → error del servidor en el campo.
    await page.getByTestId('asn-add').click()
    await page.getByTestId('asn-input').fill('2906')
    await expect(page.getByTestId('asn-lookup')).toContainText('Netflix Streaming Services')
    await expect(page.getByTestId('asn-lookup')).toContainText('En el catálogo de Horus: Netflix')
    await expect(page.getByTestId('asn-name')).toHaveValue('Netflix Streaming Services')
    await page.getByTestId('asn-input').fill('263237')
    await expect(page.getByTestId('asn-exists')).toContainText('Fibra Andina')
    await page.getByTestId('asn-name').fill('Duplicado')
    await page.getByTestId('asn-category').click()
    await page.getByRole('option', { name: 'Navegación web' }).click()
    await page.getByTestId('asn-submit').click()
    await expect(
      page.getByText('Ya hay una regla para este ASN: edítala en la lista.'),
    ).toBeVisible()
    await page.getByRole('button', { name: 'Cancelar' }).click()

    // Edición: con un servicio del catálogo la categoría es la del servicio.
    await row.getByTestId('asn-edit').click()
    await expect(page.getByTestId('asn-input')).toBeDisabled()
    await page.getByTestId('asn-name').fill('Fibra Andina SA')
    await page.getByTestId('asn-service').click()
    await page.getByPlaceholder('Buscar servicio').fill('netf')
    await page.getByRole('option', { name: 'Netflix · Streaming de video' }).click()
    await expect(page.getByTestId('asn-category')).toContainText('Streaming de video')
    await expect(page.getByTestId('asn-category')).toBeDisabled()
    await page.getByTestId('asn-submit').click()
    await expect(row).toContainText('Fibra Andina SA')
    await expect(row).toContainText('Streaming de video · Servicio: Netflix')

    // Búsqueda en la lista.
    await page.getByTestId('asn-search').fill('AS9999')
    await expect(page.getByTestId('asn-no-match')).toBeVisible()
    await page.getByTestId('asn-search').fill('andina')
    await expect(row).toBeVisible()

    // Borrado.
    await row.getByTestId('asn-remove').click()
    await page.getByTestId('asn-remove-confirm').click()
    await expect(row).toHaveCount(0)
    await page.getByTestId('asn-search').fill('')
    await expect(page.getByText('Todavía no nombraste ningún ASN')).toBeVisible()
  })

  test('el desglose de Tráfico usa el nombre, filtra y enlaza a nombrar un ASN', async ({
    page,
  }) => {
    test.setTimeout(60_000)
    await page.setViewportSize({ width: 1440, height: 900 })
    await loginAsAdmin(page)
    await page.goto(PAGE)
    await page.getByTestId('asn-add').click()
    await page.getByTestId('asn-input').fill('263237')
    await page.getByTestId('asn-name').fill('Fibra Andina')
    await page.getByTestId('asn-category').click()
    await page.getByRole('option', { name: 'CDN e infraestructura' }).click()
    await page.getByTestId('asn-submit').click()
    await expect(page.locator('[data-asn-rule="263237"]')).toBeVisible()

    await page.goto(TRAFFIC)
    const table = page.getByTestId('traffic-breakdown-table')
    const asns = page.getByTestId('traffic-breakdown-asn')
    await expect(asns.filter({ hasText: 'AS263237' })).toHaveText(/^AS263237\s*Fibra Andina$/)

    // Búsqueda por nombre y por número de ASN (sin la fila "Otros").
    const search = page.getByTestId('traffic-breakdown-search')
    await search.fill('fibra')
    await expect(table.locator('tbody tr')).toHaveCount(1)
    await expect(table).toContainText('Fibra Andina')
    await search.fill('AS2906')
    await expect(table.locator('tbody tr')).toHaveCount(1)
    await expect(table).toContainText('Netflix Streaming Services')
    await search.fill('no existe')
    await expect(page.getByTestId('traffic-breakdown-no-match')).toBeVisible()

    // En organizaciones, la regla también da nombre a la fila.
    await search.fill('')
    await page.getByTestId('traffic-breakdown').getByRole('tab', { name: 'Organizaciones' }).click()
    await expect(table).toContainText('Fibra Andina')
    expect(await seriousViolations(page)).toEqual([])

    // "Nombrar AS15169" abre el alta con el ASN y el nombre que tiene hoy.
    await page.getByTestId('traffic-breakdown').getByRole('tab', { name: 'ASN' }).click()
    await search.fill('15169')
    await table.getByTestId('traffic-name-asn').click()
    await expect(page).toHaveURL(/\/t\/fibra-norte\/admin\/asn/)
    await expect(page.getByTestId('asn-input')).toHaveValue('15169')
    await expect(page.getByTestId('asn-lookup-name')).toHaveText('Google LLC')
    await expect(page.getByTestId('asn-name')).toHaveValue('Google LLC')
  })
})
