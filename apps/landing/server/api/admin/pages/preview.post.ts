// POST /api/admin/pages/preview — vista previa: convierte Markdown con el mismo conversor seguro
// que la web (server/lib/markdown.ts). No guarda nada.
import { z } from 'zod'
import { MARKDOWN_MAX, renderMarkdown } from '../../../lib/markdown'

const schema = z.object({ markdown: z.string().max(MARKDOWN_MAX) })

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, schema, { maxBytes: PAGE_BODY_MAX_BYTES })
  return { html: renderMarkdown(body.markdown) }
})
