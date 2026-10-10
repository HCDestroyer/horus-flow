#!/usr/bin/env node
// Copia y optimiza las capturas del recorrido del producto (apps/frontend/docs/screenshots/tour,
// datos simulados) a public/img/shots en AVIF y WebP con varios anchos, y genera la imagen
// Open Graph (public/img/og.png, 1200×630). Uso: pnpm images. El resultado se versiona.
import { mkdir, rm } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import sharp from 'sharp'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..')
const tour = join(root, '../frontend/docs/screenshots/tour')
const out = join(root, 'public/img/shots')

// Debe coincidir con app/config/shots.ts.
const SHOTS = [
  { name: 'mural', src: '32-mural-noc-1920x1080.png', widths: [640, 960, 1440, 1920] },
  { name: 'infected', src: '60-cliente-infectado.png', widths: [640, 960, 1440] },
  { name: 'finding', src: '64-hallazgo-detalle.png', widths: [640, 960, 1440] },
  { name: 'router', src: '70-router-onboarding-script.png', widths: [640, 960, 1440] },
  { name: 'kiosk', src: '73-kiosco-codigo.png', widths: [640, 960, 1440] },
  { name: 'mobile', src: '82-movil-hallazgo-claro.png', widths: [390] },
]

await rm(out, { recursive: true, force: true })
await mkdir(out, { recursive: true })

for (const shot of SHOTS) {
  const input = join(tour, shot.src)
  for (const w of shot.widths) {
    const base = sharp(input).resize({ width: w, withoutEnlargement: true })
    await base.clone().avif({ quality: 55, effort: 6 }).toFile(join(out, `${shot.name}-${w}.avif`))
    await base.clone().webp({ quality: 78, effort: 6 }).toFile(join(out, `${shot.name}-${w}.webp`))
  }
  const meta = await sharp(input).metadata()
  console.log(`${shot.name}: ${meta.width}×${meta.height} → ${shot.widths.join(', ')}`)
}

// Imagen Open Graph: fondo de marca, título y el mural del NOC a la derecha.
const W = 1200
const H = 630
const mural = await sharp(join(tour, '32-mural-noc-1920x1080.png'))
  .resize({ width: 760 })
  .png()
  .toBuffer()
const svg = `
<svg width="${W}" height="${H}" xmlns="http://www.w3.org/2000/svg">
  <rect width="100%" height="100%" fill="#0d1220"/>
  <g transform="translate(64 92)" fill="none" stroke="#93b1ff" stroke-width="5" stroke-linejoin="round" stroke-linecap="round">
    <path d="M6 31C13 20 22 15 32 15s19 5 26 16c-7 11-16 16-26 16S13 42 6 31Z"/>
    <path d="M26 46c-1.2 5.2-4.4 8.8-9.2 10.6"/>
  </g>
  <circle cx="96" cy="123" r="7.2" fill="#93b1ff"/>
  <text x="150" y="137" font-family="DejaVu Sans, Arial, sans-serif" font-size="44" font-weight="700" fill="#ffffff">Horus Flow</text>
  <text x="64" y="250" font-family="DejaVu Sans, Arial, sans-serif" font-size="40" font-weight="700" fill="#e8ecf4">
    <tspan x="64" dy="0">Detección de botnets</tspan>
    <tspan x="64" dy="52">y visibilidad de tráfico</tspan>
    <tspan x="64" dy="52">para ISP</tspan>
  </text>
  <text x="64" y="470" font-family="DejaVu Sans, Arial, sans-serif" font-size="24" fill="#a4aec4">
    <tspan x="64" dy="0">IPFIX de MikroTik por WireGuard ·</tspan>
    <tspan x="64" dy="34">hallazgos explicables y comandos RouterOS</tspan>
  </text>
  <text x="64" y="580" font-family="DejaVu Sans, Arial, sans-serif" font-size="18" fill="#a4aec4">C&amp;S Company · Guatemala</text>
</svg>`
await mkdir(join(root, 'public/img'), { recursive: true })
await sharp(Buffer.from(svg))
  .composite([{ input: mural, left: 640, top: 150 }])
  .png({ compressionLevel: 9, palette: true, quality: 90 })
  .toFile(join(root, 'public/img/og.png'))
console.log('og.png: 1200×630')
