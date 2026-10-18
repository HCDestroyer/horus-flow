import { expect, test } from '@playwright/test'
import { loginAsAdmin, seriousViolations } from './helpers'

/**
 * Hub multitransporte (docs/tunnels.md): el administrador cambia el transporte de un MikroTik a
 * IPsec IKEv2 (configuración con PSK de un solo uso y túnel en vivo) y a GRE por Internet, que
 * exige confirmar el riesgo y deja un aviso permanente (icono + texto, nunca solo color).
 * `pnpm e2e --grep @tunnels`.
 */

const T = '/t/fibra-norte'
const CENTRO = `${T}/routers/0192e333-0000-7000-8000-000000000101`

test.describe('transporte por router @tunnels', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.addInitScript(() =>
      window.localStorage.setItem('horus.mock.onboardingStepMs', '2500'),
    )
  })

  test('WireGuard → IPsec con PSK de un solo uso → túnel arriba', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(`${CENTRO}/connection`)
    const panel = page.getByTestId('transport-panel')
    await expect(panel.getByTestId('transport-current')).toContainText('WireGuard · Cifrado')
    await expect(page.getByTestId('router-protection')).toHaveAttribute(
      'data-protection',
      'encrypted',
    )

    await panel.getByTestId('transport-change').click()
    await panel.getByRole('radio', { name: /IPsec IKEv2/ }).check()
    await panel.getByTestId('transport-remote-address').fill('198.51.100.7')
    await expect(panel.getByTestId('transport-resulting')).toContainText('Cifrado')
    expect(await seriousViolations(page)).toEqual([])
    await panel.getByTestId('transport-save').click()
    await expect(panel.getByTestId('transport-current')).toContainText('IPsec IKEv2 · Cifrado')

    const wizard = page.getByTestId('tunnel-onboarding')
    await expect(wizard).toHaveAttribute('data-transport', 'ipsec')
    await wizard.getByTestId('tunnel-generate').click()
    await expect(wizard.getByTestId('tunnel-once')).toContainText('Solo se muestra una vez')
    const psk = wizard.getByTestId('tunnel-psk')
    await expect(psk).toBeVisible()
    const config = wizard.getByTestId('tunnel-config')
    await expect(config).toHaveAttribute('data-platform', 'routeros')
    await expect(config).toContainText('/ip ipsec identity add')
    await wizard.getByRole('tab', { name: /strongSwan/ }).click()
    await expect(wizard.getByTestId('tunnel-config')).toHaveAttribute('data-platform', 'strongswan')

    await expect(wizard.getByTestId('tunnel-up')).toContainText('IKE SA establecida', {
      timeout: 15_000,
    })
    await expect(page.getByTestId('router-tunnel-state')).toContainText('IKE SA activa')
    expect(await seriousViolations(page)).toEqual([])
  })

  test('GRE por Internet exige confirmación y deja un aviso permanente', async ({ page }) => {
    await loginAsAdmin(page)
    await page.goto(`${CENTRO}/connection`)
    const panel = page.getByTestId('transport-panel')
    await panel.getByTestId('transport-change').click()
    await panel.getByRole('radio', { name: /^GRE/ }).check()
    await panel.getByTestId('transport-remote-address').fill('198.51.100.7')
    await panel.getByRole('radio', { name: /Internet/ }).check()
    await expect(panel.getByTestId('transport-resulting')).toContainText('SIN CIFRAR por Internet')
    await expect(panel.getByTestId('transport-confirm-alert')).toBeVisible()
    await panel.getByTestId('transport-save').click()
    await expect(panel).toContainText('Confirma explícitamente')
    await panel.getByTestId('transport-confirm').click()
    expect(await seriousViolations(page)).toEqual([])
    await panel.getByTestId('transport-save').click()

    await expect(panel.getByTestId('transport-internet-warning')).toBeVisible()
    const badge = page.getByTestId('router-protection')
    await expect(badge).toHaveAttribute('data-protection', 'unencrypted_internet')
    await expect(badge).toContainText('SIN CIFRAR por Internet')
    const wizard = page.getByTestId('tunnel-onboarding')
    await expect(wizard.getByTestId('tunnel-gre-warning')).toContainText('GRE no cifra')
    await wizard.getByTestId('tunnel-generate').click()
    await expect(wizard.getByTestId('tunnel-psk')).toHaveCount(0)
    await expect(wizard.getByTestId('tunnel-config')).toContainText('/interface gre add')

    // El aviso sigue en la cabecera en cualquier pestaña (persistente; la API simulada vive en memoria
    // de la pestaña, así que se navega dentro de la SPA en vez de recargar).
    await page.getByRole('tab', { name: 'Resumen' }).click()
    await expect(page).toHaveURL(/\/summary$/)
    await expect(page.getByTestId('router-protection')).toHaveAttribute(
      'data-protection',
      'unencrypted_internet',
    )
  })
})
