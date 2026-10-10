// DELETE /api/admin/admins/:id — baja de un administrador (no uno mismo ni el último).
import { deleteAdmin } from '../../../lib/auth/service'

export default defineEventHandler((event) => {
  try {
    deleteAdmin(adminAuthDeps(), adminId(event), adminActor(event))
    return { ok: true }
  } catch (err) {
    adminFail(event, err)
  }
})
