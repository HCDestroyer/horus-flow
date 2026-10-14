// POST /api/admin/admins — alta de un administrador con contraseña inicial. Deberá dar de alta
// su TOTP en el primer acceso.
import { z } from 'zod'
import { createAdmin } from '../../../lib/auth/service'

const schema = z.object({
  email: z.string().trim().max(254),
  name: z.string().trim().max(100).optional().default(''),
  password: z.string().max(256),
})

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, schema)
  try {
    const id = await createAdmin(adminAuthDeps(), body, adminActor(event))
    return { id }
  } catch (err) {
    adminFail(event, err)
  }
})
