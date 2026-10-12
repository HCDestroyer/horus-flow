import { expect, test, type Page } from '@playwright/test'
import { layoutReport } from './layout-audit'
import { loginAsAdmin, PASSWORD, seriousViolations, TOTP } from './helpers'

/**
 * Destinos remotos SFTP de las copias (Almacenamiento, `platform.storage.manage`): alta guiada
 * con re-autenticación (datos → credenciales write-only → prueba con confirmación de la huella
 * → activar → copiar ahora), estados, copiar ahora (`SYNC_RUNNING`, `NOT_READY`), edición con
 * 412 y cambio de servidor, aviso "La clave del servidor cambió", credenciales, borrado (los
 * archivos remotos no se tocan), estado vacío, accesibilidad y diseño móvil.
 * `pnpm e2e --grep @storage`.
 */

const PAGE = '/platform/storage'
const KEY = [
  '-----BEGIN OPENSSH PRIVATE KEY-----',
  'b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW',
  'QyNTUxOQAAACBzZWNyZXRvLWRlLXBydWViYS1ub3Rhcy1yZWFsZXMAAAAA',
  '-----END OPENSSH PRIVATE KEY-----',
].join('\n')

function flags(page: Page, values: Record<string, string>) {
  return page.addInitScript((v) => {
    for (const [k, value] of Object.entries(v)) window.localStorage.setItem(k, value)
  }, values)
}

async function reauth(page: Page) {
  const dialog = page.getByRole('dialog', { name: 'Confirma tu identidad' })
  await expect(dialog).toContainText('destinos de las copias')
  await page.getByTestId('reauth-password').fill(PASSWORD)
  await page.getByTestId('reauth-code').fill(TOTP)
  await page.getByTestId('reauth-submit').click()
  await expect(dialog).toBeHidden()
}

const card = (page: Page, name: string) =>
  page.locator(`[data-testid="dst-card"][data-name="${name}"]`)

async function openStorage(page: Page) {
  await page.goto(PAGE)
  await expect(page.getByTestId('remote-destinations')).toBeVisible()
}

async function setNumber(page: Page, testId: string, value: string) {
  await page.getByTestId(testId).fill(value)
  await page.getByTestId(testId).blur()
}

