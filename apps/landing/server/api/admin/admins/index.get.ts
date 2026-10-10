// GET /api/admin/admins — administradores (sin hashes ni secretos).
import { listAdmins } from '../../../lib/auth/service'

export default defineEventHandler((event) => ({
  admins: listAdmins(adminAuthDeps().db),
  me: adminSession(event).adminId,
}))
