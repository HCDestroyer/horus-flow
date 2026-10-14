#!/usr/bin/env node
// Render de las composiciones a los vídeos de la landing (apps/landing/public/motion/).
//
//   pnpm render                  todas las composiciones
//   pnpm render:one HeroNetwork  una (o varias) por id
//   pnpm frames [id…]            solo los PNG de frames clave (docs/frames/)
//
// Por composición:
//   1. Remotion renderiza un intermedio de alta calidad (H.264 CRF 12, out/, se borra al final).
//   2. ffmpeg codifica WebM VP9 y MP4 H.264 (2 pasadas, sin audio) a ≈1920 y ≈960 px de ancho,
//      apuntando a un bitrate que respeta el presupuesto de tamaño (y falla si se pasa).
//   3. Póster AVIF y WebP del frame más representativo, en los dos anchos.
//   4. PNG de 3–4 frames clave en docs/frames/.
//
// Navegador: el Chromium ya instalado en /opt/pw-browsers (nunca se descarga otro). Se puede
// cambiar con REMOTION_BROWSER_EXECUTABLE.
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, rmSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { bundle } from '@remotion/bundler'
import { renderMedia, renderStill, selectComposition } from '@remotion/renderer'
import sharp from 'sharp'

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const OUT = path.join(ROOT, 'out')
const PUBLIC = path.resolve(ROOT, '../landing/public/motion')
const FRAMES = path.join(ROOT, 'docs/frames')
const BROWSER =
  process.env.REMOTION_BROWSER_EXECUTABLE ??
  '/opt/pw-browsers/chromium_headless_shell-1194/chrome-linux/headless_shell'

// slug: nombre de archivo; poster: frame del póster; keyFrames: PNG de docs/frames;
// budgetMB: tope por formato y tamaño.
const META = {
  HeroNetwork: { slug: 'hero-network', poster: 196, keyFrames: [0, 112, 160, 196], budgetMB: 1.5 },
  HowItWorks: { slug: 'how-it-works', poster: 262, keyFrames: [30, 96, 200, 262], budgetMB: 1 },
  Resilience: { slug: 'resilience', poster: 112, keyFrames: [20, 112, 150, 220], budgetMB: 1 },
}
const SIZES = [1920, 960]

const args = process.argv.slice(2)
const framesOnly = args.includes('--frames-only')
const ids = args.filter((a) => !a.startsWith('--'))
for (const id of ids) {
  if (!META[id]) {
    console.error(`Composición desconocida: ${id}. Disponibles: ${Object.keys(META).join(', ')}`)
    process.exit(2)
  }
}
const selected = ids.length ? ids : Object.keys(META)

if (!existsSync(BROWSER)) {
  console.error(`No existe el navegador ${BROWSER} (define REMOTION_BROWSER_EXECUTABLE).`)
  process.exit(1)
}
for (const d of [OUT, PUBLIC, FRAMES]) mkdirSync(d, { recursive: true })

const ffmpeg = (argv) =>
  execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...argv], {
    stdio: 'inherit',
    cwd: OUT,
  })
const kb = (f) => statSync(f).size / 1024

console.log('Empaquetando composiciones…')
const serveUrl = await bundle({
  entryPoint: path.join(ROOT, 'src/index.ts'),
  outDir: path.join(ROOT, '.bundle'),
})
const common = {
  serveUrl,
  browserExecutable: BROWSER,
  chromeMode: 'headless-shell',
  logLevel: 'warn',
}

