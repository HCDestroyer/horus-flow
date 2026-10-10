// Compresión de las páginas HTML renderizadas (brotli o gzip según Accept-Encoding). Los
// recursos estáticos ya se sirven precomprimidos (nitro.compressPublicAssets). Si el proxy
// (Nginx Proxy Manager) ya comprime, no pasa nada: no recomprime una respuesta con
// Content-Encoding.
import { brotliCompressSync, constants, gzipSync } from 'node:zlib'

export default defineNitroPlugin((nitro) => {
  nitro.hooks.hook('render:response', (response, { event }) => {
    if (typeof response.body !== 'string' || response.body.length < 1024) return
    const type = String(response.headers?.['content-type'] ?? '')
    if (type && !type.includes('text/html')) return
    const accept = getRequestHeader(event, 'accept-encoding') ?? ''
    let encoding: 'br' | 'gzip' | null = null
    if (/\bbr\b/.test(accept)) encoding = 'br'
    else if (/\bgzip\b/.test(accept)) encoding = 'gzip'
    if (!encoding) return
    const raw = Buffer.from(response.body, 'utf8')
    const body =
      encoding === 'br'
        ? brotliCompressSync(raw, { params: { [constants.BROTLI_PARAM_QUALITY]: 5 } })
        : gzipSync(raw, { level: 6 })
    response.body = body as unknown as string
    response.headers = {
      ...response.headers,
      'content-encoding': encoding,
      vary: 'Accept-Encoding',
    }
  })
})
