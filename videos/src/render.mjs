// Renderiza un video: línea de tiempo (guion + duraciones de la voz) → cuadros con Playwright
// → ffmpeg (H.264) → mezcla de voz y música → MP4 + SRT.
// Uso: node videos/src/render.mjs <video> <16x9|9x16> [--voice A] [--preview t1,t2,...] [--fps 30]
import { createRequire } from 'node:module'
import { spawn, execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { stories } from './stories.mjs'

const ROOT = resolve(import.meta.dirname, '..')
const require = createRequire(resolve(ROOT, '../apps/frontend/package.json'))
const { chromium } = require('@playwright/test')

const args = process.argv.slice(2)
const opt = (k, d) => { const i = args.indexOf(k); return i >= 0 ? args[i + 1] : d }
const [vid, fmt = '16x9'] = args
const voice = opt('--voice', 'L')
const fps = Number(opt('--fps', 30))
const preview = opt('--preview', null)
const story = stories[vid]
if (!story) throw new Error(`No existe el video "${vid}"`)
const vertical = fmt === '9x16'
const W = vertical ? 1080 : 1920, H = vertical ? 1920 : 1080
const fast = vid === 'promo'

// ── Línea de tiempo ──
const manifestPath = resolve(ROOT, `build/voice/${voice}/${vid}.json`)
const manifest = existsSync(manifestPath) ? JSON.parse(readFileSync(manifestPath, 'utf8')) : []
const xf = fast ? 0.3 : 0.5
const lead = fast ? 0.2 : 0.45, tail = fast ? 0.25 : 0.6
let t = 0
const scenes = story.scenes.map((sc, i) => {
  const v = manifest[i]
  const vd = v ? v.dur : (sc.say ? sc.say.length / 14 : 0)
  const base = { title: 3.4, outro: 4.2, chapter: 2.4, shot: 3.2, pair: 4 }[sc.kind]
  const min = sc.min ?? (fast ? Math.min(base, 2.2) : base)
  const dur = Math.max(min, lead + vd + tail)
  const out = { ...sc, start: t, dur, voiceAt: t + lead, voiceDur: vd, voiceFile: v?.file }
  // En títulos, si la voz repite el texto en pantalla, no se duplica como subtítulo.
  const norm = (x) => (x || '').toLowerCase().replace(/[^a-záéíóúñü ]/g, '').trim()
  const repeats = sc.kind !== 'shot' && sc.kind !== 'pair' && [sc.title, sc.sub].some((x) => x && norm(sc.say).includes(norm(x)))
  out.subs = repeats ? [] : chunk(sc.say || '', vertical ? 34 : 52)
  // Reparto por longitud de texto dentro de la duración de la voz (tiempos relativos a la escena).
  const total = out.subs.reduce((a, c) => a + c.length, 0) || 1
  let acc = 0
  out.subs = out.subs.map((c) => {
    const t0 = lead + (acc / total) * vd; acc += c.length
    const t1 = lead + (acc / total) * vd + 0.15
    return { t0, t1, text: c }
  })
  t += dur
  return out
})
// Fundido cruzado: cada escena sigue visible `xf` segundos después de su final.
const total = scenes.at(-1).start + scenes.at(-1).dur + xf

/** Divide la narración en frases cortas para subtítulos (≤ max caracteres, cortando en pausas). */
function chunk(text, max) {
  const words = text.split(/\s+/).filter(Boolean); const out = []; let cur = ''
  for (const w of words) {
    const next = cur ? cur + ' ' + w : w
    if (next.length > max && cur) { out.push(cur); cur = w } else cur = next
    if (/[.,:;?!]$/.test(w) && cur.length > max * 0.55) { out.push(cur); cur = '' }
  }
  if (cur) out.push(cur)
  return out
}

const OUT = resolve(ROOT, 'build/out'); mkdirSync(OUT, { recursive: true })
const name = `${vid}-${fmt}`

// ── SRT ──
const srtTime = (s) => { const ms = Math.round(s * 1000); const h = Math.floor(ms / 3600000), m = Math.floor(ms / 60000) % 60, ss = Math.floor(ms / 1000) % 60
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(ss).padStart(2, '0')},${String(ms % 1000).padStart(3, '0')}` }
let n = 1; const srt = []
for (const sc of scenes) for (const s of sc.subs) srt.push(`${n++}\n${srtTime(sc.start + s.t0)} --> ${srtTime(sc.start + s.t1)}\n${s.text}\n`)

// ── Cuadros ──
const browser = await chromium.launch({ args: ['--font-render-hinting=none'] })
const page = await browser.newPage({ viewport: { width: W, height: H }, deviceScaleFactor: 1 })
await page.goto(pathToFileURL(resolve(ROOT, 'src/compositor.html')).href)
const cfg = { W, H, xf, assets: pathToFileURL(resolve(ROOT, 'build/shots')).href + '/', scenes,
  product: 'Flow', partner: vid === 'integracion' ? 'ArgosISP' : null }
await page.evaluate((c) => window.setup(c), cfg)

if (preview) {
  for (const ts of preview.split(',').map(Number)) {
    await page.evaluate((x) => window.renderAt(x), ts)
    await page.screenshot({ path: resolve(OUT, `${name}-preview-${ts}.jpg`), type: 'jpeg', quality: 85 })
  }
  console.log('escenas', scenes.map((s) => `${s.kind}@${s.start.toFixed(1)}+${s.dur.toFixed(1)}`).join(' '), 'total', total.toFixed(1))
  await browser.close(); process.exit(0)
}

const silent = resolve(OUT, `${name}.video.mp4`)
const ff = spawn('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'image2pipe', '-framerate', String(fps), '-c:v', 'mjpeg', '-i', '-',
  '-c:v', 'libx264', '-preset', 'medium', '-crf', '18', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', silent], { stdio: ['pipe', 'inherit', 'inherit'] })
const frames = Math.ceil(total * fps)
const t0 = Date.now()
for (let f = 0; f < frames; f++) {
  await page.evaluate((x) => window.renderAt(x), f / fps)
  const buf = await page.screenshot({ type: 'jpeg', quality: 93 })
  if (!ff.stdin.write(buf)) await new Promise((r) => ff.stdin.once('drain', r))
  if (f % (fps * 10) === 0) console.log(`${name}: ${f}/${frames} cuadros (${((Date.now() - t0) / 1000).toFixed(0)} s)`)
}
ff.stdin.end(); await new Promise((r) => ff.on('close', r))
await browser.close()

// ── Audio: voces en su sitio + música con compresión lateral (baja cuando habla la voz) ──
const music = resolve(ROOT, `build/music/${vid}.wav`)
const inputs = []; const filters = []; const labels = []
scenes.forEach((sc) => { if (sc.voiceFile) inputs.push(sc) })
const ffArgs = ['-y', '-loglevel', 'error']
inputs.forEach((sc) => ffArgs.push('-i', sc.voiceFile))
ffArgs.push('-i', music)
inputs.forEach((sc, i) => {
  const d = Math.round(sc.voiceAt * 1000)
  filters.push(`[${i}:a]aresample=48000,aformat=channel_layouts=mono,highpass=f=70,acompressor=threshold=-20dB:ratio=3:attack=5:release=80,adelay=${d}|${d}[v${i}]`)
  labels.push(`[v${i}]`)
})
const mi = inputs.length
filters.push(`${labels.join('')}amix=inputs=${labels.length}:normalize=0,apad,atrim=0:${total.toFixed(3)},asplit=2[voz][key]`)
filters.push(`[${mi}:a]aresample=48000,atrim=0:${total.toFixed(3)},afade=t=out:st=${(total - 2.5).toFixed(2)}:d=2.5,volume=0.55[mus]`)
filters.push(`[mus][key]sidechaincompress=threshold=0.03:ratio=6:attack=40:release=500[duck]`)
filters.push(`[voz][duck]amix=inputs=2:normalize=0,loudnorm=I=-16:TP=-1.5:LRA=9,aformat=channel_layouts=stereo[a]`)
const audio = resolve(OUT, `${name}.m4a`)
execFileSync('ffmpeg', [...ffArgs, '-filter_complex', filters.join(';'), '-map', '[a]', '-c:a', 'aac', '-b:a', '192k', '-ar', '48000', audio])
execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-i', silent, '-i', audio, '-c', 'copy', '-shortest', '-movflags', '+faststart', resolve(OUT, `${name}.mp4`)])
writeFileSync(resolve(OUT, `${name}.srt`), srt.join('\n'))
console.log('listo', resolve(OUT, `${name}.mp4`), total.toFixed(1), 's')