test.describe('destinos remotos SFTP @storage', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
  })

  test('sin destinos: aviso con acceso y alta guiada completa (reauth, huella, activar, copiar)', async ({
    page,
  }) => {
    await flags(page, { 'horus.mock.reauthRequired': '1', 'horus.mock.sftpSyncMs': '1500' })
    await loginAsAdmin(page)
    await openStorage(page)
    const notice = page.getByTestId('no-remote-copy')
    await expect(notice).toContainText('Sin copia remota configurada')
    await expect(notice).toContainText('si el disco falla')
    await expect(page.getByTestId('dst-empty')).toBeVisible()
    await expect(page.getByTestId('remote-explainer')).toContainText('se cifran con age')
    expect(await seriousViolations(page)).toEqual([])

    // 1. Datos
    await notice.getByRole('button', { name: 'Añadir destino SFTP' }).click()
    const wizard = page.getByRole('dialog', { name: 'Añadir destino SFTP' })
    await expect(wizard).toBeVisible()
    const explainer = wizard.getByTestId('remote-explainer')
    await expect(explainer).toContainText('nunca ve los datos en claro')
    await expect(explainer).toContainText('paquete de secretos offline')
    await expect(explainer).toContainText('docs/disaster-recovery.md')
    await expect(explainer).toContainText('más de 30 días')
    await expect(page.getByTestId('dst-new-port')).toHaveValue('22')
    await expect(page.getByTestId('dst-new-retention')).toHaveValue('30')
    expect(await seriousViolations(page)).toEqual([])

    await page.getByTestId('dst-new-name').fill('NAS de la oficina')
    await page.getByTestId('dst-new-host').fill('sftp://nas.fibranorte.example')
    await page.getByTestId('dst-new-username').fill('horus')
    await page.getByTestId('dst-new-path').fill('srv/copias')
    await page.getByTestId('dst-wizard-create').click()
    await expect(wizard).toContainText('sin sftp:// delante')
    await expect(wizard).toContainText('La ruta debe ser absoluta')
    await page.getByTestId('dst-new-host').fill('nas.fibranorte.example')
    await page.getByTestId('dst-new-path').fill('/srv/copias/horus')
    await setNumber(page, 'dst-new-retention', '45')
    await expect(explainer).toContainText('más de 45 días')
    await page.getByTestId('dst-wizard-create').click()
    await reauth(page)

    // 2. Credenciales (clave privada, oculta; nunca se vuelve a mostrar)
    await expect(wizard).toContainText('sftp://horus@nas.fibranorte.example:22/srv/copias/horus')
    await expect(wizard).toContainText('no se vuelven a mostrar')
    const keyField = page.getByTestId('dst-private-key')
    await keyField.fill('ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI horus@nas')
    await page.getByTestId('dst-credentials-submit').click()
    await expect(page.getByTestId('dst-credentials-error')).toContainText(
      'Eso es una clave pública',
    )
    await keyField.fill(KEY)
    await expect(keyField).toHaveClass(/rd-secret-mask/)
    await page.getByTestId('dst-key-toggle').click()
    await expect(page.getByTestId('dst-key-toggle')).toHaveAttribute('aria-pressed', 'true')
    await expect(keyField).not.toHaveClass(/rd-secret-mask/)
    await page.getByTestId('dst-credentials-submit').click()

    // 3. Probar: primera conexión → verificar y confirmar la huella
    await expect(page.getByTestId('dst-wizard-next')).toBeDisabled()
    await page.getByTestId('dst-test-run').click()
    const confirm = page.getByTestId('dst-fingerprint-confirm')
    await expect(confirm).toContainText('Verifica la huella del servidor')
    await expect(confirm).toContainText('ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub')
    await expect(confirm.getByTestId('dst-fingerprint')).toHaveText(/^SHA256:[A-Za-z0-9+/]{43}$/)
    const fingerprint = (await confirm.getByTestId('dst-fingerprint').textContent())!.trim()
    await expect(page.locator('[data-stage="connect"]')).toHaveAttribute('data-state', 'done')
    await expect(page.locator('[data-stage="host_key"]')).toHaveAttribute('data-state', 'waiting')
    await expect(page.locator('[data-stage="auth"]')).toHaveAttribute('data-state', 'skipped')
    await expect(page.getByTestId('dst-wizard-next')).toBeDisabled()
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('dst-confirm-fingerprint').click()
    await expect(page.getByTestId('dst-test-ok')).toContainText('Conexión correcta')
    for (const stage of ['connect', 'host_key', 'auth', 'write']) {
      await expect(page.locator(`[data-stage="${stage}"]`)).toHaveAttribute('data-state', 'done')
    }
    await page.getByTestId('dst-wizard-next').click()

    // 4. Activar y copiar ahora
    await expect(wizard).toContainText('más de 45 días se borrarán del destino')
    await page.getByTestId('dst-wizard-activate-submit').click()
    await expect(page.getByTestId('dst-wizard-activated')).toContainText(
      'Copias a NAS de la oficina activadas',
    )
    await page.getByTestId('dst-wizard-sync').click()
    await expect(
      page.getByText('Copia a NAS de la oficina en marcha', { exact: true }),
    ).toBeVisible()
    await page.getByTestId('dst-wizard-finish').click()
    await expect(wizard).toBeHidden()

    // La tarjeta: pendiente → al día cuando llega la copia.
    await expect(page.getByTestId('no-remote-copy')).toHaveCount(0)
    const nas = card(page, 'NAS de la oficina')
    await expect(nas.getByTestId('dst-url')).toHaveText(
      'sftp://horus@nas.fibranorte.example:22/srv/copias/horus',
    )
    await expect(nas.getByTestId('dst-fingerprint-value')).toContainText(fingerprint)
    await expect(nas.getByTestId('dst-retention')).toHaveText('45 días')
    await expect(nas.getByTestId('dst-status')).toHaveText('Al día', { timeout: 10_000 })
    await expect(
      page.getByText('Copia a NAS de la oficina completada', { exact: true }),
    ).toBeVisible()
    await expect(nas.getByTestId('dst-pending')).toHaveText('Nada')
    await expect(nas.getByTestId('dst-last-success')).toContainText('hace')
    await expect(nas.getByTestId('dst-enabled')).toBeChecked()

    // Persistido en la sesión simulada.
    await page.reload()
    await expect(card(page, 'NAS de la oficina').getByTestId('dst-status')).toHaveText('Al día')
    expect(await seriousViolations(page)).toEqual([])
  })

  test('estados con su explicación, último error, retraso y pendientes', async ({ page }) => {
    await flags(page, { 'horus.mock.sftpSeed': '1' })
    await loginAsAdmin(page)
    await openStorage(page)
    await expect(page.getByTestId('no-remote-copy')).toHaveCount(0)
    const expected: [string, string, RegExp][] = [
      ['NAS de la oficina', 'Al día', /llegó a tiempo/],
      ['Servidor del datacenter', 'Con retraso', /más de una hora de retraso/],
      ['Respaldo en casa', 'Fallando', /espera en el disco local/],
      ['Prueba antigua', 'Desactivado', /No se copia nada/],
    ]
    for (const [name, status, explain] of expected) {
      await expect(card(page, name).getByTestId('dst-status')).toHaveText(status)
      await expect(card(page, name).getByTestId('dst-status-explain')).toHaveText(explain)
    }
    const dc = card(page, 'Servidor del datacenter')
    await expect(dc.getByTestId('dst-url')).toHaveText(
      'sftp://horus@198.51.100.20:2222/srv/copias/horus',
    )
    await expect(dc.getByTestId('dst-lag')).toHaveText('3 h')
    await expect(dc.getByTestId('dst-pending')).toHaveText(/12 archivos · 840 MB/)
    await expect(card(page, 'Respaldo en casa').getByTestId('dst-last-error')).toContainText(
      'Tiempo de espera agotado',
    )
    await expect(card(page, 'Prueba antigua').getByTestId('dst-lag')).toHaveText('—')
    expect(await seriousViolations(page)).toEqual([])
  })

  test('copiar ahora: copia en curso (SYNC_RUNNING) y destino no listo (NOT_READY)', async ({
    page,
  }) => {
    await flags(page, { 'horus.mock.sftpSeed': '1', 'horus.mock.sftpSyncMs': '60000' })
    await loginAsAdmin(page)
    await openStorage(page)
    const nas = card(page, 'NAS de la oficina')
    await nas.getByTestId('dst-sync').click()
    await expect(
      page.getByText('Copia a NAS de la oficina en marcha', { exact: true }),
    ).toBeVisible()
    await expect(nas.getByTestId('dst-syncing')).toHaveText('Copiando…')
    await nas.getByTestId('dst-sync').click()
    await expect(
      page.getByText('Ya hay una copia en curso a este destino. Espera a que termine.', {
        exact: true,
      }),
    ).toBeVisible()

    await card(page, 'Prueba antigua').getByTestId('dst-sync').click()
    await expect(page.getByText('Este destino aún no puede copiar', { exact: true })).toBeVisible()
    await expect(
      page.getByText(
        'Tiene que estar activado, con credenciales y con la huella del servidor confirmada.',
        { exact: true },
      ),
    ).toBeVisible()
  })

  test('activar y desactivar con el interruptor', async ({ page }) => {
    await flags(page, { 'horus.mock.sftpSeed': '1' })
    await loginAsAdmin(page)
    await openStorage(page)
    const old = card(page, 'Prueba antigua')
    await old.getByTestId('dst-enabled').click()
    await expect(page.getByText('Copias a Prueba antigua activadas', { exact: true })).toBeVisible()
    await expect(old.getByTestId('dst-enabled')).toBeChecked()
    await expect(old.getByTestId('dst-status')).not.toHaveText('Desactivado')
    await old.getByTestId('dst-enabled').click()
    await expect(old.getByTestId('dst-status')).toHaveText('Desactivado')
  })

  test('editar: 412 carga lo actual; cambiar el host desactiva hasta volver a probar', async ({
    page,
  }) => {
    await flags(page, {
      'horus.mock.sftpSeed': '1',
      'horus.mock.sftpConflict': '1',
      'horus.mock.reauthRequired': '1',
    })
    await loginAsAdmin(page)
    await openStorage(page)
    const nas = card(page, 'NAS de la oficina')
    await nas.getByTestId('dst-edit-open').click()
    const modal = page.getByRole('dialog', { name: 'Editar NAS de la oficina' })
    await expect(modal.getByTestId('dst-edit-server-warning')).toHaveCount(0)
    await setNumber(page, 'dst-edit-retention', '45')
    await page.getByTestId('dst-edit-submit').click()
    await reauth(page)
    await expect(page.getByTestId('dst-edit-conflict')).toContainText(
      'Otra persona cambió este destino',
    )
    await expect(page.getByTestId('dst-edit-retention')).toHaveValue('14')

    await page.getByTestId('dst-edit-host').fill('nas2.fibranorte.example')
    await expect(modal.getByTestId('dst-edit-server-warning')).toContainText(
      'Cambiar el host o el puerto obliga a volver a probar',
    )
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('dst-edit-submit').click()
    await expect(
      page.getByText('Destino NAS de la oficina actualizado', { exact: true }),
    ).toBeVisible()

    // Se abre la prueba del servidor nuevo; la tarjeta queda desactivada y sin huella.
    const test = page.getByRole('dialog', { name: 'Probar NAS de la oficina' })
    await expect(test).toBeVisible()
    await expect(nas.getByTestId('dst-status')).toHaveText('Desactivado')
    await expect(nas.getByTestId('dst-fingerprint-value')).toContainText('Sin confirmar')
    await expect(nas.getByTestId('dst-missing')).toContainText('huella del servidor confirmada')
    await expect(nas.getByTestId('dst-enabled')).toBeDisabled()
    await page.getByTestId('dst-test-run').click()
    await page.getByTestId('dst-confirm-fingerprint').click()
    await expect(page.getByTestId('dst-test-ok')).toBeVisible()
    await page.getByTestId('dst-test-enable').click()
    await expect(test).toBeHidden()
    await expect(nas.getByTestId('dst-status')).toHaveText('Pendiente')
    await expect(nas.getByTestId('dst-url')).toContainText('nas2.fibranorte.example')
    await expect(nas.getByTestId('dst-retention')).toHaveText('14 días')
  })

  test('la clave del servidor cambió: aviso de seguridad y aceptar solo tras verificar', async ({
    page,
  }) => {
    await flags(page, { 'horus.mock.sftpSeed': '1', 'horus.mock.sftpHostKeyChanged': '1' })
    await loginAsAdmin(page)
    await openStorage(page)
    const nas = card(page, 'NAS de la oficina')
    const pinned = (await nas.getByTestId('dst-fingerprint-text').textContent())!.trim()
    await nas.getByTestId('dst-test-open').click()
    await page.getByTestId('dst-test-run').click()
    const warning = page.getByTestId('dst-fingerprint-mismatch')
    await expect(warning).toHaveAttribute('role', 'alert')
    await expect(warning).toContainText('La clave del servidor cambió')
    await expect(warning).toContainText('ataque de intermediario')
    await expect(warning.getByTestId('dst-fingerprint-pinned')).toHaveText(pinned)
    const presented = (await warning.getByTestId('dst-fingerprint').textContent())!.trim()
    expect(presented).not.toBe(pinned)
    await expect(page.locator('[data-stage="host_key"]')).toHaveAttribute('data-state', 'failed')
    await expect(page.getByTestId('dst-fingerprint-confirm')).toHaveCount(0)
    const accept = page.getByTestId('dst-accept-new-fingerprint')
    await expect(accept).toBeDisabled()
    expect(await seriousViolations(page)).toEqual([])
    await page.getByTestId('dst-mismatch-verified').click()
    await accept.click()
    await expect(page.getByTestId('dst-test-ok')).toBeVisible()
    await page.getByTestId('dst-test-close').click()
    await expect(nas.getByTestId('dst-fingerprint-text')).toHaveText(presented)
  })

  test('cambiar credenciales: contraseña write-only', async ({ page }) => {
    await flags(page, { 'horus.mock.sftpSeed': '1' })
    await loginAsAdmin(page)
    await openStorage(page)
    await card(page, 'Respaldo en casa').getByTestId('dst-credentials-open').click()
    const modal = page.getByRole('dialog', { name: 'Credenciales de Respaldo en casa' })
    await expect(modal).toContainText('Ya hay credenciales guardadas')
    await modal.getByText('Contraseña', { exact: true }).first().click()
    await page.getByTestId('dst-credentials-submit').click()
    await expect(page.getByTestId('dst-credentials-error')).toHaveText('Escribe la contraseña.')
    await page.getByTestId('dst-password').fill('una-contraseña-larga')
    await page.getByTestId('dst-credentials-submit').click()
    await expect(
      page.getByText('Credenciales de Respaldo en casa guardadas', { exact: true }),
    ).toBeVisible()
    await expect(modal).toBeHidden()
    await card(page, 'Respaldo en casa').getByTestId('dst-credentials-open').click()
    await expect(page.getByTestId('dst-private-key')).toHaveValue('')
  })

  test('eliminar: los archivos remotos no se tocan; sin destinos vuelve el aviso', async ({
    page,
  }) => {
    await flags(page, { 'horus.mock.sftpSeed': '1', 'horus.mock.reauthRequired': '1' })
    await loginAsAdmin(page)
    await openStorage(page)
    const names = [
      'Prueba antigua',
      'NAS de la oficina',
      'Servidor del datacenter',
      'Respaldo en casa',
    ]
    for (const [i, name] of names.entries()) {
      await card(page, name).getByTestId('dst-delete-open').click()
      const modal = page.getByRole('dialog', { name: `¿Eliminar el destino ${name}?` })
      await expect(modal).toContainText('no se borran')
      await expect(modal).toContainText('Las copias locales de este servidor no cambian')
      if (i === 0) expect(await seriousViolations(page)).toEqual([])
      await page.getByTestId('dst-delete-confirm').click()
      if (i === 0) await reauth(page)
      await expect(page.getByText(`Destino ${name} eliminado`, { exact: true })).toBeVisible()
      await expect(card(page, name)).toHaveCount(0)
    }
    await expect(page.getByTestId('no-remote-copy')).toContainText('Sin copia remota configurada')
    await expect(
      page.getByTestId('no-remote-copy').getByRole('button', { name: 'Añadir destino SFTP' }),
    ).toBeVisible()
    await expect(page.getByTestId('dst-empty')).toBeVisible()
    await page.goto('/platform/system')
    await expect(page.getByTestId('no-remote-copy')).toBeVisible()
    await page
      .getByTestId('no-remote-copy')
      .getByRole('link', { name: 'Configurar copia remota' })
      .click()
    await expect(page).toHaveURL(/\/platform\/storage$/)
  })

  test('móvil: tarjetas apiladas, sin desbordamiento y asistente usable', async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 800 })
    await flags(page, { 'horus.mock.sftpSeed': '1', 'horus.mock.sftpHostKeyChanged': '1' })
    await loginAsAdmin(page)
    await openStorage(page)
    await expect(card(page, 'Servidor del datacenter')).toBeVisible()
    const problems = await layoutReport(page, {
      extra: ['[data-testid="app-navbar"]', '#main-content'],
    })
    expect(problems).toEqual([])
    const noOverflow = () =>
      page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)

    await card(page, 'NAS de la oficina').getByTestId('dst-test-open').click()
    await page.getByTestId('dst-test-run').click()
    await expect(page.getByTestId('dst-fingerprint-mismatch')).toBeVisible()
    expect(await noOverflow()).toBe(true)
    await page.getByTestId('dst-test-close').click()

    await page.getByTestId('dst-add').click()
    await expect(page.getByTestId('dst-wizard-create')).toBeVisible()
    await expect(page.getByTestId('dst-new-host')).toBeVisible()
    expect(await noOverflow()).toBe(true)
  })
})
