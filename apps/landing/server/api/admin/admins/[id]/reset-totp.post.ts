// POST /api/admin/admins/:id/reset-totp — obliga a dar de alta el TOTP de nuevo (cierra sus sesiones).
import { resetTotp } from '../../../../lib/auth/service'

export default defineEventHandler((event) => {
  try {
    resetTotp(adminAuthDeps(), adminId(event), adminActor(event))
    return { ok: true }
  } catch (err) {
    adminFail(event, err)
  }
})
