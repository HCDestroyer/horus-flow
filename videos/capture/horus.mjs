// Capturas de Horus Flow con la API simulada (HORUS_UI_MOCKS=1, `pnpm build:mocks`).
// Uso: node videos/capture/horus.mjs [salida]   (servidor: node apps/frontend/tests/e2e/serve.mjs)
// Solo datos de demostración: ISP ficticio "Fibra Norte", cuentas *.example.
import { createRequire } from 'node:module'
import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

const require = createRequire(resolve(import.meta.dirname, '../../apps/frontend/package.json'))
const { chromium } = require('@playwright/test')

const BASE = process.env.HORUS_URL ?? 'http://127.0.0.1:4173'
const OUT = resolve(process.argv[2] ?? resolve(import.meta.dirname, '../build/shots/horus'))
mkdirSync(OUT, { recursive: true })
const T = '/t/fibra-norte'
const NOC = `${T}/dashboards/0192f000-0000-7000-8000-00000000d001`
const SEC = `${T}/dashboards/0192f000-0000-7000-8000-00000000d002`
const only = process.env.ONLY ? new RegExp(process.env.ONLY) : null

const browser = await chromium.launch()
const ctx = await browser.newContext({
  viewport: { width: 1600, height: 900 },
  deviceScaleFactor: 2,
  locale: 'es-ES',
  timezoneId: 'America/Bogota',
  colorScheme: 'dark',
  reducedMotion: 'reduce',
})
await ctx.addInitScript(() => {
  localStorage.setItem('horus-color-mode', 'dark')
  localStorage.setItem('horus.mock.findingEveryMs', '0')
  if (!localStorage.getItem('horus.mock.onboardingStepMs'))
    localStorage.setItem('horus.mock.onboardingStepMs', '60000')
})
const page = await ctx.newPage()

async function shot(name, wait = 2500) {
  if (only && !only.test(name)) return
  await page.waitForLoadState('load')
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${OUT}/${name}.png` })
  console.log('ok', name)
}
async function go(url, name, wait) {
  if (only && !only.test(name)) return
  await page.goto(BASE + url)
  await shot(name, wait)
}

// Acceso
await page.goto(BASE + '/login')
await page.getByTestId('login-card').waitFor()
await shot('login', 1200)
await page.getByLabel('Correo electrónico').fill('ana.ruiz@fibranorte.example')
await page.getByLabel('Contraseña', { exact: true }).fill('horus-demo-2026')
await page.getByTestId('login-submit').click()
await page.getByLabel('Código de verificación').fill('123456')
await page.getByTestId('mfa-submit').click()
await page.waitForURL(/\/t\/fibra-norte$/)
await shot('resumen', 3500)

const shots = [
  ['dashboard-noc', NOC, 4000],
  ['dashboard-seguridad', SEC, 4000],
  ['trafico', `${T}/traffic`, 3500],
  ['trafico-30d', `${T}/traffic?range=30d`, 3500],
  ['clientes', `${T}/clients`, 3000],
  ['cliente-infectado', `${T}/clients/0193c000-0000-7000-8000-000000100001`, 3500],
  ['cliente-comercial', `${T}/clients/0193c000-0000-7000-8000-000000100005`, 3500],
  ['hallazgos', `${T}/security/findings`, 3000],
  ['hallazgo-botnet', `${T}/security/findings/0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f00`, 3000],
  ['hallazgo-c2', `${T}/security/findings/0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f01`, 3000],
  ['hallazgo-3', `${T}/security/findings/0192f0d1-1b2c-7e44-8a10-6b9c2d1e0f02`, 3000],
  ['capacidad', `${T}/capacity`, 3500],
  ['routers', `${T}/routers`, 3000],
  ['router-centro', `${T}/routers/0192e333-0000-7000-8000-000000000101`, 3500],
  ['router-prefijos', `${T}/routers/0192e333-0000-7000-8000-000000000101/prefixes`, 2500],
  ['nodos', `${T}/nodes`, 2500],
  ['switches', `${T}/switches`, 2500],
  ['servicios-infra', `${T}/infra-services`, 2500],
  ['reportes', `${T}/reports`, 2500],
  ['dashboards', `${T}/dashboards`, 2500],
  ['integraciones', `${T}/admin/integrations`, 2500],
  ['notificaciones', `${T}/admin/notifications`, 2500],
  ['deteccion', `${T}/admin/detection`, 2500],
  ['kioscos', `${T}/admin/kiosks`, 2500],
  ['ia', `${T}/admin/ai`, 2500],
  ['asn', `${T}/admin/asn`, 2500],
  ['plataforma-isps', '/platform/isps', 2500],
  ['plataforma-sistema', '/platform/system', 3000],
  ['plataforma-wireguard', '/platform/wireguard', 2500],
  ['plataforma-reputacion', '/platform/reputation', 2500],
]
for (const [name, url, wait] of shots) {
  try { await go(url, name, wait) } catch (e) { console.log('FALLO', name, e.message) }
}

// Mural NOC a 1920×1080 (escala 1:1 de TV) y seguridad
await page.setViewportSize({ width: 1920, height: 1080 })
await go(`${NOC}?scale=wall`, 'mural-noc', 4500)
await go(`${SEC}?scale=wall`, 'mural-seguridad', 4500)
await go(`${T}/routers?view=wall`, 'mural-routers', 3500)
await page.setViewportSize({ width: 1600, height: 900 })

// Alta de router: pasos en vivo (mock: un paso cada 3 s)
if (!only || only.test('alta')) {
  await page.evaluate(() => localStorage.setItem('horus.mock.onboardingStepMs', '3000'))
  await page.goto(BASE + `${T}/nodes`)
  await page.waitForTimeout(1500)
  await shot('alta-0-nodos', 500)
  await page.locator('[data-node="Nodo Lago"]').getByTestId('node-connect').click()
  await page.waitForURL(/\/connection$/)
  await shot('alta-1-pasos', 1500)
  await page.getByTestId('generate-script').click()
  await page.getByTestId('provisioning-script').waitFor()
  await shot('alta-2-script', 600)
  await page.getByTestId('provisioning-script').scrollIntoViewIfNeeded()
  await shot('alta-2b-script-detalle', 300)
  await page.getByTestId('key-received').waitFor({ timeout: 20000 })
  const verif = async () => page.locator('[data-check="flow"]').evaluate((el) => el.scrollIntoView({ block: 'center' }))
  await verif()
  await shot('alta-3-clave', 300)
  await page.locator('[data-check="tunnel"][data-done="true"]').waitFor({ timeout: 20000 })
  await verif()
  await shot('alta-4-tunel', 300)
  await page.locator('[data-check="flow"][data-done="true"]').waitFor({ timeout: 20000 })
  await verif()
  await shot('alta-5-flujo', 800)
  await page.getByTestId('router-header').evaluate((el) => el.scrollIntoView({ block: 'start' }))
  await shot('alta-6-exportando', 500)
  await page.evaluate(() => localStorage.setItem('horus.mock.onboardingStepMs', '60000'))
}

// Importar prefijos del MikroTik
if (!only || only.test('importar')) {
  await page.goto(BASE + `${T}/routers/0192e333-0000-7000-8000-000000000101/prefixes`)
  await page.getByTestId('import-prefixes').click()
  await page.getByTestId('import-preview').waitFor()
  await shot('importar-prefijos', 800)
}

await browser.close()
