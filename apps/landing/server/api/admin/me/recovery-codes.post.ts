// POST /api/admin/me/recovery-codes — códigos de recuperación nuevos (anula los anteriores).
import { regenerateRecoveryCodes } from '../../../lib/auth/service'

export default defineEventHandler((event) => {
  const s = adminSession(event)
  return { recoveryCodes: regenerateRecoveryCodes(adminAuthDeps(), s.adminId, adminActor(event)) }
})
