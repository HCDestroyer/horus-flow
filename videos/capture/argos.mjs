// Capturas de ArgosISP en modo fixtures (NUXT_PUBLIC_USE_FIXTURES=true, puerto 3150).
// Uso: node videos/capture/argos.mjs [salida]. Solo datos de demostración (fixtures).
import { createRequire } from 'node:module'
import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

const require = createRequire(resolve(import.meta.dirname, '../../apps/frontend/package.json'))
const { chromium } = require('@playwright/test')

const BASE = process.env.ARGOS_URL ?? 'http://127.0.0.1:3150'
const OUT = resolve(process.argv[2] ?? resolve(import.meta.dirname, '../build/shots/argos'))
mkdirSync(OUT, { recursive: true })

const browser = await chromium.launch()
const ctx = await browser.newContext({
  viewport: { width: 1600, height: 900 }, deviceScaleFactor: 2,
  locale: 'es-GT', timezoneId: 'America/Guatemala', colorScheme: 'dark', reducedMotion: 'reduce',
})
await ctx.addInitScript(() => localStorage.setItem('nuxt-color-mode', 'dark'))
const page = await ctx.newPage()
const shot = async (name, wait = 2000) => {
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${OUT}/${name}.png` }); console.log('ok', name)
}
const go = async (url, name, wait) => {
  try { await page.goto(BASE + url); await shot(name, wait) } catch (e) { console.log('FALLO', name, e.message) }
}
async function login(email, pass = 'clave-de-prueba') {
  await page.goto(BASE + '/login')
  await page.getByLabel('Correo').fill(email)
  await page.getByLabel('Contraseña').fill(pass)
  await page.getByRole('button', { name: 'Entrar' }).click()
  await page.waitForURL((u) => !u.pathname.startsWith('/login'))
}

await page.goto(BASE + '/login'); await shot('login', 1500)
await login('admin@argos.test')
await shot('inicio', 2500)
await go('/clientes', 'clientes')
await go('/clientes/1', 'cliente-1')
await go('/servicios', 'servicios')
await go('/servicios/1', 'servicio-1', 3000)
await page.locator('[data-prueba="bloque-horus"]').scrollIntoViewIfNeeded().catch(() => {})
await shot('servicio-1-horus', 800)
await go('/servicios/2', 'servicio-2')
await go('/configuracion/integraciones', 'integraciones')
await go('/configuracion/integraciones/horus', 'integracion-horus', 2500)
await go('/routers', 'routers')
await go('/facturas', 'facturas')

// Cobro y reactivación (navegación dentro de la SPA)
try {
  await page.goto(BASE + '/cobro'); await page.waitForTimeout(1500)
  await page.getByPlaceholder('Buscar cliente…').fill('Juan Carlos')
  await page.getByRole('option', { name: /Juan Carlos Ramírez/ }).click()
  await shot('cobro-1-pendiente', 1500)
  await page.getByLabel('Monto').press('Enter')
  await page.getByRole('article', { name: 'Recibo de pago' }).waitFor()
  await shot('cobro-2-recibo', 1500)
  await page.locator('[data-servicio="2"]').scrollIntoViewIfNeeded()
  await shot('cobro-3-reactivado', 800)
} catch (e) { console.log('FALLO cobro', e.message) }

// Integración: conectar con el enlace de Horus, activar y ver la seguridad del servicio
try {
  await page.goto(BASE + '/configuracion/integraciones/horus'); await page.waitForTimeout(1500)
  await page.locator('[data-prueba="enlace-horus"]').fill('https://horus.isp.example/conectar#demo-enlace-0001')
  await shot('int-1-enlace', 500)
  await page.locator('[data-prueba="conectar-enlace"]').click()
  await page.getByText('Conectado con Horus').first().waitFor()
  await page.locator('[data-prueba="interruptor-horus"]').click()
  await page.locator('[data-prueba="estado-conector"]').getByText('Activa').waitFor()
  await shot('int-2-activa', 1200)
  const lateral = page.locator('#dashboard-sidebar-principal')
  const cerrados = lateral.locator('[data-prueba="menu-principal"] button[aria-expanded="false"]')
  while (await cerrados.count()) await cerrados.first().click()
  await lateral.getByRole('link', { name: 'Servicios', exact: true }).click()
  await page.waitForURL(/\/servicios$/)
  await page.getByLabel('Buscar servicios').fill('100.64.10.11')
  await page.getByRole('link', { name: /^maria\.jose1$/ }).click()
  await page.waitForURL(/\/servicios\/1$/)
  await page.locator('[data-prueba="hallazgos-horus"]').waitFor()
  await shot('int-3-servicio', 1500)
  await page.locator('[data-prueba="bloque-horus"]').scrollIntoViewIfNeeded()
  await shot('int-4-bloque-horus', 800)
} catch (e) { console.log('FALLO integracion', e.message) }

await browser.close()
