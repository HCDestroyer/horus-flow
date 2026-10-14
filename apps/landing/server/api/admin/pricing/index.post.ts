// POST /api/admin/pricing — publica el catálogo (precios, planes, lo que incluye). Guarda una
// versión en el historial, audita antes/después e invalida la caché de la landing: el cambio
// se ve en la web en el siguiente render.
import { z } from 'zod'
import { audit } from '../../../lib/auth/service'
import { publishCatalog } from '../../../lib/catalog'
import { useApp } from '../../../lib/context'

const schema = z.object({ catalog: z.unknown(), note: z.string().trim().max(200).optional() })

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, schema)
  const deps = adminAuthDeps()
  const actor = adminActor(event)
  try {
    const r = publishCatalog(deps.db, body.catalog, actor, { note: body.note })
    audit(deps, actor, 'pricing.publish', 'pricing', r.version, r.before, r.after)
    useApp().cache.invalidate()
    return { version: r.version, catalog: r.after }
  } catch (err) {
    if (err instanceof z.ZodError) {
      throw createError({
        statusCode: 422,
        data: {
          code: 'VALIDATION',
          issues: err.issues
            .slice(0, 20)
            .map((i) => ({ path: i.path.join('.'), message: i.message })),
        },
      })
    }
    throw err
  }
})