for (const id of selected) {
  const meta = META[id]
  const composition = await selectComposition({ ...common, id })
  const seconds = composition.durationInFrames / composition.fps
  console.log(
    `\n${id}: ${composition.width}×${composition.height}, ${seconds} s a ${composition.fps} fps`,
  )

  // Frames clave (PNG a 960 px de ancho para no inflar el repo).
  for (const f of meta.keyFrames) {
    const png = path.join(OUT, `${id}-${f}.png`)
    await renderStill({ ...common, composition, frame: f, output: png, imageFormat: 'png' })
    const dest = path.join(FRAMES, `${meta.slug}-f${String(f).padStart(3, '0')}.png`)
    await sharp(png)
      .resize({ width: 960 })
      .png({ compressionLevel: 9, palette: false })
      .toFile(dest)
    console.log(`  frame ${f} → ${path.relative(ROOT, dest)}`)
  }
  if (framesOnly) continue

  // Póster (frame más representativo).
  const posterPng = path.join(OUT, `${id}-poster.png`)
  await renderStill({
    ...common,
    composition,
    frame: meta.poster,
    output: posterPng,
    imageFormat: 'png',
  })
  for (const w of SIZES) {
    const base = path.join(PUBLIC, `${meta.slug}-poster-${w}`)
    await sharp(posterPng)
      .resize({ width: w })
      .avif({ quality: 55, effort: 6 })
      .toFile(`${base}.avif`)
    await sharp(posterPng)
      .resize({ width: w })
      .webp({ quality: 78, effort: 6 })
      .toFile(`${base}.webp`)
    console.log(
      `  póster ${w}: avif ${kb(`${base}.avif`).toFixed(0)} KB, webp ${kb(`${base}.webp`).toFixed(0)} KB`,
    )
  }

  // Intermedio de alta calidad.
  const master = path.join(OUT, `${id}-master.mp4`)
  console.log('  renderizando intermedio…')
  await renderMedia({
    ...common,
    composition,
    codec: 'h264',
    crf: 12,
    pixelFormat: 'yuv420p',
    muted: true,
    concurrency: 4,
    outputLocation: master,
  })

  // Codificación final. El grande apunta al 85 % del presupuesto; el pequeño, al 40 %.
  const budget = meta.budgetMB * 1024 * 1024
  for (const w of SIZES) {
    const share = w === SIZES[0] ? 0.85 : 0.4
    const kbps = Math.floor((budget * share * 8) / seconds / 1000)
    const scale = `scale=${w}:-2:flags=lanczos`
    const gop = String(composition.fps * 2)
    const webm = path.join(PUBLIC, `${meta.slug}-${w}.webm`)
    const mp4 = path.join(PUBLIC, `${meta.slug}-${w}.mp4`)
    const vp9 = [
      '-i',
      master,
      '-vf',
      scale,
      '-an',
      '-c:v',
      'libvpx-vp9',
      '-b:v',
      `${kbps}k`,
      '-maxrate',
      `${Math.floor(kbps * 1.4)}k`,
      '-bufsize',
      `${kbps * 2}k`,
      '-row-mt',
      '1',
      '-tile-columns',
      '2',
      '-g',
      gop,
      '-deadline',
      'good',
      '-cpu-used',
      '2',
      '-pix_fmt',
      'yuv420p',
    ]
    ffmpeg([...vp9, '-pass', '1', '-passlogfile', `${id}-${w}-vp9`, '-f', 'null', '/dev/null'])
    ffmpeg([...vp9, '-pass', '2', '-passlogfile', `${id}-${w}-vp9`, webm])
    const x264 = [
      '-i',
      master,
      '-vf',
      scale,
      '-an',
      '-c:v',
      'libx264',
      '-preset',
      'slow',
      '-profile:v',
      'high',
      '-b:v',
      `${kbps}k`,
      '-maxrate',
      `${Math.floor(kbps * 1.4)}k`,
      '-bufsize',
      `${kbps * 2}k`,
      '-g',
      gop,
      '-pix_fmt',
      'yuv420p',
    ]
    ffmpeg([...x264, '-pass', '1', '-passlogfile', `${id}-${w}-x264`, '-f', 'mp4', '/dev/null'])
    ffmpeg([
      ...x264,
      '-pass',
      '2',
      '-passlogfile',
      `${id}-${w}-x264`,
      '-movflags',
      '+faststart',
      mp4,
    ])
    for (const f of [webm, mp4]) {
      const size = kb(f)
      const ok = size * 1024 <= budget
      console.log(
        `  ${path.basename(f)}: ${size.toFixed(0)} KB ${ok ? '' : `> presupuesto ${meta.budgetMB} MB`}`,
      )
      if (!ok) process.exitCode = 1
    }
  }
}

// Intermedios fuera (ocupan mucho).
rmSync(OUT, { recursive: true, force: true })
rmSync(path.join(ROOT, '.bundle'), { recursive: true, force: true })
console.log('\nListo.')
