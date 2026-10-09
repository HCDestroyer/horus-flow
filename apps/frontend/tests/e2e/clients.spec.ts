import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/**
 * I1-16 · Clientes: lista y ficha por IP. `pnpm e2e --grep @clients`.
 * Datos de la API simulada (base común con los widgets).
 */

const LIST = '/t/fibra-norte/clients'

test.describe('clientes @clients', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await loginAsAdmin(page)
  })

  test('la lista muestra IP, nodo, tipo con origen, estado, tráfico, seguridad y última vez', async ({
    page,
  }) => {
    await page.goto(LIST)
    const table = page.getByTestId('clients-table')
    await expect(table).toBeVisible()
    for (const header of [
      'Cliente',
      'Nodo',
      'Tipo',
      'Tráfico 24 h',
      'Principal',
      'Seguridad',
      'Estado',
      'Visto',
    ]) {
      await expect(table.getByRole('columnheader', { name: header })).toBeVisible()
    }
    // IP monoespaciada y tipo siempre con su origen.
    await expect(table.getByTestId('client-address').first()).toHaveClass(/font-mono/)
    await expect(table.getByTestId('client-kind').first()).toContainText(
      /por defecto|manual|detectado/,
    )
    // D18: "Infectado" acompañado del número de hallazgos que lo sostienen.
    await expect(table.getByText('Infectado').first()).toBeVisible()
    expect(await seriousViolations(page)).toEqual([])
  })

  test('la búsqueda por IP no deja la IP en la URL (va en el cuerpo de POST /customers/lookup)', async ({
    page,
  }) => {
    await page.goto(LIST)
    await expect(page.getByTestId('clients-table')).toBeVisible()
    await page.getByTestId('client-search').fill('10.20.0.41')
    await page.getByTestId('client-search').press('Enter')
    await expect(page.getByTestId('clients-count')).toContainText('1')
    expect(page.url()).not.toContain('10.20.0.41')
    // Y la ficha tampoco lleva la IP en la URL.
    await page.getByTestId('client-link').first().click()
    await expect(page.getByTestId('client-ip')).toHaveText('10.20.0.41')
    expect(page.url()).not.toContain('10.20.0.41')
  })

  test('IPv6: el cliente es su prefijo delegado (D22)', async ({ page }) => {
    await page.goto(LIST)
    await expect(page.getByTestId('clients-table')).toBeVisible()
    await page.getByTestId('client-search').fill('2001:db8:4a28:1800::1')
    await page.getByTestId('client-search').press('Enter')
    await expect(page.getByTestId('clients-count')).toContainText('Mostrando 1 ')
    await expect(page.getByTestId('clients-table').getByTestId('client-address')).toHaveText(
      '2001:db8:4a28:1800::/56',
    )
  })

  test('cambiar el tipo pide motivo, pone el candado, queda en el historial y avisa', async ({
    page,
  }) => {
    await page.goto(LIST)
    await expect(page.getByTestId('clients-table')).toBeVisible()
    await page.getByTestId('client-search').fill('10.20.1.47')
    await page.getByTestId('client-search').press('Enter')
    await page.getByTestId('client-link').first().click()
    await expect(page.getByTestId('client-header').getByTestId('client-kind')).toContainText(
      'por defecto',
    )
    await page.getByTestId('set-kind').click()
    await page.getByTestId('kind-submit').click()
    await expect(page.getByText('Escribe el motivo del cambio')).toBeVisible()
    await page.getByTestId('kind-reason').fill('Local con TPV')
    await page.getByTestId('kind-submit').click()
    await expect(page.getByText('Tipo actualizado').first()).toBeVisible()
    await expect(page.getByTestId('client-header').getByTestId('client-kind')).toContainText(
      'Comercial',
    )
    await expect(page.getByTestId('kind-lock')).toBeVisible()
    await page.getByRole('tab', { name: 'Historial' }).click()
    await expect(page.getByTestId('kind-history')).toContainText('Local con TPV')
  })

  test('ante 412 la UI muestra el valor actual y permite reintentar', async ({ page }) => {
    await page.goto(LIST)
    await expect(page.getByTestId('clients-table')).toBeVisible()
    await page.getByTestId('client-search').fill('10.20.2.177')
    await page.getByTestId('client-search').press('Enter')
    await page.getByTestId('client-link').first().click()
    await expect(page.getByTestId('client-header').getByTestId('client-kind')).toContainText(
      'detectado',
    )
    await page.getByTestId('set-kind').click()
    await page.getByTestId('kind-reason').fill('Confirmado con el cliente')
    await page.getByTestId('kind-submit').click()
    await expect(page.getByTestId('kind-conflict')).toBeVisible()
    await expect(page.getByTestId('kind-submit')).toHaveText('Reintentar con el valor actual')
    await page.getByTestId('kind-submit').click()
    await expect(page.getByText('Tipo actualizado').first()).toBeVisible()
    await expect(page.getByTestId('kind-lock')).toBeVisible()
  })

  test('reiniciar borra alias y notas, vuelve al tipo por defecto y lo registra', async ({
    page,
  }) => {
    await page.goto(LIST)
    await expect(page.getByTestId('clients-table')).toBeVisible()
    await page.getByTestId('client-search').fill('Panadería')
    await page.getByTestId('client-search').press('Enter')
    await page.getByTestId('client-link').first().click()
    await expect(page.getByTestId('client-alias')).toContainText('Panadería Sol')
    await page.getByTestId('client-more').click()
    await page.getByRole('menuitem', { name: 'Reiniciar cliente' }).click()
    await page.getByTestId('reset-reason').fill('IP reasignada a otro abonado')
    await page.getByTestId('reset-submit').click()
    await expect(page.getByText('Cliente reiniciado', { exact: true }).first()).toBeVisible()
    await expect(page.getByTestId('client-alias')).toHaveCount(0)
    await expect(page.getByTestId('client-header').getByTestId('client-kind')).toContainText(
      'por defecto',
    )
    await page.getByRole('tab', { name: 'Historial' }).click()
    await expect(page.getByTestId('kind-history')).toContainText('Cliente reiniciado')
  })

  test('un cliente inactivo explica desde cuándo y por qué', async ({ page }) => {
    await page.goto(`${LIST}?status=inactive`)
    await page.getByTestId('client-link').first().click()
    await expect(page.getByTestId('inactive-explanation')).toContainText('Inactivo desde el')
    await expect(page.getByTestId('inactive-explanation')).toContainText('No vemos tráfico')
  })

  test('sin permiso de escritura no aparecen "Cambiar tipo" ni "Reiniciar"', async ({ page }) => {
    // Ana es analista de seguridad en Red Andina: lee clientes, no los modifica.
    await page.goto('/t/red-andina/clients')
    await page.getByTestId('client-link').first().click()
    await expect(page.getByTestId('client-header')).toBeVisible()
    await expect(page.getByTestId('set-kind')).toHaveCount(0)
    await expect(page.getByTestId('client-more')).toHaveCount(0)
  })
})
