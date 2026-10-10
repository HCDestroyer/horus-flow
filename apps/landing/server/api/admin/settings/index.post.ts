// POST /api/admin/settings — guarda contacto, soporte y banner (auditado; invalida la caché).
import { siteSettingsSchema } from '../../../../shared/catalog'
import { audit } from '../../../lib/auth/service'
import { readSiteSettings, writeSiteSettings } from '../../../lib/catalog'
import { useApp } from '../../../lib/context'

export default defineEventHandler(async (event) => {
  const body = await readAdminBody(event, siteSettingsSchema)
  const app = useApp()
  const before = readSiteSettings(app.db)
  writeSiteSettings(app.db, body)
  const after = readSiteSettings(app.db)
  audit(app.auth(), adminActor(event), 'settings.update', 'settings', '', before, after)
  app.cache.invalidate()
  return after
})
