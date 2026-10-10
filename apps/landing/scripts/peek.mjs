// Captura rápida de una URL para revisar el diseño durante el desarrollo (no se usa en CI).
// Guarda la página completa troceada en partes de `alto` píxeles: <prefijo>-1.png, -2.png…
// Uso: node scripts/peek.mjs <url> <prefijo> [ancho] [dark|light] [alto]
import { chromium } from '@playwright/test'
import sharp from 'sharp'

const [url, prefix, width = '1440', scheme = 'light', slice = '1100'] = process.argv.slice(2)
const browser = await chromium.launch()
const page = await browser.newPage({
  viewport: { width: Number(width), height: 900 },
  colorScheme: scheme,
  reducedMotion: 'reduce',
})
await page.goto(url, { waitUntil: 'networkidle' })
await page.evaluate(async () => {
  for (let y = 0; y < document.body.scrollHeight; y += 600) {
    window.scrollTo(0, y)
    await new Promise((r) => setTimeout(r, 60))
  }
  window.scrollTo(0, 0)
})
await page.waitForLoadState('networkidle')
const buf = await page.screenshot({ fullPage: true })
await browser.close()
const img = sharp(buf)
const { width: w, height: h } = await img.metadata()
const step = Number(slice)
let n = 0
for (let top = 0; top < h; top += step) {
  n++
  await sharp(buf)
    .extract({ left: 0, top, width: w, height: Math.min(step, h - top) })
    .toFile(`${prefix}-${n}.png`)
}
console.log(`${w}×${h} → ${n} partes`)
