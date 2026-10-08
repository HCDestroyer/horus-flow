// Servidor estático mínimo para los e2e: sirve `.output/public` (salida de `nuxt generate`)
// con fallback de SPA, como hará Traefik en producción. Sin dependencias.
/* eslint-disable no-console -- script de línea de comandos */
import { createReadStream, existsSync, statSync } from 'node:fs'
import { createServer } from 'node:http'
import { extname, join, normalize, resolve } from 'node:path'

const root = resolve(import.meta.dirname, '../../.output/public')
const port = Number(process.env.PORT ?? 4173)

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
}

if (!existsSync(root)) {
  console.error(`No existe ${root}. Ejecuta antes "pnpm build".`)
  process.exit(1)
}

const fallback = existsSync(join(root, '200.html')) ? '200.html' : 'index.html'

createServer((req, res) => {
  const url = new URL(req.url ?? '/', 'http://localhost')
  let file = normalize(join(root, decodeURIComponent(url.pathname)))
  if (!file.startsWith(root)) {
    res.writeHead(403).end()
    return
  }
  if (!existsSync(file) || statSync(file).isDirectory()) {
    const index = join(file, 'index.html')
    file = existsSync(index) ? index : join(root, fallback)
  }
  res.writeHead(200, {
    'Content-Type': TYPES[extname(file)] ?? 'application/octet-stream',
    'Cache-Control': 'no-store',
  })
  createReadStream(file).pipe(res)
}).listen(port, '127.0.0.1', () => {
  console.log(`Horus Flow (estático) en http://127.0.0.1:${port}`)
})
