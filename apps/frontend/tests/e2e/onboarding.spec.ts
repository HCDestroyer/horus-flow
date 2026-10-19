import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/**
 * I1-19 · Router: onboarding, prefijos de clientes y estado del exportador.
 * `pnpm e2e --grep @onboarding`.
 */

const T = '/t/fibra-norte'
const PENDING = `${T}/routers/0192e333-0000-7000-8000-000000000106`
const CENTRO = `${T}/routers/0192e333-0000-7000-8000-000000000101`
const COSTA = `${T}/nodes/0192e111-0000-7000-8000-000000000105`
const RIBERA = '/t/valle-conecta/routers/0192e333-0000-7000-8000-000000000202'

test.describe('router y onboarding @onboarding', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('router pendiente: tres pasos, script con Copiar/Descargar y aviso; los pasos se marcan en vivo', async ({
    page,
  }) => {
    await page.addInitScript(() =>
      window.localStorage.setItem('horus.mock.onboardingStepMs', '2500'),
    )
    await loginAsAdmin(page)
    await page.goto(`${T}/nodes`)
    await page.locator('[data-node="Nodo Lago"]').getByTestId('node-connect').click()
    await expect(page).toHaveURL(/\/connection$/)
    const wizard = page.getByTestId('onboarding')
    await expect(wizard.locator('[data-step]')).toHaveCount(3)
    await expect(page.getByTestId('router-exporter-state')).toContainText('Pendiente')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('generate-script').click()
    const script = page.getByTestId('provisioning-script')
    await expect(script).toContainText('/interface wireguard add')
    await expect(script.getByRole('button', { name: 'Copiar' })).toBeVisible()
    await expect(script.getByRole('button', { name: 'Descargar .rsc' })).toBeVisible()
    await expect(page.getByTestId('script-once')).toContainText('Solo se muestra una vez')
    // Por partes: cada encabezado `# N)` es una parte; tras firmar el certificado, esperar.
    const parts = page.getByTestId('script-parts')
    await expect(page.getByTestId('script-part-1')).toContainText('/interface wireguard add')
    await expect(page.getByTestId('script-part-3')).toContainText('/certificate sign horus-api')
    await expect(page.getByTestId('script-part-4')).toContainText('/ip service set www-ssl')
    await expect(parts.getByTestId('script-part-wait')).toHaveCount(1)
    await expect(page.getByTestId('revoke-token')).toBeVisible()

    // Sin recargar: clave recibida → handshake → primer flujo.
    await expect(page.getByTestId('key-received')).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('[data-check="tunnel"]')).toHaveAttribute('data-done', 'true', {
      timeout: 15_000,
    })
    await expect(page.locator('[data-check="flow"]')).toHaveAttribute('data-done', 'true', {
      timeout: 15_000,
    })
    await expect(page.getByTestId('router-exporter-state')).toContainText('Exportando')
    await expect(page.getByTestId('router-last-flow')).toContainText('último flujo hace')
  })

  test('alta manual: el script no llama a Horus y la clave se pega en el panel', async ({
    page,
  }) => {
    await page.addInitScript(() =>
      window.localStorage.setItem('horus.mock.onboardingStepMs', '2500'),
    )
    await loginAsAdmin(page)
    await page.goto(`${PENDING}/connection`)
    await page.getByRole('switch', { name: /Alta manual/ }).click()
    await page.getByTestId('generate-script').click()
    const script = page.getByTestId('provisioning-script')
    await expect(script).toContainText('HORUS_ROUTER_PUBKEY=')
    await expect(script).not.toContainText('/tool fetch')

    const input = page.getByTestId('manual-key-input')
    await expect(input).toBeVisible()
    await input.fill('no-es-una-clave')
    await page.getByTestId('manual-key-submit').click()
    await expect(page.getByTestId('manual-key')).toContainText('No es una clave pública WireGuard')
    // Sin el envío del router no avanza solo.
    await page.waitForTimeout(3000)
    await expect(page.getByTestId('key-received')).toHaveCount(0)

    await input.fill('HORUS_ROUTER_PUBKEY=yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=')
    await page.getByTestId('manual-key-submit').click()
    await expect(page.getByTestId('key-received')).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('[data-check="tunnel"]')).toHaveAttribute('data-done', 'true', {
      timeout: 15_000,
    })
    expect(await seriousViolations(page)).toEqual([])
  })

  test('si el token caduca, se ofrece regenerarlo', async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem('horus.mock.onboardingStepMs', '60000')
      window.localStorage.setItem('horus.mock.enrollTtlMs', '1500')
    })
    await loginAsAdmin(page)
    await page.goto(`${PENDING}/connection`)
    await page.getByTestId('generate-script').click()
    await expect(page.getByTestId('token-dead')).toContainText('caducó', { timeout: 15_000 })
    await expect(
      page.getByTestId('token-dead').getByRole('button', { name: 'Generar otro script' }),
    ).toBeVisible()
  })

  test('prefijo que solapa: error en línea; rol Clientes, Infraestructura o Excluido', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(`${CENTRO}/prefixes`)
    await page.getByTestId('add-prefix').click()
    const roles = page.getByTestId('prefix-role')
    for (const r of ['Clientes', 'Infraestructura', 'Excluido'])
      await expect(roles.getByText(r)).toBeVisible()
    await page.getByTestId('prefix-input').fill('10.20.4.0/24')
    await page.getByTestId('prefix-submit').click()
    await expect(page.getByTestId('prefix-form')).toContainText('Solapa con 10.20.0.0/20')
    await page.getByTestId('prefix-input').fill('10.21.0.0/24')
    await roles.getByText('Infraestructura').click()
    await page.getByTestId('prefix-submit').click()
    await expect(
      page.getByTestId('prefixes-list').locator('[data-prefix="10.21.0.0/24"]'),
    ).toContainText('Infraestructura')
  })

  test('importar del MikroTik: revisión con casillas, rol propuesto e IPv6; nada sin Confirmar', async ({
    page,
  }) => {
    await loginAsAdmin(page)
    await page.goto(`${CENTRO}/prefixes`)
    const list = page.getByTestId('prefixes-list')
    await expect(list.locator('li')).toHaveCount(3)
    await page.getByTestId('import-prefixes').click()
    const preview = page.getByTestId('import-preview')
    await expect(preview).toBeVisible()
    await expect(
      preview.locator('[data-prefix="10.20.0.0/20"]').getByTestId('import-diff'),
    ).toHaveText('Ya registrado como Clientes')
    await expect(
      preview.locator('[data-prefix="10.20.0.0/16"]').getByTestId('import-diff'),
    ).toContainText('Solapa')
    // IPv6 (enmienda E-IPv6-1): tamaño delegado, uso del pool y tamaño de cliente propuesto.
    const pd = preview.locator('[data-prefix="2001:db8:4af0::/44"]')
    await expect(pd.getByTestId('import-v6')).toContainText('delega /48')
    await expect(pd.getByTestId('import-v6')).toContainText('DHCPv6-PD')
    const shared = preview.locator('[data-prefix="2001:db8:4aff:ff00::/56"]')
    await expect(shared.getByTestId('import-v6')).toContainText('Infraestructura')
    // Cerrar sin confirmar no aplica nada.
    await page.getByRole('button', { name: 'Cancelar' }).click()
    await expect(list.locator('li')).toHaveCount(3)
    await page.getByTestId('import-prefixes').click()
    await expect(page.getByTestId('import-preview')).toBeVisible()
    await page.getByTestId('import-confirm').click()
    await expect(page.getByText(/Prefijos importados: \d/).first()).toBeVisible()
    await expect(list.locator('[data-prefix="10.20.32.0/24"]')).toBeVisible()
  })

  test('huella TLS cambiada: aviso, importación detenida y re-fijado explícito', async ({
    page,
  }) => {
    await page.addInitScript(() =>
      window.localStorage.setItem('horus.mock.tlsChanged', '0192e333-0000-7000-8000-000000000101'),
    )
    await loginAsAdmin(page)
    await page.goto(`${CENTRO}/prefixes`)
    await expect(page.getByTestId('tls-pin-banner')).toContainText(
      'La huella TLS del router cambió',
    )
    // La importación no lee el router: pide revisar la huella.
    await page.getByTestId('import-prefixes').click()
    await expect(page.getByTestId('import-pin-required')).toContainText('cambió')
    await page.keyboard.press('Escape')
    await page.getByTestId('tls-pin-review').click()
    await expect(page).toHaveURL(/\/credentials$/)
    const card = page.getByTestId('tls-pin-card')
    await expect(card.getByTestId('tls-pin-status')).toHaveText('Cambió')
    await expect(card.getByTestId('tls-pin-pinned')).toContainText('5A:5A')
    await expect(card.getByTestId('tls-pin-observed')).toContainText('C3:C3')
    expect(await seriousViolations(page)).toEqual([])
    await card.getByTestId('tls-pin-confirm').click()
    await expect(page.getByTestId('tls-pin-confirm-value')).toContainText('C3:C3')
    await page.getByTestId('tls-pin-confirm-submit').click()
    await expect(card.getByTestId('tls-pin-status')).toHaveText('Confirmada')
    await expect(card.getByTestId('tls-pin-pinned')).toContainText('C3:C3')
    await expect(page.getByTestId('tls-pin-banner')).toHaveCount(0)
  })

  test('nodo sin prefijos: banner de modo descubrimiento y propuestas', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(COSTA)
    await expect(page.getByTestId('discovery-banner')).toContainText(
      'no tiene prefijos de clientes',
    )
    const proposals = page.getByTestId('proposal')
    await expect(proposals.first()).toBeVisible()
    await expect(
      proposals.first().getByRole('button', { name: 'Aceptar como Clientes' }),
    ).toBeVisible()
    await expect(proposals.first().getByRole('button', { name: 'Infraestructura' })).toBeVisible()
    await expect(proposals.first().getByRole('button', { name: 'Excluir' })).toBeVisible()
    await proposals.first().getByRole('button', { name: 'Aceptar como Clientes' }).click()
    await expect(page.getByTestId('discovery-banner')).toHaveCount(0)
    await expect(page.getByTestId('prefixes-list')).toContainText('100.64.12.0/22')

    // Excluir una propuesta: aparece en «Excluido», deja de proponerse y se puede revertir.
    const next = proposals.first()
    const excluded = (await next.locator('.font-mono').textContent())!.trim()
    await next.getByRole('button', { name: 'Excluir' }).click()
    const row = page.getByTestId('prefixes-excluded').locator(`[data-prefix="${excluded}"]`)
    await expect(row).toBeVisible()
    await expect(page.getByTestId('proposal').filter({ hasText: excluded })).toHaveCount(0)
    await row.getByTestId('prefix-unexclude').click()
    await expect(
      page.getByTestId('prefixes-customers').locator(`[data-prefix="${excluded}"]`),
    ).toBeVisible()
  })

  test('router silencioso: desde cuándo y qué revisar', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(RIBERA)
    await expect(page.getByTestId('router-silent')).toContainText('Silencioso desde')
    await expect(page.getByTestId('router-silent')).toContainText('túnel WireGuard')
  })
})
