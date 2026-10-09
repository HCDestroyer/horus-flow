// Servidor estático de producción de la SPA (imagen `horus-web`, apps/frontend/Dockerfile).
// Sirve la salida de `nuxt generate` detrás de Traefik, que enruta aquí todo lo que no es
// `/api` ni `/ws` (frontend.md §16: SPA estática detrás de Traefik). Sin dependencias.
//
// - Fallback de SPA a 200.html (rutas del cliente como /t/fibra-norte/clients).
// - `/_nuxt/*` con hash en el nombre: caché inmutable de un año; el resto, `no-cache`.
// - Cabeceras de seguridad y CSP de security.md (STRIDE T): connect-src 'self' basta porque
//   la API y el WebSocket están en el mismo origen. Los scripts en línea que genera Nuxt
//   (importmap, tema inicial, configuración pública) se permiten por su hash SHA-256,
//   calculado al arrancar: nada de 'unsafe-inline' en scripts.
// - Nunca sale del directorio raíz (path traversal → 404), solo GET/HEAD, `/healthz`.
// - Pensado para correr sin root y con el sistema de archivos de solo lectura.
/* eslint-disable no-console -- proceso de servidor */
import { createHash } from 'node:crypto'
import { createReadStream, existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { createServer } from 'node:http'
import { extname, join, normalize, resolve, sep } from 'node:path'

const root = resolve(process.env.HORUS_WEB_ROOT ?? join(import.meta.dirname, '../.output/public'))
const port = Number(process.env.HORUS_WEB_PORT ?? 8081)
const host = process.env.HORUS_WEB_HOST ?? '0.0.0.0'

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
  '.txt': 'text/plain; charset=utf-8',
  '.webmanifest': 'application/manifest+json',
}

if (!existsSync(join(root, 'index.html'))) {
  console.error(`No existe ${root}/index.html: ejecuta antes "pnpm build".`)
  process.exit(1)
}

/** Hashes CSP de los <script> en línea ejecutables de los HTML generados. */
function inlineScriptHashes() {
  const hashes = new Set()
  for (const name of readdirSync(root).filter((f) => f.endsWith('.html'))) {
    const html = readFileSync(join(root, name), 'utf8')
    for (const m of html.matchAll(/<script(?![^>]*\ssrc=)([^>]*)>([\s\S]*?)<\/script>/g)) {
      if (/type="application\/(ld\+)?json"/.test(m[1])) continue
      hashes.add(`'sha256-${createHash('sha256').update(m[2], 'utf8').digest('base64')}'`)
    }
  }
  return [...hashes]
}

const CSP = [
  "default-src 'self'",
  ["script-src 'self'", ...inlineScriptHashes()].join(' '),
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob:",
  "connect-src 'self'",
  "worker-src 'self' blob:",
  "object-src 'none'",
  "frame-ancestors 'none'",
  "base-uri 'none'",
  "form-action 'self'",
].join('; ')

const SECURITY = {
  'Content-Security-Policy': CSP,
  'X-Content-Type-Options': 'nosniff',
  'Referrer-Policy': 'no-referrer',
  'X-Frame-Options': 'DENY',
  'Cross-Origin-Opener-Policy': 'same-origin',
  'Permissions-Policy': 'camera=(), microphone=(), geolocation=(), payment=()',
}

const fallback = existsSync(join(root, '200.html')) ? '200.html' : 'index.html'

function resolveFile(pathname) {
  let decoded
  try {
    decoded = decodeURIComponent(pathname)
  } catch {
    return null
  }
  if (decoded.includes('\0')) return null
  const file = normalize(join(root, decoded))
  if (file !== root && !file.startsWith(root + sep)) return null
  if (existsSync(file) && statSync(file).isFile()) return file
  const index = join(file, 'index.html')
  if (existsSync(index)) return index
  // Archivos con extensión que no existen (p. ej. /_nuxt/viejo.js): 404, no la SPA.
  if (extname(decoded)) return null
  return join(root, fallback)
}

createServer((req, res) => {
  const url = new URL(req.url ?? '/', 'http://localhost')
  if (url.pathname === '/healthz') {
    res.writeHead(200, { 'Content-Type': 'text/plain', 'Cache-Control': 'no-store' }).end('ok')
    return
  }
  if (req.method !== 'GET' && req.method !== 'HEAD') {
    res.writeHead(405, { Allow: 'GET, HEAD', ...SECURITY }).end()
    return
  }
  const file = resolveFile(url.pathname)
  if (!file) {
    res
      .writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8', ...SECURITY })
      .end('No encontrado')
    return
  }
  const immutable = url.pathname.startsWith('/_nuxt/') && !file.endsWith('.html')
  res.writeHead(200, {
    'Content-Type': TYPES[extname(file)] ?? 'application/octet-stream',
    'Content-Length': statSync(file).size,
    'Cache-Control': immutable ? 'public, max-age=31536000, immutable' : 'no-cache',
    ...SECURITY,
  })
  if (req.method === 'HEAD') {
    res.end()
    return
  }
  createReadStream(file).pipe(res)
}).listen(port, host, () => {
  console.log(JSON.stringify({ msg: 'horus-web escuchando', host, port, root }))
})
