// GET /api/admin/settings — contacto, soporte (24/7, tiempo de respuesta opcional) y banner.
import { readSiteSettings } from '../../../lib/catalog'

export default defineEventHandler(() => readSiteSettings(adminAuthDeps().db))
