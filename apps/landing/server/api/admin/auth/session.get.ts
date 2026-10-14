// GET /api/admin/auth/session — estado del acceso para el panel (sin datos sensibles).
import type { SessionInfo } from '../../../lib/auth/service'
import { remainingRecoveryCodes } from '../../../lib/auth/service'

export default defineEventHandler((event) => {
  const s = event.context.adminSession as SessionInfo | null
  if (!s) return { authenticated: false as const }
  return {
    authenticated: true as const,
    stage: s.stage,
    email: s.email,
    name: s.name,
    csrf: s.csrf,
    recoveryCodesLeft:
      s.stage === 'full' ? remainingRecoveryCodes(adminAuthDeps().db, s.adminId) : null,
  }
})
