// Capturas de ArgosISP en modo fixtures: panel (NUXT_PUBLIC_USE_FIXTURES=true, :3150) y portal
// del cliente (:3001). Uso: node videos/capture/argos.mjs [salida]. Solo datos de demostración.
// Los fixtures viven en memoria: los flujos (alta, cobro, integración) navegan dentro de la SPA.
import { createRequire } from 'node:module'
import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

const require = createRequire(resolve(import.meta.dirname, '../../apps/frontend/package.json'))
const { chromium } = require('@playwright/test')

const BASE = process.env.ARGOS_URL ?? 'http://127.0.0.1:3150'
const PORTAL = process.env.PORTAL_URL ?? 'http://127.0.0.1:3001'
const OUT = resolve(process.argv[2] ?? resolve(import.meta.dirname, '../build/shots/argos'))
const only = process.env.ONLY ? new RegExp(process.env.ONLY) : null
mkdirSync(OUT, { recursive: true })

const browser = await chromium.launch()
const desk = { viewport: { width: 1600, height: 900 }, deviceScaleFactor: 2, locale: 'es-GT', timezoneId: 'America/Guatemala', colorScheme: 'dark', reducedMotion: 'reduce' }
let ctx, page
async function fresh(opts = desk) {
  if (ctx) await ctx.close()
  ctx = await browser.newContext(opts)
  await ctx.addInitScript(() => localStorage.setItem('nuxt-color-mode', 'dark'))
  page = await ctx.newPage()
}
const run = (name) => !only || only.test(name)
const shot = async (name, wait = 2000) => {
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${OUT}/${name}.png` }); console.log('ok', name)
}
const go = async (url, name, wait) => {
  if (!run(name)) return
  try { await page.goto(BASE + url); await shot(name, wait) } catch (e) { console.log('FALLO', name, e.message) }
}
async function login(email = 'admin@argos.test', pass = 'clave-de-prueba') {
  await page.goto(BASE + '/login')
  await page.getByLabel('Correo').fill(email)
  await page.getByLabel('Contraseña').fill(pass)
  await page.getByRole('button', { name: 'Entrar' }).click()
  await page.waitForURL((u) => !u.pathname.startsWith('/login'))
}
async function menu(seccion, url) {
  const lateral = page.locator('#dashboard-sidebar-principal')
  await lateral.getByRole('link', { name: 'Inicio', exact: true }).waitFor()
  const cerrados = lateral.locator('[data-prueba="menu-principal"] button[aria-expanded="false"]')
  while (await cerrados.count()) await cerrados.first().click()
  await lateral.getByRole('link', { name: seccion, exact: true }).click()
  await page.waitForURL(url)
}
const step = async (name, fn) => {
  if (!run(name)) return
  try { await fn() } catch (e) {
    console.log('FALLO', name, e.message.split('\n').slice(0, 3).join(' | '))
    await page.screenshot({ path: `${OUT}/_fallo-${name}.png` }).catch(() => {})
  }
}

// ── Panel: pantallas sueltas ──
await fresh()
if (run('login')) { await page.goto(BASE + '/login'); await shot('login', 1500) }
await login()
await shot('inicio', 2500)
for (const [name, url, wait] of [
  ['clientes', '/clientes'], ['cliente-1', '/clientes/1'], ['cliente-1-cuenta', '/clientes/1?tab=estado-cuenta', 2500],
  ['cliente-1-linea', '/clientes/1?tab=linea-tiempo', 2500],
  ['servicios', '/servicios'], ['servicio-1', '/servicios/1', 3000], ['servicio-2', '/servicios/2', 3000],
  ['planes', '/planes'], ['routers', '/routers'], ['facturas', '/facturas'], ['factura-1', '/facturas/1', 2500],
  ['pagos', '/pagos'], ['caja', '/caja', 2500], ['promesas', '/promesas'], ['comprobantes', '/comprobantes'],
  ['cartera', '/cartera', 2500], ['reportes', '/reportes'], ['bitacora', '/bitacora'], ['libro', '/libro', 2500],
  ['integraciones', '/configuracion/integraciones'], ['integracion-horus', '/configuracion/integraciones/horus', 2500],
  ['plantillas', '/plantillas'], ['egresos', '/egresos'],
]) await go(url, name, wait)

// Vista previa del cambio de control de velocidad de un router (Routers → Velocidad)
await step('routers-velocidad', async () => {
  await page.goto(BASE + '/routers'); await page.waitForTimeout(1500)
  await page.getByRole('button', { name: /Velocidad/ }).first().click()
  await page.waitForTimeout(1500)
  await shot('routers-velocidad', 1500)
})

// ── Alta de cliente y servicio (flujo dentro de la SPA) ──
await step('alta', async () => {
  await fresh(); await login()
  await menu('Clientes', /\/clientes$/)
  await page.getByRole('link', { name: 'Nuevo cliente' }).click()
  await page.waitForURL(/\/clientes\/nuevo/)
  await shot('alta-1-cliente-vacio', 1200)
  await page.getByLabel('Nombre', { exact: true }).fill('Lucía Fernanda Morales')
  await page.getByLabel('Teléfono').fill('5555-1234')
  await shot('alta-2-cliente-lleno', 600)
  await page.getByRole('button', { name: 'Crear cliente' }).click()
  await page.waitForURL(/\/clientes\/\d+$/)
  await shot('alta-3-cliente-creado', 1500)
  await page.getByRole('link', { name: 'Nuevo servicio' }).first().click()
  await page.waitForURL(/\/servicios\/nuevo\?cliente=\d+/)
  await shot('alta-4-servicio-router', 1200)
  await page.getByRole('radio', { name: /RB-Centro/ }).click()
  await page.waitForTimeout(800)
  await page.getByRole('radio', { name: /Hogar 10 Mbps/ }).click()
  await shot('alta-5-servicio-plan', 1000)
  for (let i = 0; i < 5; i++) {
    await page.getByRole('button', { name: 'Siguiente' }).click()
    await shot(`alta-6-paso-${i + 1}`, 900)
  }
  await page.getByRole('button', { name: 'Crear servicio' }).click()
  await page.waitForURL(/\/clientes\/\d+\?tab=servicios/)
  await shot('alta-7-servicios-cliente', 1500)
  await page.getByRole('link', { name: /^lucia\./ }).click()
  await page.waitForURL(/\/servicios\/\d+$/)
  await shot('alta-8-servicio-activo', 2500)
})

// ── Cobro y reactivación (cajero) ──
await step('cobro', async () => {
  await fresh(); await login('cajero@argos.test', 'cajero12345')
  await menu('Cobrar', /\/cobro/)
  await shot('cobro-0-vacio', 1200)
  await page.getByPlaceholder('Buscar cliente…').fill('Juan Carlos')
  await page.getByRole('option', { name: /Juan Carlos Ramírez/ }).click()
  await shot('cobro-1-pendiente', 1500)
  await page.getByLabel('Monto').press('Enter')
  await page.getByRole('article', { name: 'Recibo de pago' }).waitFor()
  await shot('cobro-2-recibo', 1500)
  await page.locator('[data-servicio="2"]').scrollIntoViewIfNeeded()
  await shot('cobro-3-reactivado', 800)
})

// ── Integración con Horus ──
await step('int', async () => {
  await fresh(); await login()
  await page.goto(BASE + '/configuracion/integraciones/horus'); await page.waitForTimeout(1500)
  await page.locator('[data-prueba="enlace-horus"]').fill('https://horus.isp.example/conectar#demo-enlace-0001')
  await shot('int-1-enlace', 500)
  await page.locator('[data-prueba="conectar-enlace"]').click()
  await page.getByText('Conectado con Horus').first().waitFor()
  await page.locator('[data-prueba="interruptor-horus"]').click()
  await page.locator('[data-prueba="estado-conector"]').getByText('Activa').waitFor()
  await shot('int-2-activa', 1200)
  await menu('Servicios', /\/servicios$/)
  await page.getByLabel('Buscar servicios').fill('100.64.10.11')
  await page.getByRole('link', { name: /^maria\.jose1$/ }).click()
  await page.waitForURL(/\/servicios\/1$/)
  await page.locator('[data-prueba="hallazgos-horus"]').waitFor()
  await shot('int-3-servicio', 1500)
  await page.locator('[data-prueba="bloque-horus"]').scrollIntoViewIfNeeded()
  await shot('int-4-bloque-horus', 800)
})

// ── Portal del cliente (teléfono) ──
const phone = { viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true,
  locale: 'es-GT', timezoneId: 'America/Guatemala', colorScheme: 'dark', reducedMotion: 'reduce' }
await step('portal', async () => {
  await fresh(phone)
  await page.goto(PORTAL + '/corte?fixture=suspendido'); await page.getByText(/suspendid/i).first().waitFor({ timeout: 30000 }); await shot('portal-corte', 2500)
  await page.goto(PORTAL + '/'); await shot('portal-inicio', 2500)
  await page.goto(PORTAL + '/entrar'); await page.waitForTimeout(2000)
  await page.getByLabel('NIT, CUI o usuario de conexión').fill('12345679')
  await shot('portal-entrar', 300)
  await page.getByRole('button', { name: 'Enviarme un código' }).click()
  await page.getByTestId('destino-codigo').waitFor()
  await page.locator('input').first().click()
  await page.keyboard.type('12345')
  await shot('portal-codigo', 300)
  await page.keyboard.type('6')
  await page.waitForURL(/\/cuenta/)
  await page.getByTestId('cuenta').waitFor()
  await shot('portal-cuenta', 1500)
})

await browser.close()
