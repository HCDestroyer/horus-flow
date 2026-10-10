// POST /api/admin/me/password — cambiar la propia contraseña (cierra las demás sesiones).
import { z } from 'zod'
import { changePassword } from '../../../lib/auth/service'

const schema = z.object({ current: z.string().min(1).max(256), next: z.string().min(1).max(256) })

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, schema)
  const s = adminSession(event)
  try {
    await changePassword(
      adminAuthDeps(),
      s.adminId,
      body.current,
      body.next,
      adminActor(event),
      s.idHash,
    )
    return { ok: true }
  } catch (err) {
    adminFail(event, err)
  }
})
