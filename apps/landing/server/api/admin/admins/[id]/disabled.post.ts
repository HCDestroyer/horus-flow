// POST /api/admin/admins/:id/disabled — { disabled: true|false }. No se puede desactivar a uno
// mismo ni al último administrador activo.
import { z } from 'zod'
import { setAdminDisabled } from '../../../../lib/auth/service'

const schema = z.object({ disabled: z.boolean() })

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, schema)
  try {
    setAdminDisabled(adminAuthDeps(), adminId(event), body.disabled, adminActor(event))
    return { ok: true }
  } catch (err) {
    adminFail(event, err)
  }
})
