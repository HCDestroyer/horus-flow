// POST /api/admin/auth/logout — cierra la sesión actual.
import { audit, destroySession } from '../../../lib/auth/service'

export default defineEventHandler((event) => {
  const s = adminSession(event)
  destroySession(adminAuthDeps(), s.idHash)
  if (s.stage === 'full')
    audit(adminAuthDeps(), adminActor(event), 'auth.logout', 'admin', s.adminId)
  clearAdminSessionCookie(event)
  return { ok: true }
})
